package search

import (
	"context"
	"time"
)

type Options struct {
	Query        string
	ProjectRoot  string
	TreeDepth    int
	MaxTurns     int
	MaxCommands  int
	MaxResults   int
	Timeout      time.Duration
	ExcludePaths []string
	Format       string
	Verbose      bool
	Progress     func(string)
}

type Result struct {
	Files      []ResultFile `json:"files"`
	RGPatterns []string     `json:"rg_patterns,omitempty"`
	Meta       Meta         `json:"meta"`
	Raw        string       `json:"raw_response,omitempty"`
	Error      string       `json:"error,omitempty"`
}

type ResultFile struct {
	Path     string      `json:"path"`
	FullPath string      `json:"full_path,omitempty"`
	Ranges   []LineRange `json:"ranges"`
}

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Meta struct {
	TreeDepth      int     `json:"tree_depth"`
	TreeSizeKB     float64 `json:"tree_size_kb"`
	MaxTurns       int     `json:"max_turns"`
	MaxResults     int     `json:"max_results"`
	MaxCommands    int     `json:"max_commands"`
	TimeoutMS      int64   `json:"timeout_ms"`
	FellBack       bool    `json:"fell_back,omitempty"`
	ProjectRoot    string  `json:"project_root,omitempty"`
	ErrorCode      string  `json:"error_code,omitempty"`
	ContextTrimmed bool    `json:"context_trimmed,omitempty"`
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
	FetchJWT(ctx context.Context, apiKey string) (string, error)
	CheckRateLimit(ctx context.Context, apiKey, jwt string) (bool, error)
	Stream(ctx context.Context, apiKey, jwt string, messages []Message, toolDefs string, timeout time.Duration) ([]byte, error)
	ParseResponse(data []byte) (string, *ToolCall, error)
}

type ToolCall struct {
	Name string
	Args map[string]any
}
