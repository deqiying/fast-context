// Package mcpserver 以 Model Context Protocol server 的身份通过 stdio 暴露 fast-context，
// 提供 fast_context_search 与 fast_context_doctor 两个工具。
//
// 本包是唯一依赖 MCP SDK 的包；搜索语义、凭据解析、格式化和本地检查全部复用
// internal/search、internal/output 与 internal/doctor，不形成第二套实现。
// stdout 只承载 SDK 写出的 JSON-RPC 消息，日志与进度只写 Config.Stderr。
package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/search"
	"github.com/deqiying/fast-context/internal/version"
	"github.com/deqiying/fast-context/internal/windsurf"
)

// ServerName 是 initialize 响应中的 serverInfo.name。
const ServerName = "fast-context"

const serverInstructions = "fast-context — AI-driven semantic code search over a local repository. " +
	"fast_context_search sends the query, a repository map, and restricted local search results to Windsurf; " +
	"do not use it when external transmission is not authorized. " +
	"Results are candidate files with line ranges, grep keywords, and a [config] line; verify candidates locally before relying on them. " +
	"On payload, timeout, or noisy results, retry with a lower tree_depth or max_turns, add exclude_paths, or narrow project_path. " +
	"Call fast_context_doctor with the same project_path when credentials or ripgrep may be missing, or after an authentication or environment error; " +
	"it is local-only and does not contact Windsurf."

// SearchFunc 是搜索执行的注入点：生产环境绑定 search.RunPipeline 与共享的
// windsurf.Client，测试替换为本地 fake，不访问网络、不读取凭据。
type SearchFunc func(ctx context.Context, opts search.Options) (search.Result, error)

// Config 描述一个 server 实例。Runtime 在启动时读取一次，修改 FC_* 环境变量需要重启 server。
type Config struct {
	Runtime config.Runtime
	// IncludeSnippetsExplicit 表示 FC_INCLUDE_SNIPPETS 被显式设置，仅影响工具描述措辞。
	IncludeSnippetsExplicit bool
	Search                  SearchFunc
	Version                 string
	// Stderr 接收日志与进度；绝不能是 stdout，否则会破坏 stdio 协议流。
	Stderr io.Writer
}

// DefaultConfig 从当前进程环境构造生产配置。
func DefaultConfig(stderr io.Writer) Config {
	// windsurf.Client 底层是并发安全的 http.Client，server 生命周期内共享一个实例。
	client := windsurf.NewClient(nil)
	// 空白值视为未设置，与 config.ReadBoolEnv 的空值语义一致，避免误提示"不要覆盖服务端默认值"。
	snippetsExplicit := strings.TrimSpace(os.Getenv(config.EnvIncludeSnippets)) != ""
	return Config{
		Runtime:                 config.ReadRuntime(),
		IncludeSnippetsExplicit: snippetsExplicit,
		Search: func(ctx context.Context, opts search.Options) (search.Result, error) {
			return search.RunPipeline(ctx, opts, client)
		},
		Version: version.Version,
		Stderr:  stderr,
	}
}

// NewServer 组装 SDK Server 并注册 fast_context_search 与 fast_context_doctor，
// 供 Run 与内存传输测试共用。
func NewServer(cfg Config) *mcp.Server {
	stderr := newSyncWriter(cfg.Stderr)
	level := slog.LevelWarn
	if cfg.Runtime.Debug {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	server := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: cfg.Version},
		&mcp.ServerOptions{Instructions: serverInstructions, Logger: logger},
	)
	registerSearchTool(server, cfg, stderr)
	registerDoctorTool(server)
	return server
}

// Run 在给定的读写端上运行单会话，直到输入 EOF（返回 nil）、ctx 取消（返回 ctx.Err()）
// 或传输错误。与 mcp.Server.Run 等价，但正常的信号取消不会写 ERROR 日志。
func Run(ctx context.Context, cfg Config, in io.ReadCloser, out io.WriteCloser) error {
	server := NewServer(cfg)
	session, err := server.Connect(ctx, &mcp.IOTransport{Reader: in, Writer: out}, nil)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		<-done
		return ctx.Err()
	case err := <-done:
		// ctx 取消也可能先表现为读端出错；此时以取消为准，避免信号退出被报告为运行失败。
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
}

// syncWriter 让 SDK 日志与并发工具调用的进度共用一把锁写 stderr，避免行交错。
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newSyncWriter(w io.Writer) *syncWriter {
	if w == nil {
		w = io.Discard
	}
	return &syncWriter{w: w}
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
