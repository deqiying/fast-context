package search

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/credentials"
	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/pathfilter"
	"github.com/deqiying/fast-context/internal/repomap"
	"github.com/deqiying/fast-context/internal/textutil"
)

const (
	maxToolArgsJSONBytes    = 16 * 1024
	maxCommandStringBytes   = 2 * 1024
	maxCommandListItems     = 32
	maxCommandListItemBytes = 512
)

var commandIndexRe = regexp.MustCompile(`^command([1-9]\d*)$`)

type decodedCommandBatch struct {
	Commands []executor.NamedCommand
	ArgsJSON string
	Rejected int
}

func Run(ctx context.Context, opts Options, client Client) (Result, error) {
	if client == nil {
		return Result{}, errors.New("missing Windsurf client")
	}
	progress := func(msg string) {
		if opts.Progress != nil {
			opts.Progress(msg)
		}
	}
	opts = normalizeOptions(opts)
	projectRoot, err := filepath.Abs(opts.ProjectRoot)
	if err != nil {
		return Result{}, err
	}
	st, err := os.Stat(projectRoot)
	if err != nil {
		return Result{}, fmt.Errorf("project path does not exist: %s", projectRoot)
	}
	if !st.IsDir() {
		return Result{}, fmt.Errorf("project path is not a directory: %s", projectRoot)
	}
	if err := rejectBroadProjectRoot(projectRoot); err != nil {
		return Result{}, err
	}
	policy := opts.pathPolicy
	if policy == nil {
		policy, err = pathfilter.New(projectRoot, config.MergeExcludePaths(opts.ExcludePaths))
		if err != nil {
			return Result{}, err
		}
	}

	keyInfo, err := credentials.FindAPIKey()
	if err != nil {
		return Result{}, err
	}
	progress("Fetching JWT...")
	jwt, err := client.FetchJWT(ctx, keyInfo.APIKey, opts.Timeout)
	if err != nil {
		return Result{}, err
	}
	progress("Checking rate limit...")
	ok, err := client.CheckRateLimit(ctx, keyInfo.APIKey, jwt, opts.Timeout)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, errors.New("rate limited, please try again later")
	}

	execEngine, err := executor.NewWithPolicy(projectRoot, policy)
	if err != nil {
		return Result{}, err
	}
	rgPath, _ := executor.FindRipgrep()

	var hints *repomap.BootstrapHints
	if opts.RepoMapMode == "bootstrap_hotspot" && opts.BootstrapEnabled {
		hints = runBootstrapPhase(ctx, opts, client, keyInfo.APIKey, jwt, projectRoot, policy, progress)
		progress(fmt.Sprintf("Bootstrap hints: patterns=%d, hot_dirs=%d", len(hints.RGPatterns), len(hints.HotDirs)))
	}

	repoMap := repomap.BuildOptimized(opts.Query, projectRoot, opts.TreeDepth, policy, repomap.OptimizerOptions{
		Mode:               opts.RepoMapMode,
		BootstrapTreeDepth: opts.BootstrapTreeDepth,
		HotspotTopK:        opts.HotspotTopK,
		HotspotTreeDepth:   opts.HotspotTreeDepth,
		MaxBytes:           opts.HotspotMaxBytes,
		RGPath:             rgPath,
	}, hints, progress)
	mapNote := ""
	if repoMap.FellBack {
		mapNote = fmt.Sprintf(" [fell back from L=%d]", opts.TreeDepth)
	}
	if repoMap.AutoDepth {
		mapNote += " [auto]"
	}
	progress(fmt.Sprintf("Repo map: tree -L %d (%.1fKB)%s [strategy=%s]%s",
		repoMap.Depth, float64(repoMap.SizeBytes)/1024, mapNote, repoMap.Strategy, hotDirsNote(repoMap.HotDirs)))

	meta := Meta{
		TreeDepth:    repoMap.Depth,
		HotspotDepth: repoMap.HotspotDepth,
		TreeSizeKB:   float64(repoMap.SizeBytes) / 1024,
		MaxTurns:     opts.MaxTurns,
		MaxResults:   opts.MaxResults,
		MaxCommands:  opts.MaxCommands,
		TimeoutMS:    opts.Timeout.Milliseconds(),
		FellBack:     repoMap.FellBack,
		AutoDepth:    repoMap.AutoDepth,
		Strategy:     repoMap.Strategy,
		HotDirs:      repoMap.HotDirs,
		ProjectRoot:  projectRoot,
	}

	userContent := fmt.Sprintf("Problem Statement: %s\n\nRepo Map (tree -L %d /codebase):\n```text\n%s\n```", opts.Query, repoMap.Depth, repoMap.Tree)
	userContent, _ = textutil.NormalizeUTF8(userContent)
	messages := []Message{
		{Role: 5, Content: buildSystemPrompt(opts.MaxTurns, opts.MaxCommands, opts.MaxResults)},
		{Role: 1, Content: userContent},
	}
	toolDefs := buildToolDefinitions(opts.MaxCommands)
	totalAPICalls := opts.MaxTurns + 1
	compensatedTurns := 0
	const maxCompensations = 2
	forceAnswerInjected := false
	normalizedQuery, _ := textutil.NormalizeUTF8(opts.Query)
	state := &trimState{query: normalizedQuery}
	invalidArgumentRetried := false
	// executedCommands 记录已通过校验并执行的命令，供轮次耗尽时抢救 readfile 证据。
	var executedCommands []executor.NamedCommand

	for turn := 0; turn < totalAPICalls+compensatedTurns; turn++ {
		progress(fmt.Sprintf("Turn %d/%d", turn+1, totalAPICalls))
		state.turn = turn + 1

		// Preflight trim: proactively reduce payload before sending.
		if size := estimateRequestSize(messages, toolDefs); size > MaxRequestProtoBytes && len(messages) > 1 {
			progress(fmt.Sprintf("Estimated payload %d bytes > %d. Trimming context before request...", size, MaxRequestProtoBytes))
			if trimMessages(&messages, state) {
				meta.ContextTrimmed = true
			}
		}

		thinking, toolInfo, err := sendAndParse(ctx, client, keyInfo.APIKey, jwt, messages, toolDefs, opts.Timeout)
		if err != nil {
			code := errorCode(err)
			if (code == "PAYLOAD_TOO_LARGE" || code == "TIMEOUT") && len(messages) > 1 {
				candidate := append([]Message(nil), messages...)
				if trimMessages(&candidate, state) {
					progress(fmt.Sprintf("%s on turn %d: trimming context and retrying...", code, turn+1))
					messages = candidate
					meta.ContextTrimmed = true
					thinking, toolInfo, err = sendAndParse(ctx, client, keyInfo.APIKey, jwt, messages, toolDefs, opts.Timeout)
				}
			}

			if err != nil && errorCode(err) == "invalid_argument" && !invalidArgumentRetried {
				candidate, beforeBytes, afterBytes, ok, sizeErr := buildInvalidArgumentRecovery(
					client,
					keyInfo.APIKey,
					jwt,
					messages,
					toolDefs,
					state,
				)
				if sizeErr != nil {
					progress("Remote invalid_argument recovery skipped: request size could not be verified")
				} else if ok {
					progress(fmt.Sprintf(
						"Remote invalid_argument on turn %d: request protobuf reduced from %d to %d bytes; retrying once",
						turn+1,
						beforeBytes,
						afterBytes,
					))
					messages = candidate
					meta.ContextTrimmed = true
					invalidArgumentRetried = true
					thinking, toolInfo, err = sendAndParse(ctx, client, keyInfo.APIKey, jwt, messages, toolDefs, opts.Timeout)
				}
			}

			if err == nil {
				meta.ErrorCode = ""
			}
		}
		if err != nil {
			var malformedToolCall *MalformedToolCallError
			if errors.As(err, &malformedToolCall) && compensatedTurns < maxCompensations {
				compensatedTurns++
				messages = append(messages, Message{Role: 1, Content: malformedToolCallRetryPrompt})
				progress(fmt.Sprintf("Malformed tool arguments on turn %d: requesting a corrected response (%d/%d)", turn+1, compensatedTurns, maxCompensations))
				continue
			}
			meta.ErrorCode = errorCode(err)
			return Result{Meta: meta}, err
		}
		if toolInfo == nil {
			if strings.HasPrefix(thinking, "[Error]") {
				return Result{Meta: meta, Raw: thinking}, errors.New(thinking)
			}
			result := Result{Meta: meta, Raw: thinking}
			collector := newSalvageCollector(execEngine, projectRoot)
			collector.addText(thinking)
			if files := filterResultFiles(collector.files, policy); len(files) > 0 {
				result.Files = files
				result.RGPatterns = unique(execEngine.CollectedRgPatterns)
				result.Meta.Salvaged = true
				progress(fmt.Sprintf("No answer tool call; salvaged %d files from the model response", len(files)))
			}
			return result, nil
		}

		if toolInfo.Repaired {
			progress(fmt.Sprintf("Tool arguments on turn %d were repaired locally before validation", turn+1))
		}
		switch toolInfo.Name {
		case "answer":
			answerXML, _ := toolInfo.Args["answer"].(string)
			progress("Received final answer")
			result := ParseAnswer(answerXML, projectRoot)
			result.Files = filterResultFiles(result.Files, policy)
			result.RGPatterns = unique(execEngine.CollectedRgPatterns)
			result.Meta = meta
			return result, nil
		case "restricted_exec":
			callID := randomID()
			batch := decodeCommands(toolInfo.Args, opts.MaxCommands)
			progress(fmt.Sprintf("Tool batch: accepted=%d rejected=%d", len(batch.Commands), batch.Rejected))
			progress(fmt.Sprintf("Executing %d local commands", len(batch.Commands)))
			batchResult := execEngine.ExecToolCall(ctx, batch.Commands)
			progress(fmt.Sprintf(
				"Tool results: bytes_before=%d bytes_after=%d truncated=%t utf8_repaired=%t",
				batchResult.BytesBefore,
				batchResult.BytesAfter,
				batchResult.Truncated,
				batchResult.UTF8Repaired,
			))
			if len(batch.Commands) == 0 && compensatedTurns < maxCompensations {
				compensatedTurns++
				progress(fmt.Sprintf("Turn compensation: no valid commands, extending search by 1 turn (%d/%d)", compensatedTurns, maxCompensations))
			}
			state.record(batch.Commands)
			executedCommands = append(executedCommands, batch.Commands...)
			thinking, _ = textutil.TruncateUTF8Bytes(
				thinking,
				maxAssistantThinkingBytes,
				"\n...[assistant content truncated]...",
			)
			messages = append(messages, Message{
				Role:         2,
				Content:      thinking,
				ToolCallID:   callID,
				ToolName:     "restricted_exec",
				ToolArgsJSON: batch.ArgsJSON,
			})
			messages = append(messages, Message{Role: 4, Content: batchResult.Text, RefCallID: callID})
			effectiveTurn := turn - compensatedTurns
			if effectiveTurn >= opts.MaxTurns-1 && !forceAnswerInjected {
				messages = append(messages, Message{Role: 1, Content: finalForceAnswer})
				forceAnswerInjected = true
				progress("Injected force-answer prompt")
			}
		default:
			return Result{Meta: meta, Raw: thinking}, fmt.Errorf("unknown tool call: %s", toolInfo.Name)
		}
	}

	// 轮次耗尽：先尝试从模型已读取的文件与已收集的 rg 关键词中抢救部分结果，
	// 只有确实没有任何可验证证据时才返回错误。
	patterns := unique(execEngine.CollectedRgPatterns)
	collector := newSalvageCollector(execEngine, projectRoot)
	collector.addReadfiles(executedCommands)
	if len(collector.files) > 0 || len(patterns) > 0 {
		meta.Salvaged = true
		progress(fmt.Sprintf("Max turns reached without a final answer; salvaged %d files and %d rg patterns", len(collector.files), len(patterns)))
		return Result{
			Files:      filterResultFiles(collector.files, policy),
			RGPatterns: patterns,
			Meta:       meta,
		}, nil
	}
	return Result{
		RGPatterns: patterns,
		Meta:       meta,
	}, errors.New("max turns reached without getting an answer")
}

func sendAndParse(
	ctx context.Context,
	client Client,
	apiKey, jwt string,
	messages []Message,
	toolDefs string,
	timeout time.Duration,
) (string, *ToolCall, error) {
	data, err := client.Stream(ctx, apiKey, jwt, messages, toolDefs, timeout)
	if err != nil {
		return "", nil, err
	}
	return client.ParseResponse(data)
}

func buildInvalidArgumentRecovery(
	client Client,
	apiKey, jwt string,
	messages []Message,
	toolDefs string,
	state *trimState,
) (candidate []Message, beforeBytes, afterBytes int, ok bool, err error) {
	if !containsToolResult(messages) {
		return nil, 0, 0, false, nil
	}
	beforeBytes, err = client.RequestSize(apiKey, jwt, messages, toolDefs)
	if err != nil {
		return nil, 0, 0, false, err
	}
	candidate = append([]Message(nil), messages...)
	if !trimMessages(&candidate, state) || !hasValidLatestToolPair(candidate) || !messagesValidUTF8(candidate) {
		return nil, beforeBytes, 0, false, nil
	}
	afterBytes, err = client.RequestSize(apiKey, jwt, candidate, toolDefs)
	if err != nil {
		return nil, beforeBytes, 0, false, err
	}
	if afterBytes >= beforeBytes || afterBytes > MaxRequestProtoBytes {
		return nil, beforeBytes, afterBytes, false, nil
	}
	return candidate, beforeBytes, afterBytes, true, nil
}

func containsToolResult(messages []Message) bool {
	for _, message := range messages {
		if message.Role == 4 && message.RefCallID != "" {
			return true
		}
	}
	return false
}

func hasValidLatestToolPair(messages []Message) bool {
	lastResult := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == 4 && messages[i].RefCallID != "" {
			lastResult = i
			break
		}
	}
	if lastResult < 0 {
		return false
	}
	ref := messages[lastResult].RefCallID
	for i := lastResult - 1; i >= 0; i-- {
		if messages[i].Role == 2 && messages[i].ToolCallID == ref {
			return messages[i].ToolName != "" && json.Valid([]byte(messages[i].ToolArgsJSON))
		}
	}
	return false
}

func messagesValidUTF8(messages []Message) bool {
	for _, message := range messages {
		if !utf8.ValidString(message.Content) ||
			!utf8.ValidString(message.ToolCallID) ||
			!utf8.ValidString(message.ToolName) ||
			!utf8.ValidString(message.ToolArgsJSON) ||
			!utf8.ValidString(message.RefCallID) {
			return false
		}
	}
	return true
}

func normalizeOptions(opts Options) Options {
	if opts.ProjectRoot == "" {
		opts.ProjectRoot = "."
	}
	// TreeDepth 0 = auto (resolved in repomap); negative values are clamped.
	if opts.TreeDepth < 0 {
		opts.TreeDepth = 0
	}
	if opts.TreeDepth > 6 {
		opts.TreeDepth = 6
	}
	if opts.MaxTurns == 0 {
		opts.MaxTurns = config.DefaultMaxTurns
	}
	if opts.MaxCommands == 0 {
		opts.MaxCommands = config.DefaultMaxCommands
	}
	if opts.MaxResults == 0 {
		opts.MaxResults = config.DefaultMaxResults
	}
	if opts.Timeout == 0 {
		opts.Timeout = config.DefaultTimeout
	}
	opts.MaxTurns = config.ClampInt(opts.MaxTurns, 1, 5)
	opts.MaxCommands = config.ClampInt(opts.MaxCommands, 1, 20)
	opts.MaxResults = config.ClampInt(opts.MaxResults, 1, 30)

	if opts.RepoMapMode != "classic" {
		opts.RepoMapMode = "bootstrap_hotspot"
	}
	if opts.BootstrapTreeDepth == 0 {
		opts.BootstrapTreeDepth = config.DefaultBootstrapTreeDepth
	}
	if opts.BootstrapMaxTurns == 0 {
		opts.BootstrapMaxTurns = config.DefaultBootstrapMaxTurns
	}
	if opts.BootstrapMaxCommands == 0 {
		opts.BootstrapMaxCommands = config.DefaultBootstrapMaxCommand
	}
	if opts.HotspotTopK == 0 {
		opts.HotspotTopK = config.DefaultHotspotTopK
	}
	if opts.HotspotTreeDepth == 0 {
		opts.HotspotTreeDepth = config.DefaultHotspotTreeDepth
	}
	if opts.HotspotMaxBytes == 0 {
		opts.HotspotMaxBytes = config.DefaultHotspotMaxBytes
	}
	opts.BootstrapTreeDepth = config.ClampInt(opts.BootstrapTreeDepth, 1, 3)
	opts.BootstrapMaxTurns = config.ClampInt(opts.BootstrapMaxTurns, 1, 3)
	opts.BootstrapMaxCommands = config.ClampInt(opts.BootstrapMaxCommands, 1, 8)
	opts.HotspotTopK = config.ClampInt(opts.HotspotTopK, 0, 8)
	opts.HotspotTreeDepth = config.ClampInt(opts.HotspotTreeDepth, 1, 4)
	opts.HotspotMaxBytes = config.ClampInt(opts.HotspotMaxBytes, 16*1024, 256*1024)
	return opts
}

func decodeCommands(args map[string]any, maxCommands int) decodedCommandBatch {
	batch := decodedCommandBatch{ArgsJSON: "{}"}
	type candidate struct {
		name  string
		index int
		value any
	}
	candidates := make([]candidate, 0, len(args))
	for name, value := range args {
		match := commandIndexRe.FindStringSubmatch(name)
		if len(match) != 2 {
			batch.Rejected++
			continue
		}
		index, err := strconv.Atoi(match[1])
		if err != nil || index > maxCommands {
			batch.Rejected++
			continue
		}
		candidates = append(candidates, candidate{name: name, index: index, value: value})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].index < candidates[j].index })
	if len(candidates) == 0 || candidates[0].index != 1 {
		batch.Rejected += len(candidates)
		return batch
	}

	for i, candidate := range candidates {
		command, err := decodeCommand(candidate.value)
		if err != nil {
			batch.Rejected++
			if candidate.index == 1 {
				batch.Rejected += len(candidates) - i - 1
				batch.Commands = nil
				return batch
			}
			continue
		}
		trial := append(append([]executor.NamedCommand(nil), batch.Commands...), executor.NamedCommand{
			Name:    candidate.name,
			Command: command,
		})
		argsJSON, err := marshalValidatedCommands(trial)
		if err != nil || len(argsJSON) > maxToolArgsJSONBytes {
			batch.Rejected += len(candidates) - i
			break
		}
		batch.Commands = trial
		batch.ArgsJSON = argsJSON
	}
	return batch
}

func decodeCommand(value any) (executor.Command, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return executor.Command{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var command executor.Command
	if err := decoder.Decode(&command); err != nil {
		return executor.Command{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return executor.Command{}, err
	}
	if err := validateCommandFields(command.Type, fields); err != nil {
		return executor.Command{}, err
	}
	normalizeCommand(&command)
	if err := validateCommand(command); err != nil {
		return executor.Command{}, err
	}
	return command, nil
}

func validateCommandFields(commandType string, fields map[string]json.RawMessage) error {
	allowedByType := map[string]map[string]bool{
		"rg":       {"type": true, "pattern": true, "path": true, "include": true, "exclude": true},
		"readfile": {"type": true, "file": true, "start_line": true, "end_line": true},
		"tree":     {"type": true, "path": true, "levels": true},
		"ls":       {"type": true, "path": true, "long_format": true, "all": true},
		"glob":     {"type": true, "pattern": true, "path": true, "type_filter": true},
	}
	allowed, ok := allowedByType[commandType]
	if !ok {
		return fmt.Errorf("unsupported command type")
	}
	for field := range fields {
		if !allowed[field] {
			return fmt.Errorf("field %q is not allowed for %s", field, commandType)
		}
	}
	return nil
}

func normalizeCommand(command *executor.Command) {
	command.Type, _ = textutil.NormalizeUTF8(command.Type)
	command.Pattern, _ = textutil.NormalizeUTF8(command.Pattern)
	command.Path, _ = textutil.NormalizeUTF8(command.Path)
	command.File, _ = textutil.NormalizeUTF8(command.File)
	command.TypeFilter, _ = textutil.NormalizeUTF8(command.TypeFilter)
	for i := range command.Include {
		command.Include[i], _ = textutil.NormalizeUTF8(command.Include[i])
	}
	for i := range command.Exclude {
		command.Exclude[i], _ = textutil.NormalizeUTF8(command.Exclude[i])
	}
}

func validateCommand(command executor.Command) error {
	for _, value := range []string{command.Type, command.Pattern, command.Path, command.File, command.TypeFilter} {
		if len(value) > maxCommandStringBytes {
			return fmt.Errorf("command string exceeds byte budget")
		}
	}
	for _, values := range [][]string{command.Include, command.Exclude} {
		if len(values) > maxCommandListItems {
			return fmt.Errorf("command list exceeds item budget")
		}
		for _, value := range values {
			if len(value) > maxCommandListItemBytes {
				return fmt.Errorf("command list item exceeds byte budget")
			}
		}
	}
	switch command.Type {
	case "rg":
		if command.Pattern == "" || command.Path == "" {
			return fmt.Errorf("rg requires pattern and path")
		}
	case "readfile":
		if command.File == "" {
			return fmt.Errorf("readfile requires file")
		}
	case "tree", "ls":
		if command.Path == "" {
			return fmt.Errorf("%s requires path", command.Type)
		}
	case "glob":
		if command.Pattern == "" || command.Path == "" {
			return fmt.Errorf("glob requires pattern and path")
		}
		if command.TypeFilter != "" && command.TypeFilter != "file" && command.TypeFilter != "directory" && command.TypeFilter != "all" {
			return fmt.Errorf("invalid glob type_filter")
		}
	default:
		return fmt.Errorf("unsupported command type")
	}
	return nil
}

func marshalValidatedCommands(commands []executor.NamedCommand) (string, error) {
	args := make(map[string]executor.Command, len(commands))
	for _, command := range commands {
		args[command.Name] = command.Command
	}
	data, err := json.Marshal(args)
	return string(data), err
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if len(value) < 3 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func errorCode(err error) string {
	type coded interface{ Code() string }
	var c coded
	if errors.As(err, &c) {
		return c.Code()
	}
	return "UNKNOWN"
}

func hotDirsNote(hotDirs []string) string {
	if len(hotDirs) == 0 {
		return ""
	}
	return " [hot=" + strings.Join(hotDirs, ",") + "]"
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(buf)
}
