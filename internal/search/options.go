package search

import "github.com/deqiying/fast-context/internal/config"

// DefaultOptions 把 FC_* 运行时默认值映射为搜索选项，供 CLI 与 MCP 两个调用面共用，
// 避免两处各自维护映射而漂移。Query 与 ProjectRoot 留空，由调用方填入；
// 调用方再覆盖各自调用面显式提供的参数。
func DefaultOptions(runtime config.Runtime) Options {
	return Options{
		TreeDepth:   config.DefaultTreeDepth,
		MaxTurns:    runtime.MaxTurns,
		MaxCommands: runtime.MaxCommands,
		MaxResults:  config.DefaultMaxResults,
		Timeout:     runtime.Timeout,
		Format:      "text",

		RepoMapMode:          runtime.RepoMapMode,
		BootstrapEnabled:     runtime.BootstrapEnabled,
		BootstrapTreeDepth:   runtime.BootstrapTreeDepth,
		BootstrapMaxTurns:    runtime.BootstrapMaxTurns,
		BootstrapMaxCommands: runtime.BootstrapMaxCommands,
		HotspotTopK:          runtime.HotspotTopK,
		HotspotTreeDepth:     runtime.HotspotTreeDepth,
		HotspotMaxBytes:      runtime.HotspotMaxBytes,
		IncludeSnippets:      runtime.IncludeSnippets,
		Verbose:              runtime.Debug,
	}
}
