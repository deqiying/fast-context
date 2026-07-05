package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/repomap"
)

// runBootstrapPhase ports upstream _runBootstrapPhase: a cheap shallow-tree
// pre-pass that collects rg patterns and hot directories for the optimizer.
// Best-effort — any failure just returns whatever was collected so far.
func runBootstrapPhase(ctx context.Context, opts Options, client Client, apiKey, jwt, projectRoot string, excludePaths []string, progress func(string)) *repomap.BootstrapHints {
	log := func(msg string) { progress("[bootstrap] " + msg) }
	hints := &repomap.BootstrapHints{}

	miniMap := repomap.Build(projectRoot, opts.BootstrapTreeDepth, excludePaths)
	systemPrompt := buildBootstrapPrompt(opts.BootstrapMaxTurns, opts.BootstrapMaxCommands)
	userContent := fmt.Sprintf("Problem Statement: %s\n\nRepo Map (tree -L %d /codebase):\n```text\n%s\n```", opts.Query, miniMap.Depth, miniMap.Tree)

	messages := []Message{
		{Role: 5, Content: systemPrompt},
		{Role: 1, Content: userContent},
	}
	toolDefs := buildToolDefinitions(opts.BootstrapMaxCommands)
	execEngine, err := executor.New(projectRoot)
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

		commands := decodeCommands(toolInfo.Args)
		for _, cmd := range commands {
			if cmd.Type == "rg" && cmd.Pattern != "" {
				hints.RGPatterns = append(hints.RGPatterns, cmd.Pattern)
			}
			if cmd.Type == "tree" && cmd.Path != "" {
				if top := extractTopDirFromCodebasePath(cmd.Path); top != "" {
					hints.HotDirs = append(hints.HotDirs, top)
				}
			}
		}

		callID := randomID()
		argsJSON, _ := json.Marshal(toolInfo.Args)
		results := execEngine.ExecToolCall(ctx, commands)
		messages = append(messages, Message{
			Role:         2,
			Content:      thinking,
			ToolCallID:   callID,
			ToolName:     "restricted_exec",
			ToolArgsJSON: string(argsJSON),
		})
		messages = append(messages, Message{Role: 4, Content: results, RefCallID: callID})
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
