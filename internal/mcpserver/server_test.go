package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/search"
)

const testVersion = "9.9.9-test"

// fakeSearch 记录每次调用收到的 Options，并按 respond 返回结果；不访问网络、不读取凭据。
type fakeSearch struct {
	mu      sync.Mutex
	calls   []search.Options
	respond func(ctx context.Context, opts search.Options) (search.Result, error)
}

func (f *fakeSearch) run(ctx context.Context, opts search.Options) (search.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opts)
	respond := f.respond
	f.mu.Unlock()
	if respond == nil {
		return search.Result{}, nil
	}
	return respond(ctx, opts)
}

func (f *fakeSearch) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSearch) lastCall(t *testing.T) search.Options {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("search was not called")
	}
	return f.calls[len(f.calls)-1]
}

// testRuntime 使用与默认值不同的取值，便于断言 server 级字段确实来自 Runtime。
func testRuntime() config.Runtime {
	return config.Runtime{
		MaxTurns:             2,
		MaxCommands:          5,
		Timeout:              45 * time.Second,
		RepoMapMode:          "classic",
		BootstrapTreeDepth:   2,
		HotspotTopK:          4,
		HotspotTreeDepth:     3,
		HotspotMaxBytes:      64 * 1024,
		BootstrapEnabled:     false,
		BootstrapMaxTurns:    1,
		BootstrapMaxCommands: 3,
		IncludeSnippets:      false,
	}
}

func testConfig(fake *fakeSearch, stderr io.Writer) Config {
	return Config{Runtime: testRuntime(), Search: fake.run, Version: testVersion, Stderr: stderr}
}

// connectInMemory 用内存传输连接 SDK client 与 server，返回已完成 initialize 的 client 会话。
func connectInMemory(t *testing.T, cfg Config) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := NewServer(cfg).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	})
	return clientSession
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool(%s, %v): %v", name, arguments, err)
	}
	return result
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("expected exactly one content item, got %#v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}
	return text.Text
}

func TestInitializeAdvertisesServerIdentity(t *testing.T) {
	session := connectInMemory(t, testConfig(&fakeSearch{}, io.Discard))
	initialize := session.InitializeResult()
	if initialize == nil || initialize.ServerInfo == nil {
		t.Fatal("missing initialize result")
	}
	if initialize.ServerInfo.Name != ServerName || initialize.ServerInfo.Version != testVersion {
		t.Fatalf("unexpected server info: %#v", initialize.ServerInfo)
	}
	if initialize.Capabilities == nil || initialize.Capabilities.Tools == nil {
		t.Fatalf("tools capability is missing: %#v", initialize.Capabilities)
	}
	if !strings.Contains(initialize.Instructions, "fast_context_doctor") || !strings.Contains(initialize.Instructions, "Windsurf") {
		t.Fatalf("instructions must describe transmission and doctor: %q", initialize.Instructions)
	}
}

// stdioSession 通过 Run + 管道驱动一次真实的 stdio 会话，并逐行读取 stdout。
type stdioSession struct {
	in   *io.PipeWriter
	out  *bufio.Reader
	done chan error
}

func startStdioSession(t *testing.T, ctx context.Context, cfg Config) *stdioSession {
	t.Helper()
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	session := &stdioSession{in: inWriter, out: bufio.NewReader(outReader), done: make(chan error, 1)}
	go func() {
		err := Run(ctx, cfg, inReader, outWriter)
		_ = outWriter.Close()
		session.done <- err
	}()
	t.Cleanup(func() { _ = inWriter.Close() })
	return session
}

func (s *stdioSession) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

// receive 读取一行 stdout，并断言它是 JSON-RPC 2.0 消息。
func (s *stdioSession) receive(t *testing.T) map[string]any {
	t.Helper()
	line, err := s.out.ReadString('\n')
	if err != nil {
		t.Fatalf("read stdout: %v (partial %q)", err, line)
	}
	var message map[string]any
	if err := json.Unmarshal([]byte(line), &message); err != nil || message["jsonrpc"] != "2.0" {
		t.Fatalf("stdout line is not a JSON-RPC message: %q err=%v", line, err)
	}
	return message
}

func (s *stdioSession) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-s.done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

const initializeLine = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"stdio-test","version":"0"}}}`

func TestRunReturnsNilOnInputEOF(t *testing.T) {
	session := startStdioSession(t, context.Background(), testConfig(&fakeSearch{}, io.Discard))
	session.send(t, initializeLine)
	session.receive(t)
	if err := session.in.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.wait(t); err != nil {
		t.Fatalf("Run after EOF returned %v, want nil", err)
	}
}

func TestRunReturnsContextErrorOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr bytes.Buffer
	session := startStdioSession(t, ctx, testConfig(&fakeSearch{}, &stderr))
	session.send(t, initializeLine)
	session.receive(t)
	cancel()
	if err := session.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run after cancel returned %v, want context.Canceled", err)
	}
	if strings.Contains(stderr.String(), "level=ERROR") {
		t.Fatalf("signal-style cancellation must not log an error: %q", stderr.String())
	}
}

func TestStdoutCarriesOnlyProtocolMessages(t *testing.T) {
	project := t.TempDir()
	fake := &fakeSearch{respond: func(_ context.Context, opts search.Options) (search.Result, error) {
		opts.Progress("progress line that must stay on stderr")
		return search.Result{Files: []search.ResultFile{{Path: "main.go", Ranges: []search.LineRange{{Start: 1, End: 2}}}}}, nil
	}}
	var stderr bytes.Buffer
	cfg := testConfig(fake, &stderr)
	cfg.Runtime.Debug = true // 进度只在 FAST_CONTEXT_DEBUG 开启时输出
	session := startStdioSession(t, context.Background(), cfg)

	session.send(t, initializeLine)
	session.receive(t)
	session.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	call, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": searchToolName, "arguments": map[string]any{"query": "entry point", "project_path": project}},
	})
	if err != nil {
		t.Fatal(err)
	}
	session.send(t, string(call))
	response := session.receive(t)
	if response["id"] != float64(2) {
		t.Fatalf("unexpected response: %#v", response)
	}
	result, _ := response["result"].(map[string]any)
	if result == nil || result["isError"] == true {
		t.Fatalf("search call failed: %#v", response)
	}

	if err := session.in.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.wait(t); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	rest, err := io.ReadAll(session.out)
	if err != nil || len(rest) != 0 {
		t.Fatalf("unexpected trailing stdout %q err=%v", rest, err)
	}
	if !strings.Contains(stderr.String(), "[fast_context_search#1] progress line that must stay on stderr") {
		t.Fatalf("progress did not reach stderr: %q", stderr.String())
	}
}
