package search

import (
	"context"
	"time"

	"github.com/deqiying/fast-context/internal/pathfilter"
)

const MaxRequestProtoBytes = 320 * 1024

type Options struct {
	Query        string
	ProjectRoot  string
	TreeDepth    int // 0 = auto depth based on project size
	MaxTurns     int
	MaxCommands  int
	MaxResults   int
	Timeout      time.Duration
	ExcludePaths []string
	Format       string
	Verbose      bool
	Progress     func(string)

	// Repo-map optimizer (upstream 1.5.2 bootstrap_hotspot mode).
	RepoMapMode          string // classic | bootstrap_hotspot
	BootstrapEnabled     bool
	BootstrapTreeDepth   int
	BootstrapMaxTurns    int
	BootstrapMaxCommands int
	HotspotTopK          int
	HotspotTreeDepth     int
	HotspotMaxBytes      int

	IncludeSnippets bool

	pathPolicy *pathfilter.Policy
}

type Result struct {
	Files       []ResultFile `json:"files"`
	RGPatterns  []string     `json:"rg_patterns,omitempty"`
	Meta        Meta         `json:"meta"`
	Raw         string       `json:"raw_response,omitempty"`
	Error       string       `json:"error,omitempty"`
	RetryNotes  []string     `json:"retry_notes,omitempty"`
	GrepAdded   int          `json:"grep_expanded,omitempty"`
	ProjectRoot string       `json:"active_project_root,omitempty"`
}

type ResultFile struct {
	Path     string      `json:"path"`
	FullPath string      `json:"full_path,omitempty"`
	Ranges   []LineRange `json:"ranges"`
	FromGrep bool        `json:"from_grep,omitempty"`
	Snippets []string    `json:"snippets,omitempty"`
}

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Meta struct {
	TreeDepth      int      `json:"tree_depth"`
	HotspotDepth   int      `json:"hotspot_depth,omitempty"`
	TreeSizeKB     float64  `json:"tree_size_kb"`
	MaxTurns       int      `json:"max_turns"`
	MaxResults     int      `json:"max_results"`
	MaxCommands    int      `json:"max_commands"`
	TimeoutMS      int64    `json:"timeout_ms"`
	FellBack       bool     `json:"fell_back,omitempty"`
	AutoDepth      bool     `json:"auto_depth,omitempty"`
	Strategy       string   `json:"repo_map_strategy,omitempty"`
	HotDirs        []string `json:"hot_dirs,omitempty"`
	ProjectRoot    string   `json:"project_root,omitempty"`
	ErrorCode      string   `json:"error_code,omitempty"`
	ContextTrimmed bool     `json:"context_trimmed,omitempty"`
	// Salvaged 表示模型未通过 answer 工具正常收尾，结果来自其回复文本或已执行
	// 的 readfile 证据；调用方应把这些候选视为部分结果并在本地验证。
	Salvaged bool `json:"salvaged_response,omitempty"`
}

type Message struct {
	Role         int
	Content      string
	ToolCallID   string
	ToolName     string
	ToolArgsJSON string
	RefCallID    string
}

type Client interface {
	FetchJWT(ctx context.Context, apiKey string, timeout time.Duration) (string, error)
	CheckRateLimit(ctx context.Context, apiKey, jwt string, timeout time.Duration) (bool, error)
	RequestSize(apiKey, jwt string, messages []Message, toolDefs string) (int, error)
	Stream(ctx context.Context, apiKey, jwt string, messages []Message, toolDefs string, timeout time.Duration) ([]byte, error)
	ParseResponse(data []byte) (string, *ToolCall, error)
}

type ToolCall struct {
	Name string
	Args map[string]any
	// Repaired 表示参数 JSON 经过本地语法修复后才可解析，仅用于诊断输出。
	Repaired bool
}
