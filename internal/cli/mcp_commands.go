package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/deqiying/fast-context/internal/mcpserver"
)

// mcpServeInput 是 mcp serve 读取 JSON-RPC 消息的输入端。Execute 与 handler 签名不含 stdin，
// 因此这里直接使用 os.Stdin；包级变量只为测试注入管道。
var mcpServeInput io.ReadCloser = os.Stdin

// runParsedMCPServe 运行单会话 stdio MCP server：stdin EOF 或 SIGINT/SIGTERM 正常结束返回 0，
// 传输层或 server 运行失败返回 1。stdout 只承载协议消息，诊断信息只写 stderr。
func runParsedMCPServe(ctx context.Context, stdout, stderr io.Writer) int {
	// 信号处理仅作用于长驻的 mcp serve，不改变其它命令的 Ctrl+C 语义。
	// Windows 上 SIGTERM 可注册但不会投递，此时依靠宿主关闭管道产生的 stdin EOF 退出。
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := mcpserver.Run(ctx, mcpserver.DefaultConfig(stderr), mcpServeInput, nopWriteCloser{stdout})
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return 0
	default:
		fmt.Fprintf(stderr, "Error: mcp server stopped: %s\n", err)
		return 1
	}
}

// nopWriteCloser 防止 SDK 关闭会话时连带关闭进程 stdout，与 SDK StdioTransport 的 nopCloserWriter 一致。
type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }
