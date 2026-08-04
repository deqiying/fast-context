package search

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/textutil"
)

const (
	recoveryToolResultBudget       = 20_000
	maxAssistantThinkingBytes      = 8 * 1024
	maxProgressSummaryBytes        = 8 * 1024
	maxProgressSummaryItemBytes    = 512
	maxProgressSummaryQueryBytes   = 2 * 1024
	toolResultTruncatedMarker      = "\n...[truncated]..."
	progressItemTruncatedMarker    = "...[truncated]"
	progressSummaryTruncatedMarker = "\n...[progress summary truncated]..."
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

func (s *trimState) record(commands []executor.NamedCommand) {
	for _, named := range commands {
		cmd := named.Command
		switch cmd.Type {
		case "rg":
			if cmd.Pattern != "" {
				pattern := compactProgressItem(cmd.Pattern)
				s.recentPatterns = append(s.recentPatterns, pattern)
				s.recentCommands = append(s.recentCommands, "rg "+pattern)
			}
		case "readfile":
			if cmd.File != "" {
				short := compactProgressItem(strings.TrimPrefix(cmd.File, "/codebase/"))
				s.recentFiles = append(s.recentFiles, short)
				s.recentCommands = append(s.recentCommands, "read "+short)
			}
		case "tree":
			if cmd.Path != "" {
				s.recentCommands = append(s.recentCommands, "tree "+compactProgressItem(cmd.Path))
			}
		}
	}
	s.recentCommands = tailSlice(s.recentCommands, 12)
	s.recentFiles = tailUnique(s.recentFiles, 20)
	s.recentPatterns = tailUnique(s.recentPatterns, 30)
}

var commandResultRe = regexp.MustCompile(`(?s)<(command\d+)_result>\n(.*?)\n</(command\d+)_result>`)

type toolResultBlock struct {
	key  string
	body string
}

func truncateToolResultsPreserve(text string, maxPerBlock, maxTotal int) string {
	text, _ = textutil.NormalizeUTF8(text)
	if maxTotal <= 0 {
		return ""
	}
	matches := commandResultRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		out, _ := textutil.TruncateUTF8Bytes(text, maxTotal, "\n...[tool results truncated]...")
		return out
	}
	blocks := make([]toolResultBlock, 0, len(matches))
	for _, m := range matches {
		if m[1] != m[3] {
			continue
		}
		body := m[2]
		if maxPerBlock > 0 {
			body, _ = textutil.TruncateUTF8Bytes(body, maxPerBlock, toolResultTruncatedMarker)
		}
		blocks = append(blocks, toolResultBlock{key: m[1], body: body})
	}
	if len(blocks) == 0 {
		out, _ := textutil.TruncateUTF8Bytes(text, maxTotal, "\n...[tool results truncated]...")
		return out
	}
	return formatToolResultBlocks(blocks, maxTotal)
}

func formatToolResultBlocks(blocks []toolResultBlock, maxTotal int) string {
	fullBlocks := make([]string, len(blocks))
	lengths := make([]int, len(blocks))
	baseBytes := 0
	for i, block := range blocks {
		prefix := "<" + block.key + "_result>\n"
		suffix := "\n</" + block.key + "_result>"
		fullBlocks[i] = prefix + block.body + suffix
		lengths[i] = len(block.body)
		baseBytes += len(prefix) + len(suffix)
	}
	full := strings.Join(fullBlocks, "")
	if len(full) <= maxTotal {
		return full
	}
	bodyBudget := maxTotal - baseBytes
	if bodyBudget < 0 {
		bodyBudget = 0
	}
	allocations := fairByteAllocations(lengths, bodyBudget)
	var b strings.Builder
	for i, block := range blocks {
		body, _ := textutil.TruncateUTF8Bytes(block.body, allocations[i], toolResultTruncatedMarker)
		fmt.Fprintf(&b, "<%s_result>\n%s\n</%s_result>", block.key, body, block.key)
	}
	return b.String()
}

func fairByteAllocations(lengths []int, budget int) []int {
	allocations := make([]int, len(lengths))
	remaining := make([]int, len(lengths))
	for i := range lengths {
		remaining[i] = i
	}
	for len(remaining) > 0 && budget > 0 {
		share := budget / len(remaining)
		if share == 0 {
			for i := 0; i < budget && i < len(remaining); i++ {
				allocations[remaining[i]]++
			}
			break
		}
		next := remaining[:0]
		usedShort := false
		for _, index := range remaining {
			if lengths[index] <= share {
				allocations[index] = lengths[index]
				budget -= lengths[index]
				usedShort = true
				continue
			}
			next = append(next, index)
		}
		if usedShort {
			remaining = next
			continue
		}
		for _, index := range remaining {
			allocations[index] = share
			budget -= share
		}
		for i := 0; budget > 0 && i < len(remaining); i++ {
			allocations[remaining[i]]++
			budget--
		}
		break
	}
	return allocations
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
	originalSystem := systemMsg
	originalUser := userMsg
	normalizeMessage(&systemMsg)
	normalizeMessage(&userMsg)
	baseChanged := systemMsg != originalSystem || userMsg != originalUser

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
		q, _ = textutil.TruncateUTF8Bytes(q, maxProgressSummaryQueryBytes, progressItemTruncatedMarker)
		compact := fmt.Sprintf("Problem Statement: %s\n\nRepo Map: (omitted to reduce payload). Use tree/rg to explore structure if needed.", q)
		compact, _ = textutil.NormalizeUTF8(compact)
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
	summary, _ := textutil.TruncateUTF8Bytes(
		strings.Join(summaryLines, "\n"),
		maxProgressSummaryBytes,
		progressSummaryTruncatedMarker,
	)
	summaryMsg := Message{Role: 1, Content: summary}
	normalizeMessage(&summaryMsg)

	// Shrink oversized tail messages to avoid immediate re-overflow.
	tailChanged := false
	for i := range tail {
		before := tail[i]
		normalizeMessage(&tail[i])
		switch tail[i].Role {
		case 2:
			tail[i].Content, _ = textutil.TruncateUTF8Bytes(
				tail[i].Content,
				maxAssistantThinkingBytes,
				"\n...[assistant content truncated]...",
			)
		case 4:
			tail[i].Content = truncateToolResultsPreserve(tail[i].Content, 4000, recoveryToolResultBudget)
		}
		if tail[i] != before {
			tailChanged = true
		}
	}

	willDropHistory := tailStart > 2
	if !didCompactUser && !willDropHistory && !tailChanged && !baseChanged {
		return false
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

func normalizeMessage(message *Message) {
	message.Content, _ = textutil.NormalizeUTF8(message.Content)
	message.ToolCallID, _ = textutil.NormalizeUTF8(message.ToolCallID)
	message.ToolName, _ = textutil.NormalizeUTF8(message.ToolName)
	message.ToolArgsJSON, _ = textutil.NormalizeUTF8(message.ToolArgsJSON)
	message.RefCallID, _ = textutil.NormalizeUTF8(message.RefCallID)
}

func compactProgressItem(value string) string {
	value, _ = textutil.NormalizeUTF8(value)
	value, _ = textutil.TruncateUTF8Bytes(value, maxProgressSummaryItemBytes, progressItemTruncatedMarker)
	return value
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
