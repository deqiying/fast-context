package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/output"
	"github.com/deqiying/fast-context/internal/search"
)

const (
	searchToolName = "fast_context_search"

	projectPathRequiredMessage = "Error: project_path is required. Pass the absolute path to the project root directory."
)

// searchInput 的字段名与上游 fast-context-mcp@1.5.2 的工具参数保持一致。
// SDK 先按 searchInputSchema 校验并填充默认值，再解码到该结构。
type searchInput struct {
	Query               string   `json:"query"`
	ProjectPath         string   `json:"project_path"`
	TreeDepth           int      `json:"tree_depth"`
	MaxTurns            int      `json:"max_turns"`
	MaxResults          int      `json:"max_results"`
	ExcludePaths        []string `json:"exclude_paths"`
	IncludeCodeSnippets bool     `json:"include_code_snippets"`
}

type searchTool struct {
	runtime config.Runtime
	search  SearchFunc
	stderr  io.Writer
	calls   atomic.Int64
}

func registerSearchTool(server *mcp.Server, cfg Config, stderr io.Writer) {
	tool := &searchTool{runtime: cfg.Runtime, search: cfg.Search, stderr: stderr}
	mcp.AddTool(server, &mcp.Tool{
		Name:        searchToolName,
		Description: searchToolDescription(cfg),
		InputSchema: searchInputSchema(cfg),
		Annotations: &mcp.ToolAnnotations{
			Title:         "Fast Context Search",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPointer(true),
		},
	}, tool.handle)
}

func (t *searchTool) handle(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
	// SDK 已完成 schema 校验与默认值填充；这里只处理 schema 无法表达的路径约束。
	projectPath, message := validateProjectPath(in.ProjectPath)
	if message != "" {
		return errorResult(message), nil, nil
	}
	opts := search.DefaultOptions(t.runtime)
	opts.Query = in.Query
	opts.ProjectRoot = projectPath
	opts.TreeDepth = in.TreeDepth
	opts.MaxTurns = in.MaxTurns
	opts.MaxResults = in.MaxResults
	opts.ExcludePaths = append([]string(nil), in.ExcludePaths...)
	opts.IncludeSnippets = in.IncludeCodeSnippets
	opts.Format = "text"
	if t.runtime.Debug {
		opts.Progress = t.progressLogger(t.calls.Add(1))
	}
	if t.search == nil {
		return nil, nil, errors.New("search backend is not configured")
	}
	result, err := t.search(ctx, opts)
	if err != nil {
		// 业务失败以 isError 结果返回，模型可据此调整参数；不升级为 JSON-RPC 协议错误。
		return errorResult(output.FormatErrorFor(err, opts, result, output.SurfaceMCP)), nil, nil
	}
	// 无结果不算执行失败，与上游一致。
	return textResult(output.FormatTextFor(result, opts, output.SurfaceMCP)), nil, nil
}

// progressLogger 把搜索进度写到 stderr，并以调用序号区分并发调用；只在 FAST_CONTEXT_DEBUG 开启时使用。
func (t *searchTool) progressLogger(call int64) func(string) {
	prefix := fmt.Sprintf("[%s#%d] ", searchToolName, call)
	return func(message string) {
		_, _ = io.WriteString(t.stderr, prefix+message+"\n")
	}
}

func searchToolDescription(cfg Config) string {
	snippets := "- include_code_snippets: Default false (lightweight mode, ~2-5KB output). Set to true to include full code snippets in the response (~45KB output).\n"
	if cfg.IncludeSnippetsExplicit {
		snippets = fmt.Sprintf("- include_code_snippets: Server-configured default is %t. Do NOT override unless explicitly asked by the user.\n", cfg.Runtime.IncludeSnippets)
	}
	return "AI-driven semantic code search using Windsurf's Devstral model. " +
		"Searches a local codebase with natural language and returns relevant file paths with line ranges, " +
		"plus suggested grep keywords for follow-up searches.\n" +
		"Sends the query, a repository map, and restricted local search results to Windsurf; do not use it when external transmission is not authorized.\n" +
		"For best semantic search quality, write the query primarily in English; add local-language business terms only when needed.\n" +
		"Use tree_depth/max_turns/max_results for task-level tuning; use exclude_paths to reduce payload or noise.\n" +
		snippets +
		"Response includes a [config] line showing actual parameters used — use this to decide adjustments on retry. " +
		"Results are candidates: verify them locally before relying on them."
}

// searchInputSchema 构造工具输入 schema。范围常量与 CLI search 共用 internal/config；
// max_turns 与 include_code_snippets 的默认值在 server 启动时取自 FC_* 运行时配置。
func searchInputSchema(cfg Config) map[string]any {
	snippetsDescription := "Include full code snippets in the response. Default false (lightweight mode: file paths + line ranges + grep keywords, ~2-5KB output). Set to true for full code context (~45KB output)."
	if cfg.IncludeSnippetsExplicit {
		snippetsDescription = fmt.Sprintf("Include full code snippets in the response. Server default: %t (configured via FC_INCLUDE_SNIPPETS). Do NOT override this value unless the user explicitly asks for a different mode.", cfg.Runtime.IncludeSnippets)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"query", "project_path"},
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"minLength":   1,
				"pattern":     `\S`,
				"description": `Natural language search query. English is recommended for best semantic matching; add local-language business terms when useful (e.g. "where is user login authentication and JWT validation handled", "database connection pool").`,
			},
			"project_path": projectPathProperty(),
			"tree_depth": map[string]any{
				"type":    "integer",
				"minimum": config.MinTreeDepth,
				"maximum": config.MaxTreeDepth,
				"default": config.DefaultTreeDepth,
				"description": fmt.Sprintf("Directory tree depth for the repository map sent to the remote AI. Default %d. "+
					"0 selects size-based auto depth in classic repo-map mode. Use 1-2 for huge monorepos or after payload size errors; "+
					"use 4-6 for small projects where deeper structure helps.", config.DefaultTreeDepth),
			},
			"max_turns": map[string]any{
				"type":    "integer",
				"minimum": config.MinSearchTurns,
				"maximum": config.MaxSearchTurns,
				"default": cfg.Runtime.MaxTurns,
				"description": fmt.Sprintf("Number of search rounds. Each round: remote AI generates search commands, local execution, results sent back. "+
					"Default %d. Use 1 for quick lookups and 4-5 for complex cross-file tracing; more rounds are slower and use more API quota.", cfg.Runtime.MaxTurns),
			},
			"max_results": map[string]any{
				"type":    "integer",
				"minimum": config.MinSearchResults,
				"maximum": config.MaxSearchResults,
				"default": config.DefaultMaxResults,
				"description": fmt.Sprintf("Maximum number of files to return. Default %d. Use 3-5 for focused queries and 15-30 for broad exploration.",
					config.DefaultMaxResults),
			},
			"exclude_paths": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"default":     []string{},
				"description": "Directory/file patterns to exclude from tree and search context, applied on top of built-in defaults. Examples: ['node_modules', 'dist', 'build', 'coverage', '*.min.*'].",
			},
			"include_code_snippets": map[string]any{
				"type":        "boolean",
				"default":     cfg.Runtime.IncludeSnippets,
				"description": snippetsDescription,
			},
		},
	}
}

func projectPathProperty() map[string]any {
	return map[string]any{
		"type":        "string",
		"minLength":   1,
		"description": "Absolute path to the project root directory (required). Example: /Users/username/projects/myproject or C:/Users/username/projects/myproject.",
	}
}

// validateAbsolutePath 校验 project_path 非空且为绝对路径，返回 trim 后的路径。
// MCP server 的工作目录由宿主决定，相对路径不可靠，因此两个工具都要求绝对路径。
func validateAbsolutePath(raw string) (string, string) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", projectPathRequiredMessage
	}
	if !filepath.IsAbs(path) {
		return "", "Error: project_path must be an absolute path, got: " + path
	}
	return path, ""
}

// validateProjectPath 在绝对路径校验之外要求路径存在且为目录，措辞对齐上游 project-path.mjs。
// 宽根目录（用户主目录、文件系统根）的拒绝仍由 search.Run 统一负责。
func validateProjectPath(raw string) (string, string) {
	path, message := validateAbsolutePath(raw)
	if message != "" {
		return "", message
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && !info.IsDir():
		return "", "Error: project_path is not a directory: " + path
	case err == nil:
		return path, ""
	case errors.Is(err, fs.ErrNotExist):
		return "", "Error: project_path does not exist: " + path
	case errors.Is(err, fs.ErrPermission):
		return "", "Error: cannot access project_path (permission denied): " + path
	default:
		return "", "Error: failed to validate project_path: " + err.Error()
	}
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func errorResult(text string) *mcp.CallToolResult {
	result := textResult(text)
	result.IsError = true
	return result
}

func boolPointer(value bool) *bool {
	return &value
}
