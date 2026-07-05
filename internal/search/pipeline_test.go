package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// scriptedClient replays canned responses; Stream returns a marker consumed
// by ParseResponse.
type scriptedClient struct {
	responses []scriptedResponse
	calls     int
}

type scriptedResponse struct {
	thinking string
	toolCall *ToolCall
	err      error
}

func (c *scriptedClient) FetchJWT(ctx context.Context, apiKey string, timeout time.Duration) (string, error) {
	return "eyJtest.jwt", nil
}

func (c *scriptedClient) CheckRateLimit(ctx context.Context, apiKey, jwt string, timeout time.Duration) (bool, error) {
	return true, nil
}

func (c *scriptedClient) Stream(ctx context.Context, apiKey, jwt string, messages []Message, toolDefs string, timeout time.Duration) ([]byte, error) {
	idx := c.calls
	if idx >= len(c.responses) {
		idx = len(c.responses) - 1
	}
	if c.responses[idx].err != nil {
		c.calls++
		return nil, c.responses[idx].err
	}
	data, _ := json.Marshal(idx)
	c.calls++
	return data, nil
}

func (c *scriptedClient) ParseResponse(data []byte) (string, *ToolCall, error) {
	var idx int
	_ = json.Unmarshal(data, &idx)
	r := c.responses[idx]
	return r.thinking, r.toolCall, nil
}

func answerCall(xml string) *ToolCall {
	return &ToolCall{Name: "answer", Args: map[string]any{"answer": xml}}
}

func testOptions(root string) Options {
	return Options{
		Query:            "user login authentication",
		ProjectRoot:      root,
		TreeDepth:        2,
		MaxTurns:         2,
		MaxCommands:      4,
		MaxResults:       5,
		Timeout:          5 * time.Second,
		RepoMapMode:      "classic",
		BootstrapEnabled: false,
	}
}

func writeProjectFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunPipelineDirectAnswer(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "auth/login.go", "package auth\nfunc Login() {}\nfunc Logout() {}\n")

	client := &scriptedClient{responses: []scriptedResponse{
		{thinking: "found it", toolCall: answerCall(`<ANSWER><file path="/codebase/auth/login.go"><range>1-3</range></file></ANSWER>`)},
	}}
	result, err := RunPipeline(context.Background(), testOptions(root), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "auth/login.go" {
		t.Fatalf("unexpected files: %#v", result.Files)
	}
	if result.Meta.Strategy != "classic" {
		t.Fatalf("strategy = %q", result.Meta.Strategy)
	}
}

func TestRunPipelineSnippets(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "auth/login.go", "package auth\nfunc Login() {}\nfunc Logout() {}\n")

	client := &scriptedClient{responses: []scriptedResponse{
		{thinking: "", toolCall: answerCall(`<ANSWER><file path="/codebase/auth/login.go"><range>1-2</range></file></ANSWER>`)},
	}}
	opts := testOptions(root)
	opts.IncludeSnippets = true
	result, err := RunPipeline(context.Background(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || len(result.Files[0].Snippets) == 0 {
		t.Fatalf("expected snippets, got %#v", result.Files)
	}
	snippet := result.Files[0].Snippets[0]
	if want := "   1 | package auth"; !contains(snippet, want) {
		t.Fatalf("snippet missing numbered line %q:\n%s", want, snippet)
	}
	if !contains(snippet, "```go") {
		t.Fatalf("snippet missing language fence:\n%s", snippet)
	}
}

func TestRunPipelineNoResultRetry(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "src/auth.go", "package src\nfunc Authentication() {}\n")

	// First call: raw response with no parseable answer → triggers retry.
	// Retry call(s): return a real answer.
	client := &retryAwareClient{
		firstResponse: scriptedResponse{thinking: "I could not find anything useful."},
		laterResponse: scriptedResponse{thinking: "", toolCall: answerCall(`<ANSWER><file path="/codebase/auth.go"><range>1-2</range></file></ANSWER>`)},
	}
	opts := testOptions(root)
	result, err := RunPipeline(context.Background(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RetryNotes) == 0 {
		t.Fatal("expected retry notes")
	}
	if len(result.Files) != 1 {
		t.Fatalf("expected recovered file, got %#v\nnotes: %v", result.Files, result.RetryNotes)
	}
	if filepath.Base(result.ProjectRoot) != "src" {
		t.Fatalf("expected narrowed project root, got %s", result.ProjectRoot)
	}
}

type retryAwareClient struct {
	firstResponse scriptedResponse
	laterResponse scriptedResponse
	searches      int
}

func (c *retryAwareClient) FetchJWT(ctx context.Context, apiKey string, timeout time.Duration) (string, error) {
	return "eyJtest.jwt", nil
}

func (c *retryAwareClient) CheckRateLimit(ctx context.Context, apiKey, jwt string, timeout time.Duration) (bool, error) {
	return true, nil
}

func (c *retryAwareClient) Stream(ctx context.Context, apiKey, jwt string, messages []Message, toolDefs string, timeout time.Duration) ([]byte, error) {
	c.searches++
	return []byte(fmt.Sprint(c.searches)), nil
}

func (c *retryAwareClient) ParseResponse(data []byte) (string, *ToolCall, error) {
	if string(data) == "1" {
		return c.firstResponse.thinking, c.firstResponse.toolCall, nil
	}
	return c.laterResponse.thinking, c.laterResponse.toolCall, nil
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
