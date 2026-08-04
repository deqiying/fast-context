package search

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/deqiying/fast-context/internal/executor"
)

func TestTrimMessagesKeepsToolPairAndSummary(t *testing.T) {
	messages := []Message{
		{Role: 5, Content: "system prompt"},
		{Role: 1, Content: "Problem Statement: find auth\n\nRepo Map (tree -L 3 /codebase):\n```text\n/codebase\n" + strings.Repeat("├── entry\n", 50) + "```"},
		{Role: 2, Content: "old thinking", ToolCallID: "call-1", ToolName: "restricted_exec", ToolArgsJSON: "{}"},
		{Role: 4, Content: "old result", RefCallID: "call-1"},
		{Role: 2, Content: "new thinking", ToolCallID: "call-2", ToolName: "restricted_exec", ToolArgsJSON: "{}"},
		{Role: 4, Content: "new result", RefCallID: "call-2"},
	}
	state := &trimState{
		query:          "find auth",
		turn:           3,
		recentFiles:    []string{"auth/login.go"},
		recentPatterns: []string{"Login"},
		recentCommands: []string{"rg Login"},
	}
	if !trimMessages(&messages, state) {
		t.Fatal("expected trim to happen")
	}
	if messages[0].Role != 5 {
		t.Fatal("system prompt must stay first")
	}
	joined := ""
	for _, m := range messages {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "Repo Map: (omitted to reduce payload)") {
		t.Fatalf("user message not compacted:\n%s", joined)
	}
	if !strings.Contains(joined, "[Context trimmed to reduce payload size. turn=3]") {
		t.Fatalf("missing progress summary:\n%s", joined)
	}
	if !strings.Contains(joined, "recent_files: auth/login.go") {
		t.Fatalf("missing recent files in summary:\n%s", joined)
	}
	// The latest tool pair must survive.
	if !strings.Contains(joined, "new thinking") || !strings.Contains(joined, "new result") {
		t.Fatalf("latest tool pair dropped:\n%s", joined)
	}
	if strings.Contains(joined, "old result") {
		t.Fatalf("stale tool result kept:\n%s", joined)
	}
}

func TestTruncateToolResultsPreserve(t *testing.T) {
	big := strings.Repeat("x", 6000)
	text := "<command1_result>\n" + big + "\n</command1_result><command2_result>\nshort\n</command2_result>"
	got := truncateToolResultsPreserve(text, 4000, 5000)
	if !strings.Contains(got, "...[truncated]...") {
		t.Fatal("oversized block not truncated")
	}
	if !strings.Contains(got, "<command2_result>") {
		t.Fatal("later block lost")
	}
	if len(got) > 5200 {
		t.Fatalf("output exceeds budget: %d", len(got))
	}
}

func TestRetryQueryTokens(t *testing.T) {
	tokens := retryQueryTokens("Where is the user login authentication handled in code?")
	for _, tok := range tokens {
		if tok == "where" || tok == "code" || tok == "handled" {
			t.Fatalf("stopword leaked: %v", tokens)
		}
	}
	found := 0
	for _, tok := range tokens {
		if tok == "user" || tok == "login" || tok == "authentication" {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("expected user/login/authentication tokens, got %v", tokens)
	}
}

func TestEstimateRequestSizeGrowsWithContent(t *testing.T) {
	small := estimateRequestSize([]Message{{Content: "hi"}}, "{}")
	large := estimateRequestSize([]Message{{Content: strings.Repeat("x", 100000)}}, "{}")
	if large-small < 90000 {
		t.Fatalf("size estimate not tracking content: small=%d large=%d", small, large)
	}
}

func TestTruncateToolResultsPreserveKeepsUTF8AndWrappers(t *testing.T) {
	boundary := strings.Repeat("a", 3999) + "中" + strings.Repeat("b", 2500)
	text := "<command1_result>\n" + boundary + "\n</command1_result>" +
		"<command2_result>\nshort\n</command2_result>"
	got := truncateToolResultsPreserve(text, 4000, 5000)
	if !utf8.ValidString(got) {
		t.Fatalf("tool results are not valid UTF-8: %q", got)
	}
	if len(got) > 5000 {
		t.Fatalf("tool results exceed total budget: %d", len(got))
	}
	for _, tag := range []string{"<command1_result>", "</command1_result>", "<command2_result>", "</command2_result>"} {
		if !strings.Contains(got, tag) {
			t.Fatalf("missing complete wrapper %q in %q", tag, got)
		}
	}
}

func TestRecoveryToolResultBudgetKeepsTwentyWrappers(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "<command%d_result>\n%s\n</command%d_result>", i, strings.Repeat("中", 2000), i)
	}
	got := truncateToolResultsPreserve(b.String(), 4000, recoveryToolResultBudget)
	if len(got) > recoveryToolResultBudget || !utf8.ValidString(got) {
		t.Fatalf("invalid recovery result: bytes=%d valid=%t", len(got), utf8.ValidString(got))
	}
	for i := 1; i <= 20; i++ {
		for _, tag := range []string{fmt.Sprintf("<command%d_result>", i), fmt.Sprintf("</command%d_result>", i)} {
			if !strings.Contains(got, tag) {
				t.Fatalf("missing wrapper %s", tag)
			}
		}
	}
}

func TestProgressSummaryUsesBoundedUTF8Content(t *testing.T) {
	state := &trimState{query: "查询", turn: 2}
	commands := make([]executor.NamedCommand, 0, 40)
	for i := 0; i < 40; i++ {
		commands = append(commands, executor.NamedCommand{
			Name: fmt.Sprintf("command%d", i+1),
			Command: executor.Command{
				Type:    "rg",
				Pattern: fmt.Sprintf("needle-%d-%s", i, strings.Repeat("中", 1000)),
			},
		})
	}
	state.record(commands)
	messages := []Message{
		{Role: 5, Content: "system"},
		{Role: 1, Content: "Problem Statement: oversized\n\nRepo Map:\n" + strings.Repeat("entry\n", 2000)},
		{Role: 2, Content: "thinking", ToolCallID: "call", ToolName: "restricted_exec", ToolArgsJSON: "{}"},
		{Role: 4, Content: "result", RefCallID: "call"},
	}
	if !trimMessages(&messages, state) {
		t.Fatal("expected trim to happen")
	}
	found := false
	for _, message := range messages {
		if !strings.HasPrefix(message.Content, "[Context trimmed") {
			continue
		}
		found = true
		if len(message.Content) > maxProgressSummaryBytes || !utf8.ValidString(message.Content) {
			t.Fatalf("invalid progress summary: bytes=%d valid=%t", len(message.Content), utf8.ValidString(message.Content))
		}
		if !strings.Contains(message.Content, progressItemTruncatedMarker) {
			t.Fatalf("oversized summary items were not truncated: %q", message.Content)
		}
	}
	if !found {
		t.Fatal("progress summary was not emitted")
	}
}
