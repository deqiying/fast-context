package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// startMCPServeForTest 通过 Execute 启动 mcp serve，用管道替换 stdin 与 stdout，
// 返回输入写端、stdout 行读取器、stderr 缓冲与退出码通道。
func startMCPServeForTest(t *testing.T, ctx context.Context) (*io.PipeWriter, *bufio.Reader, *bytes.Buffer, <-chan int) {
	t.Helper()
	t.Setenv("FAST_CONTEXT_DEBUG", "")
	inReader, inWriter := io.Pipe()
	previous := mcpServeInput
	mcpServeInput = inReader
	t.Cleanup(func() {
		mcpServeInput = previous
		_ = inWriter.Close()
	})
	outReader, outWriter := io.Pipe()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		code := Execute(ctx, []string{"mcp", "serve"}, outWriter, &stderr)
		_ = outWriter.Close()
		done <- code
	}()
	return inWriter, bufio.NewReader(outReader), &stderr, done
}

func writeJSONRPCLine(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func readJSONRPCLine(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read stdout line: %v (partial %q)", err, line)
	}
	var message map[string]any
	if err := json.Unmarshal([]byte(line), &message); err != nil || message["jsonrpc"] != "2.0" {
		t.Fatalf("stdout line is not a JSON-RPC message: %q err=%v", line, err)
	}
	return message
}

func waitExitCode(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("mcp serve did not exit")
		return -1
	}
}

const testInitializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"cli-test","version":"0"}}}`

func TestMCPServeStdinEOFExitsZeroWithProtocolOnlyStdout(t *testing.T) {
	in, out, stderr, done := startMCPServeForTest(t, context.Background())

	writeJSONRPCLine(t, in, testInitializeRequest)
	initialize := readJSONRPCLine(t, out)
	result, _ := initialize["result"].(map[string]any)
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "fast-context" {
		t.Fatalf("unexpected initialize response: %#v", initialize)
	}

	writeJSONRPCLine(t, in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	writeJSONRPCLine(t, in, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	list := readJSONRPCLine(t, out)
	tools, _ := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools/list returned %d tools: %#v", len(tools), list)
	}

	// 响应读取完毕后再关闭输入，避免 SDK 在 EOF 时取消仍在处理中的请求。
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if code := waitExitCode(t, done); code != 0 {
		t.Fatalf("mcp serve exit code=%d stderr=%q", code, stderr.String())
	}
	rest, err := io.ReadAll(out)
	if err != nil || len(rest) != 0 {
		t.Fatalf("unexpected trailing stdout %q err=%v", rest, err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("mcp serve wrote unexpected stderr: %q", stderr.String())
	}
}

func TestMCPServeContextCancelExitsZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in, out, stderr, done := startMCPServeForTest(t, ctx)

	writeJSONRPCLine(t, in, testInitializeRequest)
	readJSONRPCLine(t, out)
	// 输入端保持打开，模拟宿主仍连接时收到 SIGINT/SIGTERM。
	cancel()
	if code := waitExitCode(t, done); code != 0 {
		t.Fatalf("mcp serve exit code after cancel=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "Error:") {
		t.Fatalf("cancelled session reported an error: %q", stderr.String())
	}
}
