package search

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/deqiying/fast-context/internal/executor"
)

// trimState carries a compact record of recent search activity so trimmed
// context can be replaced with a progress summary (upstream 1.5.2 smart trim).
type trimState struct {
	query          string
	turn           int
	recentFiles    []string
	recentPatterns []string
	recentCommands []string // human-readable descriptions
}

func (s *trimState) record(commands map[string]executor.Command) {
	for _, cmd := range commands {
		switch cmd.Type {
		case "rg":
			if cmd.Pattern != "" {
				s.recentPatterns = append(s.recentPatterns, cmd.Pattern)
				s.recentCommands = append(s.recentCommands, "rg "+cmd.Pattern)
			}
		case "readfile":
			if cmd.File != "" {
				short := strings.TrimPrefix(cmd.File, "/codebase/")
				s.recentFiles = append(s.recentFiles, short)
				s.recentCommands = append(s.recentCommands, "read "+short)
			}
		case "tree":
			if cmd.Path != "" {
				s.recentCommands = append(s.recentCommands, "tree "+cmd.Path)
			}
		}
	}
	s.recentCommands = tailSlice(s.recentCommands, 12)
	s.recentFiles = tailUnique(s.recentFiles, 20)
	s.recentPatterns = tailUnique(s.recentPatterns, 30)
}

var commandResultRe = regexp.MustCompile(`(?s)<(command\d+)_result>\n(.*?)\n</command\d+_result>`)

func truncateToolResultsPreserve(text string, maxPerBlock, maxTotal int) string {
	if len(text) <= maxTotal {
		return text
	}
	matches := commandResultRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text[:maxTotal] + "\n...[tool results truncated]..."
	}
	var parts []string
	total := 0
	for _, m := range matches {
		key, body := m[1], m[2]
		if len(body) > maxPerBlock {
			body = body[:maxPerBlock] + "\n...[truncated]..."
		}
		block := "<" + key + "_result>\n" + body + "\n</" + key + "_result>"
		parts = append(parts, block)
		total += len(block)
		if total > maxTotal {
			break
		}
	}
	out := strings.Join(parts, "")
	if len(out) > maxTotal {
		out = out[:maxTotal] + "\n...[tool results truncated]..."
	}
	return out
}

// trimMessages compacts the conversation: keep the system prompt, compact the
// repo-map user message, keep the latest tool-call/tool-result pair (plus any
// trailing prompts), and insert a progress summary. Returns true if anything
// was actually trimmed.
func trimMessages(messages *[]Message, state *trimState) bool {
	msgs := *messages
	if len(msgs) < 2 {
		return false
	}
	systemMsg := msgs[0]
	userMsg := msgs[1]

	// Find the most recent tool-result and its matching tool-call.
	lastToolResultIdx := -1
	refID := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == 4 && msgs[i].RefCallID != "" {
			lastToolResultIdx = i
			refID = msgs[i].RefCallID
			break
		}
	}
	lastToolCallIdx := -1
	if refID != "" {
		for i := lastToolResultIdx - 1; i >= 0; i-- {
			if msgs[i].Role == 2 && msgs[i].ToolCallID == refID {
				lastToolCallIdx = i
				break
			}
		}
	}

	var tailStart int
	if lastToolResultIdx != -1 {
		if lastToolCallIdx != -1 {
			tailStart = lastToolCallIdx
		} else {
			tailStart = max(2, lastToolResultIdx-1)
		}
	} else {
		tailStart = max(2, len(msgs)-4)
	}
	tail := append([]Message(nil), msgs[tailStart:]...)

	// Compact the repo-map user message; it is usually the largest chunk.
	compactedUser := userMsg
	didCompactUser := false
	if strings.Contains(userMsg.Content, "Repo Map") {
		q := state.query
		if q == "" {
			if m := regexp.MustCompile(`Problem Statement:\s*([^\n]+)`).FindStringSubmatch(userMsg.Content); m != nil {
				q = strings.TrimSpace(m[1])
			}
		}
		compact := fmt.Sprintf("Problem Statement: %s\n\nRepo Map: (omitted to reduce payload). Use tree/rg to explore structure if needed.", q)
		if len(compact) < len(userMsg.Content) {
			compactedUser = Message{Role: userMsg.Role, Content: compact}
			didCompactUser = true
		}
	}

	turnNote := ""
	if state.turn > 0 {
		turnNote = fmt.Sprintf(" turn=%d", state.turn)
	}
	summaryLines := []string{fmt.Sprintf("[Context trimmed to reduce payload size.%s]", turnNote)}
	if len(state.recentCommands) > 0 {
		summaryLines = append(summaryLines, "recent_commands: "+strings.Join(tailSlice(state.recentCommands, 6), " | "))
	}
	if len(state.recentFiles) > 0 {
		summaryLines = append(summaryLines, "recent_files: "+strings.Join(tailSlice(state.recentFiles, 12), ", "))
	}
	if len(state.recentPatterns) > 0 {
		summaryLines = append(summaryLines, "rg_patterns: "+strings.Join(tailSlice(state.recentPatterns, 20), ", "))
	}
	summaryLines = append(summaryLines, "Continue from the most recent tool results kept below.")
	summaryMsg := Message{Role: 1, Content: strings.Join(summaryLines, "\n")}

	willDropHistory := tailStart > 2
	if !didCompactUser && !willDropHistory {
		return false
	}

	// Shrink oversized tail messages to avoid immediate re-overflow.
	for i := range tail {
		switch tail[i].Role {
		case 2:
			if len(tail[i].Content) > 8000 {
				tail[i].Content = tail[i].Content[:8000] + "\n...[assistant content truncated]..."
			}
		case 4:
			if len(tail[i].Content) > 20000 {
				tail[i].Content = truncateToolResultsPreserve(tail[i].Content, 4000, 20000)
			}
		}
	}

	out := make([]Message, 0, len(tail)+3)
	out = append(out, systemMsg)
	if tailStart > 1 {
		out = append(out, compactedUser)
	}
	out = append(out, summaryMsg)
	out = append(out, tail...)
	*messages = out
	return true
}

// estimateRequestSize approximates the proto payload size for preflight
// trimming; message contents dominate the request, so a small per-message
// overhead constant is enough for a 320KB threshold.
func estimateRequestSize(messages []Message, toolDefs string) int {
	size := len(toolDefs) + 512
	for _, m := range messages {
		size += len(m.Content) + len(m.ToolArgsJSON) + len(m.ToolCallID) + len(m.ToolName) + len(m.RefCallID) + 32
	}
	return size
}

func tailSlice(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

func tailUnique(values []string, n int) []string {
	seen := map[string]bool{}
	var reversed []string
	for i := len(values) - 1; i >= 0 && len(reversed) < n; i-- {
		v := values[i]
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		reversed = append(reversed, v)
	}
	out := make([]string, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		out = append(out, reversed[i])
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
