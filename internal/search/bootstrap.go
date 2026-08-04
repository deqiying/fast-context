package search

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/pathfilter"
	"github.com/deqiying/fast-context/internal/repomap"
	"github.com/deqiying/fast-context/internal/textutil"
)

// runBootstrapPhase ports upstream _runBootstrapPhase: a cheap shallow-tree
// pre-pass that collects rg patterns and hot directories for the optimizer.
// Best-effort — any failure just returns whatever was collected so far.
func runBootstrapPhase(ctx context.Context, opts Options, client Client, apiKey, jwt, projectRoot string, policy *pathfilter.Policy, progress func(string)) *repomap.BootstrapHints {
	log := func(msg string) { progress("[bootstrap] " + msg) }
	hints := &repomap.BootstrapHints{}

	miniMap := repomap.Build(projectRoot, opts.BootstrapTreeDepth, policy)
	systemPrompt := buildBootstrapPrompt(opts.BootstrapMaxTurns, opts.BootstrapMaxCommands)
	userContent := fmt.Sprintf("Problem Statement: %s\n\nRepo Map (tree -L %d /codebase):\n```text\n%s\n```", opts.Query, miniMap.Depth, miniMap.Tree)
	userContent, _ = textutil.NormalizeUTF8(userContent)

	messages := []Message{
		{Role: 5, Content: systemPrompt},
		{Role: 1, Content: userContent},
	}
	toolDefs := buildToolDefinitions(opts.BootstrapMaxCommands)
	execEngine, err := executor.NewWithPolicy(projectRoot, policy)
	if err != nil {
		return finalizeHints(hints)
	}

	for turn := 0; turn < opts.BootstrapMaxTurns; turn++ {
		log(fmt.Sprintf("Turn %d/%d", turn+1, opts.BootstrapMaxTurns))
		data, err := client.Stream(ctx, apiKey, jwt, messages, toolDefs, opts.Timeout)
		if err != nil {
			log("request failed: " + errorCode(err))
			break
		}
		thinking, toolInfo, err := client.ParseResponse(data)
		if err != nil || toolInfo == nil || toolInfo.Name != "restricted_exec" {
			break
		}

		batch := decodeCommands(toolInfo.Args, opts.BootstrapMaxCommands)
		log(fmt.Sprintf("Tool batch: accepted=%d rejected=%d", len(batch.Commands), batch.Rejected))
		for _, named := range batch.Commands {
			cmd := named.Command
			if cmd.Type == "rg" && cmd.Pattern != "" && (policy == nil || !policy.ExcludedVirtual(projectRoot, cmd.Path)) {
				hints.RGPatterns = append(hints.RGPatterns, cmd.Pattern)
			}
			if cmd.Type == "tree" && cmd.Path != "" {
				if top := extractTopDirFromCodebasePath(cmd.Path); top != "" {
					if policy == nil || !policy.Excluded(filepath.Join(projectRoot, filepath.FromSlash(top))) {
						hints.HotDirs = append(hints.HotDirs, top)
					}
				}
			}
		}

		callID := randomID()
		batchResult := execEngine.ExecToolCall(ctx, batch.Commands)
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
	}

	return finalizeHints(hints)
}

func finalizeHints(hints *repomap.BootstrapHints) *repomap.BootstrapHints {
	hints.RGPatterns = lastUnique(hints.RGPatterns, 30)
	hints.HotDirs = lastUnique(hints.HotDirs, 12)
	return hints
}

func extractTopDirFromCodebasePath(path string) string {
	p := strings.ReplaceAll(path, "\\", "/")
	if !strings.HasPrefix(p, "/codebase") {
		return ""
	}
	rel := strings.TrimPrefix(p, "/codebase")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return ""
	}
	return strings.SplitN(rel, "/", 2)[0]
}

// lastUnique dedupes while keeping first occurrence order, then keeps the
// last n entries (upstream: [...new Set(arr)].slice(-n)).
func lastUnique(values []string, n int) []string {
	seen := map[string]bool{}
	var uniq []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		uniq = append(uniq, v)
	}
	if len(uniq) > n {
		uniq = uniq[len(uniq)-n:]
	}
	return uniq
}
