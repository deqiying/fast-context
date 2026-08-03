package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// scriptedClient replays canned responses; Stream returns a marker consumed
// by ParseResponse.
type scriptedClient struct {
	responses []scriptedResponse
	calls     int
	messages  [][]Message
}

type scriptedResponse struct {
	thinking string
	toolCall *ToolCall
	err      error
	parseErr error
}

type callCountingClient struct {
	fetchJWTCalls  int
	rateLimitCalls int
	streamCalls    int
	parseCalls     int
}

func (c *callCountingClient) FetchJWT(context.Context, string, time.Duration) (string, error) {
	c.fetchJWTCalls++
	return "", errors.New("unexpected FetchJWT call")
}

func (c *callCountingClient) CheckRateLimit(context.Context, string, string, time.Duration) (bool, error) {
	c.rateLimitCalls++
	return false, errors.New("unexpected CheckRateLimit call")
}

func (c *callCountingClient) Stream(context.Context, string, string, []Message, string, time.Duration) ([]byte, error) {
	c.streamCalls++
	return nil, errors.New("unexpected Stream call")
}

func (c *callCountingClient) ParseResponse([]byte) (string, *ToolCall, error) {
	c.parseCalls++
	return "", nil, errors.New("unexpected ParseResponse call")
}

func (c *scriptedClient) FetchJWT(ctx context.Context, apiKey string, timeout time.Duration) (string, error) {
	return "eyJtest.jwt", nil
}

func (c *scriptedClient) CheckRateLimit(ctx context.Context, apiKey, jwt string, timeout time.Duration) (bool, error) {
	return true, nil
}

func (c *scriptedClient) Stream(ctx context.Context, apiKey, jwt string, messages []Message, toolDefs string, timeout time.Duration) ([]byte, error) {
	c.messages = append(c.messages, append([]Message(nil), messages...))
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
	return r.thinking, r.toolCall, r.parseErr
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

func TestRunRejectsTruncatedWindsurfKeyBeforeClientCalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FAST_CONTEXT_KEY", "")
	t.Setenv("WINDSURF_API_KEY", "devin-session-token$abc")

	client := &callCountingClient{}
	_, err := Run(context.Background(), testOptions(t.TempDir()), client)
	if err == nil {
		t.Fatal("Run() succeeded with a truncated WINDSURF_API_KEY")
	}
	if client.fetchJWTCalls != 0 || client.rateLimitCalls != 0 || client.streamCalls != 0 || client.parseCalls != 0 {
		t.Fatalf("client called before credential failure: %#v", client)
	}
	if strings.Contains(err.Error(), "devin-session-token$abc") {
		t.Fatalf("Run() error leaked truncated key: %v", err)
	}
	for _, want := range []string{"FAST_CONTEXT_KEY", "$HOME/.config/fast-context/config.json", "WINDSURF_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Run() error missing %q: %v", want, err)
		}
	}
}

func TestRunRejectsMissingCredentialsBeforeClientCalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FAST_CONTEXT_KEY", "")
	t.Setenv("WINDSURF_API_KEY", "")

	client := &callCountingClient{}
	_, err := Run(context.Background(), testOptions(t.TempDir()), client)
	if err == nil {
		t.Fatal("Run() succeeded without credentials")
	}
	if client.fetchJWTCalls != 0 || client.rateLimitCalls != 0 || client.streamCalls != 0 || client.parseCalls != 0 {
		t.Fatalf("client called before credential failure: %#v", client)
	}
	for _, want := range []string{"FAST_CONTEXT_KEY", "$HOME/.config/fast-context/config.json", "WINDSURF_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Run() error missing %q: %v", want, err)
		}
	}
}

func TestRunRejectsTruncatedAutomaticTomlBeforeClientCalls(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("automatic Devin CLI TOML is supported only on Linux/WSL")
	}
	for _, compatibilityKey := range []string{"", "devin-session-token$abc"} {
		t.Run("compatibility-key="+compatibilityKey, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("FAST_CONTEXT_KEY", "")
			t.Setenv("WINDSURF_API_KEY", compatibilityKey)
			credentialPath := filepath.Join(home, ".local", "share", "devin", "credentials.toml")
			if err := os.MkdirAll(filepath.Dir(credentialPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(credentialPath, []byte(`api_key = "devin-session-token$also-truncated"`), 0o600); err != nil {
				t.Fatal(err)
			}

			client := &callCountingClient{}
			_, err := Run(context.Background(), testOptions(t.TempDir()), client)
			if err == nil {
				t.Fatal("Run() succeeded with a truncated automatic TOML credential")
			}
			if client.fetchJWTCalls != 0 || client.rateLimitCalls != 0 || client.streamCalls != 0 || client.parseCalls != 0 {
				t.Fatalf("client called before credential failure: %#v", client)
			}
			if strings.Contains(err.Error(), "devin-session-token$also-truncated") {
				t.Fatalf("Run() error leaked truncated automatic credential: %v", err)
			}
		})
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

func TestRunRetriesMalformedToolArguments(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "auth/login.go", "package auth\nfunc Login() {}\n")

	_, _, malformedErr := ParseToolCall(`thinking[TOOL_CALLS]restricted_exec[ARGS]{"command1",{"type":"rg"}}`)
	client := &scriptedClient{responses: []scriptedResponse{
		{parseErr: malformedErr},
		{toolCall: answerCall(`<ANSWER><file path="/codebase/auth/login.go"><range>1-2</range></file></ANSWER>`)},
	}}
	result, err := Run(context.Background(), testOptions(root), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "auth/login.go" {
		t.Fatalf("unexpected files: %#v", result.Files)
	}
	if client.calls != 2 {
		t.Fatalf("calls = %d, want 2", client.calls)
	}
	if len(client.messages) != 2 {
		t.Fatalf("request count = %d, want 2", len(client.messages))
	}
	last := client.messages[1][len(client.messages[1])-1]
	if last.Role != 1 || last.Content != malformedToolCallRetryPrompt {
		t.Fatalf("retry prompt = %#v", last)
	}
}

func TestRunDoesNotRetryOtherResponseErrors(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "auth/login.go", "package auth\nfunc Login() {}\n")

	client := &scriptedClient{responses: []scriptedResponse{{parseErr: errors.New("invalid connect frame")}}}
	_, err := Run(context.Background(), testOptions(root), client)
	if err == nil {
		t.Fatal("expected response error")
	}
	if client.calls != 1 {
		t.Fatalf("calls = %d, want 1", client.calls)
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
