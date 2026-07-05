package search

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/credentials"
	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/repomap"
)

// maxProtoBytes triggers a preflight context trim before sending (upstream
// MAX_PROTO_BYTES).
const maxProtoBytes = 320 * 1024

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
	effectiveExcludes := config.MergeExcludePaths(opts.ExcludePaths)

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

	execEngine, err := executor.New(projectRoot)
	if err != nil {
		return Result{}, err
	}
	rgPath, _ := executor.FindRipgrep()

	var hints *repomap.BootstrapHints
	if opts.RepoMapMode == "bootstrap_hotspot" && opts.BootstrapEnabled {
		hints = runBootstrapPhase(ctx, opts, client, keyInfo.APIKey, jwt, projectRoot, effectiveExcludes, progress)
		progress(fmt.Sprintf("Bootstrap hints: patterns=%d, hot_dirs=%d", len(hints.RGPatterns), len(hints.HotDirs)))
	}

	repoMap := repomap.BuildOptimized(opts.Query, projectRoot, opts.TreeDepth, effectiveExcludes, repomap.OptimizerOptions{
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
	messages := []Message{
		{Role: 5, Content: buildSystemPrompt(opts.MaxTurns, opts.MaxCommands, opts.MaxResults)},
		{Role: 1, Content: userContent},
	}
	toolDefs := buildToolDefinitions(opts.MaxCommands)
	totalAPICalls := opts.MaxTurns + 1
	compensatedTurns := 0
	const maxCompensations = 2
	forceAnswerInjected := false
	state := &trimState{query: opts.Query}

	for turn := 0; turn < totalAPICalls+compensatedTurns; turn++ {
		progress(fmt.Sprintf("Turn %d/%d", turn+1, totalAPICalls))
		state.turn = turn + 1

		// Preflight trim: proactively reduce payload before sending.
		if size := estimateRequestSize(messages, toolDefs); size > maxProtoBytes && len(messages) > 1 {
			progress(fmt.Sprintf("Estimated payload %d bytes > %d. Trimming context before request...", size, maxProtoBytes))
			if trimMessages(&messages, state) {
				meta.ContextTrimmed = true
			}
		}

		data, err := client.Stream(ctx, keyInfo.APIKey, jwt, messages, toolDefs, opts.Timeout)
		if err != nil {
			meta.ErrorCode = errorCode(err)
			if (meta.ErrorCode == "PAYLOAD_TOO_LARGE" || meta.ErrorCode == "TIMEOUT") && len(messages) > 1 {
				progress(fmt.Sprintf("%s on turn %d: trimming context and retrying...", meta.ErrorCode, turn+1))
				trimMessages(&messages, state)
				meta.ContextTrimmed = true
				data, err = client.Stream(ctx, keyInfo.APIKey, jwt, messages, toolDefs, opts.Timeout)
			}
			if err != nil {
				meta.ErrorCode = errorCode(err)
				return Result{Files: nil, Meta: meta}, err
			}
		}

		thinking, toolInfo, err := client.ParseResponse(data)
		if err != nil {
			return Result{Meta: meta}, err
		}
		if toolInfo == nil {
			if strings.HasPrefix(thinking, "[Error]") {
				return Result{Meta: meta, Raw: thinking}, errors.New(thinking)
			}
			return Result{Meta: meta, Raw: thinking}, nil
		}

		switch toolInfo.Name {
		case "answer":
			answerXML, _ := toolInfo.Args["answer"].(string)
			progress("Received final answer")
			result := ParseAnswer(answerXML, projectRoot)
			result.RGPatterns = unique(execEngine.CollectedRgPatterns)
			result.Meta = meta
			return result, nil
		case "restricted_exec":
			callID := randomID()
			commands := decodeCommands(toolInfo.Args)
			progress(fmt.Sprintf("Executing %d local commands", len(commands)))
			argsJSON, _ := json.Marshal(toolInfo.Args)
			results := execEngine.ExecToolCall(ctx, commands)
			if len(commands) == 0 && compensatedTurns < maxCompensations {
				compensatedTurns++
				progress(fmt.Sprintf("Turn compensation: no valid commands, extending search by 1 turn (%d/%d)", compensatedTurns, maxCompensations))
			}
			state.record(commands)
			messages = append(messages, Message{
				Role:         2,
				Content:      thinking,
				ToolCallID:   callID,
				ToolName:     "restricted_exec",
				ToolArgsJSON: string(argsJSON),
			})
			messages = append(messages, Message{Role: 4, Content: results, RefCallID: callID})
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

	return Result{
		RGPatterns: unique(execEngine.CollectedRgPatterns),
		Meta:       meta,
	}, errors.New("max turns reached without getting an answer")
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

func decodeCommands(args map[string]any) map[string]executor.Command {
	commands := map[string]executor.Command{}
	for key, value := range args {
		if !strings.HasPrefix(key, "command") {
			continue
		}
		data, err := json.Marshal(value)
		if err != nil {
			continue
		}
		var command executor.Command
		if err := json.Unmarshal(data, &command); err != nil {
			continue
		}
		if command.Type == "" {
			continue
		}
		commands[key] = command
	}
	return commands
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
