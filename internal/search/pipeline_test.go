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
	"unicode/utf8"

	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/pathfilter"
	"github.com/deqiying/fast-context/internal/repomap"
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

type testCodedError struct {
	code string
	err  error
}

func (e testCodedError) Error() string { return e.err.Error() }
func (e testCodedError) Unwrap() error { return e.err }
func (e testCodedError) Code() string  { return e.code }

type callCountingClient struct {
	fetchJWTCalls  int
	rateLimitCalls int
	sizeCalls      int
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

func (c *callCountingClient) RequestSize(string, string, []Message, string) (int, error) {
	c.sizeCalls++
	return 0, errors.New("unexpected RequestSize call")
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

func (c *scriptedClient) RequestSize(apiKey, jwt string, messages []Message, toolDefs string) (int, error) {
	return estimateRequestSize(messages, toolDefs), nil
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

func TestRunRecoversInvalidArgumentOnceAfterToolResult(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	for i := 0; i < 120; i++ {
		writeProjectFile(t, root, fmt.Sprintf("src/file-%03d.go", i), "package src\n")
	}

	invalid := testCodedError{code: "invalid_argument", err: errors.New("invalid_argument: fixture trace")}
	client := &scriptedClient{responses: []scriptedResponse{
		{thinking: strings.Repeat("a", maxAssistantThinkingBytes-1) + "中", toolCall: &ToolCall{Name: "restricted_exec", Args: map[string]any{
			"command1": map[string]any{"type": "tree", "path": "/codebase/src", "levels": 2},
		}}},
		{err: invalid},
		{toolCall: answerCall(`<ANSWER><file path="/codebase/src/file-000.go"><range>1-1</range></file></ANSWER>`)},
	}}

	result, err := Run(context.Background(), testOptions(root), client)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 3 {
		t.Fatalf("stream calls = %d, want 3", client.calls)
	}
	if !result.Meta.ContextTrimmed || result.Meta.ErrorCode != "" {
		t.Fatalf("unexpected recovery meta: %#v", result.Meta)
	}
	metaJSON, err := json.Marshal(result.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metaJSON), `"context_trimmed":true`) || strings.Contains(string(metaJSON), `"error_code"`) {
		t.Fatalf("recovery JSON contains stale error metadata: %s", metaJSON)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "src/file-000.go" {
		t.Fatalf("unexpected files: %#v", result.Files)
	}
	if len(client.messages) != 3 || !containsToolResult(client.messages[2]) {
		t.Fatalf("recovery request lost the existing tool result: %#v", client.messages)
	}
	var assistant Message
	for _, message := range client.messages[1] {
		if message.Role == 2 {
			assistant = message
			break
		}
	}
	if len(assistant.Content) > maxAssistantThinkingBytes || !utf8.ValidString(assistant.Content) {
		t.Fatalf("assistant thinking exceeded UTF-8 budget: bytes=%d valid=%t", len(assistant.Content), utf8.ValidString(assistant.Content))
	}
}

func TestRunDoesNotRecoverFirstTurnInvalidArgument(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	invalid := testCodedError{code: "invalid_argument", err: errors.New("invalid_argument: fixture trace")}
	client := &scriptedClient{responses: []scriptedResponse{{err: invalid}}}

	result, err := Run(context.Background(), testOptions(t.TempDir()), client)
	if err == nil {
		t.Fatal("expected invalid_argument")
	}
	if client.calls != 1 {
		t.Fatalf("stream calls = %d, want 1", client.calls)
	}
	if result.Meta.ErrorCode != "invalid_argument" || result.Meta.ContextTrimmed {
		t.Fatalf("unexpected failure meta: %#v", result.Meta)
	}
}

func TestRunReturnsSecondInvalidArgumentWithoutAnotherRecovery(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	for i := 0; i < 80; i++ {
		writeProjectFile(t, root, fmt.Sprintf("src/file-%03d.go", i), "package src\n")
	}
	invalid := testCodedError{code: "invalid_argument", err: errors.New("invalid_argument: fixture trace")}
	client := &scriptedClient{responses: []scriptedResponse{
		{toolCall: &ToolCall{Name: "restricted_exec", Args: map[string]any{
			"command1": map[string]any{"type": "tree", "path": "/codebase/src", "levels": 2},
		}}},
		{err: invalid},
		{err: invalid},
	}}
	result, err := Run(context.Background(), testOptions(root), client)
	if err == nil {
		t.Fatal("expected second invalid_argument")
	}
	if client.calls != 3 || result.Meta.ErrorCode != "invalid_argument" || !result.Meta.ContextTrimmed {
		t.Fatalf("unexpected bounded recovery result: calls=%d meta=%#v", client.calls, result.Meta)
	}
}

func TestInvalidArgumentRecoveryRequiresStrictlySmallerRequest(t *testing.T) {
	client := &scriptedClient{}
	messages := []Message{
		{Role: 5, Content: "system"},
		{Role: 1, Content: "short user message"},
		{Role: 2, Content: "thinking", ToolCallID: "call-1", ToolName: "restricted_exec", ToolArgsJSON: `{"command1":{"type":"tree","path":"/codebase"}}`},
		{Role: 4, Content: "<command1_result>\nshort\n</command1_result>", RefCallID: "call-1"},
	}
	_, _, _, ok, err := buildInvalidArgumentRecovery(client, "key", "jwt", messages, "{}", &trimState{})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("unchanged request should not be retried")
	}
}

func TestDecodeCommandsEnforcesLimitOrderAndStrictFields(t *testing.T) {
	batch := decodeCommands(map[string]any{
		"command3":  map[string]any{"type": "tree", "path": "/codebase/src"},
		"command1":  map[string]any{"type": "rg", "pattern": "Login", "path": "/codebase"},
		"command2":  map[string]any{"type": "readfile", "file": "/codebase/a.go", "unknown": true},
		"command01": map[string]any{"type": "ls", "path": "/codebase"},
		"command9":  map[string]any{"type": "ls", "path": "/codebase"},
	}, 3)
	if len(batch.Commands) != 2 || batch.Commands[0].Name != "command1" || batch.Commands[1].Name != "command3" {
		t.Fatalf("unexpected accepted commands: %#v", batch.Commands)
	}
	if batch.Rejected != 3 {
		t.Fatalf("rejected = %d, want 3", batch.Rejected)
	}
	if strings.Contains(batch.ArgsJSON, "command2") || strings.Contains(batch.ArgsJSON, "command9") || strings.Contains(batch.ArgsJSON, "command01") {
		t.Fatalf("rejected command leaked into ToolArgsJSON: %s", batch.ArgsJSON)
	}
}

func TestDecodeCommandsRequiresValidCommandOne(t *testing.T) {
	batch := decodeCommands(map[string]any{
		"command2": map[string]any{"type": "tree", "path": "/codebase/src"},
	}, 4)
	if len(batch.Commands) != 0 || batch.ArgsJSON != "{}" || batch.Rejected != 1 {
		t.Fatalf("unexpected batch: %#v", batch)
	}

	batch = decodeCommands(map[string]any{
		"command1": map[string]any{"type": "rg", "pattern": strings.Repeat("x", maxCommandStringBytes+1), "path": "/codebase"},
		"command2": map[string]any{"type": "tree", "path": "/codebase/src"},
	}, 4)
	if len(batch.Commands) != 0 || batch.Rejected != 2 {
		t.Fatalf("invalid command1 should reject the batch: %#v", batch)
	}
}

func TestDecodeCommandsCapsSerializedArgsAndLists(t *testing.T) {
	args := map[string]any{}
	for i := 1; i <= 20; i++ {
		args[fmt.Sprintf("command%d", i)] = map[string]any{
			"type": "tree",
			"path": "/codebase/" + strings.Repeat("x", 1900) + fmt.Sprint(i),
		}
	}
	batch := decodeCommands(args, 20)
	if len(batch.ArgsJSON) > maxToolArgsJSONBytes || batch.Rejected == 0 || len(batch.Commands) >= 20 {
		t.Fatalf("serialized args budget was not enforced: accepted=%d rejected=%d bytes=%d", len(batch.Commands), batch.Rejected, len(batch.ArgsJSON))
	}

	tooMany := make([]string, maxCommandListItems+1)
	batch = decodeCommands(map[string]any{
		"command1": map[string]any{
			"type":    "rg",
			"pattern": "fixture",
			"path":    "/codebase",
			"include": tooMany,
		},
	}, 1)
	if len(batch.Commands) != 0 || batch.Rejected != 1 {
		t.Fatalf("oversized command list was accepted: %#v", batch)
	}

	batch = decodeCommands(map[string]any{
		"command1": map[string]any{
			"type":    "rg",
			"pattern": "fixture",
			"path":    "/codebase",
			"include": []string{strings.Repeat("x", maxCommandListItemBytes+1)},
		},
	}, 1)
	if len(batch.Commands) != 0 || batch.Rejected != 1 {
		t.Fatalf("oversized command list item was accepted: %#v", batch)
	}
}

func TestRestrictedExecSchemaRejectsAdditionalPropertiesAtEveryLevel(t *testing.T) {
	var tools []struct {
		Function struct {
			Name       string         `json:"name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(buildToolDefinitions(3)), &tools); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Function.Name != "restricted_exec" {
			continue
		}
		if value, ok := tool.Function.Parameters["additionalProperties"].(bool); !ok || value {
			t.Fatalf("restricted_exec parameters are not strict: %#v", tool.Function.Parameters)
		}
		properties, ok := tool.Function.Parameters["properties"].(map[string]any)
		if !ok || len(properties) != 3 {
			t.Fatalf("unexpected command properties: %#v", tool.Function.Parameters["properties"])
		}
		for name, raw := range properties {
			command, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s schema is not an object: %#v", name, raw)
			}
			branches, ok := command["oneOf"].([]any)
			if !ok || len(branches) != 5 {
				t.Fatalf("%s schema has invalid oneOf: %#v", name, command["oneOf"])
			}
			for index, rawBranch := range branches {
				branch, ok := rawBranch.(map[string]any)
				if !ok {
					t.Fatalf("%s branch %d is not an object: %#v", name, index, rawBranch)
				}
				if value, ok := branch["additionalProperties"].(bool); !ok || value {
					t.Fatalf("%s branch %d is not strict: %#v", name, index, branch)
				}
			}
		}
		return
	}
	t.Fatal("restricted_exec tool definition not found")
}

func TestRunEnforcesMaxCommandsAcrossExecutionAndHistory(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "src/fixture.go", "package src\nvar FirstPattern = true\nvar SecondPattern = true\n")
	client := &scriptedClient{responses: []scriptedResponse{
		{toolCall: &ToolCall{Name: "restricted_exec", Args: map[string]any{
			"command1": map[string]any{"type": "rg", "pattern": "FirstPattern", "path": "/codebase"},
			"command2": map[string]any{"type": "rg", "pattern": "SecondPattern", "path": "/codebase"},
		}}},
		{toolCall: answerCall(`<ANSWER><file path="/codebase/src/fixture.go"><range>1-3</range></file></ANSWER>`)},
	}}
	opts := testOptions(root)
	opts.MaxCommands = 1
	result, err := Run(context.Background(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RGPatterns) != 1 || result.RGPatterns[0] != "FirstPattern" {
		t.Fatalf("rejected command affected execution state: %v", result.RGPatterns)
	}
	if len(client.messages) < 2 {
		t.Fatalf("missing second request: %d", len(client.messages))
	}
	for _, message := range client.messages[1] {
		if message.Role == 2 && strings.Contains(message.ToolArgsJSON, "command2") {
			t.Fatalf("rejected command entered history: %s", message.ToolArgsJSON)
		}
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

func TestReadCodeSnippetsRepairsAndTruncatesUTF8Safely(t *testing.T) {
	file := filepath.Join(t.TempDir(), "invalid.txt")
	data := append([]byte("header\n"), 0xff)
	data = append(data, []byte(strings.Repeat("中", 200))...)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	const budget = 240
	snippets, used := readCodeSnippets(file, []LineRange{{Start: 1, End: 2}}, budget)
	if len(snippets) != 1 {
		t.Fatalf("unexpected snippets: %#v", snippets)
	}
	if used > budget || !utf8.ValidString(snippets[0]) {
		t.Fatalf("snippet violated UTF-8 byte budget: used=%d valid=%t", used, utf8.ValidString(snippets[0]))
	}
	if !strings.Contains(snippets[0], "... [truncated]") || !strings.HasSuffix(snippets[0], "```") {
		t.Fatalf("snippet truncation marker or closing fence missing: %q", snippets[0])
	}
}

func TestRunPipelineFiltersExcludedAnswerAndSnippets(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "src/keep.go", "package src\nconst Keep = true\n")
	writeProjectFile(t, root, "src/private/secret.go", "package private\nconst Secret = \"must-not-leak\"\n")

	client := &scriptedClient{responses: []scriptedResponse{{toolCall: answerCall(
		`<ANSWER>` +
			`<file path="/codebase/src/private/secret.go"><range>1-2</range></file>` +
			`<file path="/codebase/src/keep.go"><range>1-2</range></file>` +
			`</ANSWER>`,
	)}}}
	opts := testOptions(root)
	opts.ExcludePaths = []string{"src/private"}
	opts.IncludeSnippets = true
	result, err := RunPipeline(context.Background(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "src/keep.go" {
		t.Fatalf("excluded answer path survived: %#v", result.Files)
	}
	if strings.Contains(strings.Join(result.Files[0].Snippets, "\n"), "must-not-leak") {
		t.Fatalf("excluded snippet content leaked: %#v", result.Files[0].Snippets)
	}
}

func TestAutoGrepAndRetrySelectorHonorOriginalRootPolicy(t *testing.T) {
	rgPath, err := executor.FindRipgrep()
	if err != nil {
		t.Skip("rg not available")
	}
	root := t.TempDir()
	writeProjectFile(t, root, "src/keep.go", "package src\nvar PublicNeedle = true\n")
	writeProjectFile(t, root, "src/private/secret.go", "package private\nvar PrivateNeedle = true\n")
	excluded := "src/private"
	if runtime.GOOS == "windows" {
		excluded = "SRC/PRIVATE"
	}
	policy, err := pathfilter.New(root, []string{excluded})
	if err != nil {
		t.Fatal(err)
	}
	activeRoot := filepath.Join(root, "src")
	found := autoGrepFiles(rgPath, []string{"PublicNeedle"}, activeRoot, policy, nil, 10, 10)
	if len(found) != 1 || found[0].Path != "keep.go" {
		t.Fatalf("auto grep returned excluded paths: %#v", found)
	}
	if score := probeRetryDirScore(rgPath, activeRoot, retryQueryTokens("PrivateNeedle"), policy); score != 0 {
		t.Fatalf("retry probe matched content under an excluded narrow-root path: score=%d", score)
	}
	if score := probeRetryDirScore(rgPath, activeRoot, retryQueryTokens("PublicNeedle"), policy); score == 0 {
		t.Fatal("retry probe failed to match included narrow-root content")
	}
	retryRoots := selectNoResultRetryProjectRoots(rgPath, root, []string{"src"}, 2, "PublicNeedle", policy, repomap.ListTopLevelDirs)
	if len(retryRoots) != 1 || filepath.Base(retryRoots[0]) != "src" {
		t.Fatalf("retry probe failed to match included narrow-root content: %v", retryRoots)
	}
}

func TestBootstrapExecutorHonorsPolicy(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "src/keep.go", "package src\n")
	writeProjectFile(t, root, "src/private/secret.go", "package private\nconst Secret = \"must-not-leak\"\n")
	client := &scriptedClient{responses: []scriptedResponse{
		{toolCall: &ToolCall{Name: "restricted_exec", Args: map[string]any{
			"command1": map[string]any{"type": "readfile", "file": "/codebase/src/private/secret.go"},
			"command2": map[string]any{"type": "rg", "pattern": "Secret", "path": "/codebase"},
		}}},
		{thinking: "bootstrap complete"},
		{toolCall: answerCall(`<ANSWER><file path="/codebase/src/keep.go"><range>1-1</range></file></ANSWER>`)},
	}}
	opts := testOptions(root)
	opts.RepoMapMode = "bootstrap_hotspot"
	opts.BootstrapEnabled = true
	opts.BootstrapMaxTurns = 2
	opts.BootstrapMaxCommands = 1
	opts.ExcludePaths = []string{"src/private"}
	opts.Query = string([]byte{'q', 0xff})
	result, err := Run(context.Background(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || len(client.messages) < 2 {
		t.Fatalf("unexpected bootstrap result: files=%#v requests=%d", result.Files, len(client.messages))
	}
	bootstrapRequest := client.messages[1]
	joined := ""
	for _, message := range bootstrapRequest {
		joined += message.Content
	}
	if strings.Contains(joined, "must-not-leak") || !strings.Contains(joined, "path is excluded") {
		t.Fatalf("bootstrap tool result violated policy: %s", joined)
	}
	for _, message := range bootstrapRequest {
		if message.Role == 2 && strings.Contains(message.ToolArgsJSON, "command2") {
			t.Fatalf("bootstrap command cap leaked into history: %s", message.ToolArgsJSON)
		}
	}
	for requestIndex, request := range client.messages {
		for messageIndex, message := range request {
			for field, value := range map[string]string{
				"content": message.Content, "tool_call_id": message.ToolCallID,
				"tool_name": message.ToolName, "tool_args_json": message.ToolArgsJSON,
				"ref_call_id": message.RefCallID,
			} {
				if !utf8.ValidString(value) {
					t.Fatalf("request %d message %d field %s is invalid UTF-8", requestIndex, messageIndex, field)
				}
			}
		}
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

func (c *retryAwareClient) RequestSize(apiKey, jwt string, messages []Message, toolDefs string) (int, error) {
	return estimateRequestSize(messages, toolDefs), nil
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
