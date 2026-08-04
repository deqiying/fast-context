package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultTreeDepth   = 3
	DefaultMaxTurns    = 3
	DefaultMaxCommands = 8
	DefaultMaxResults  = 10
	DefaultTimeout     = 30 * time.Second

	MinTreeDepth      = 0
	MaxTreeDepth      = 6
	MinSearchTurns    = 1
	MaxSearchTurns    = 5
	MinSearchCommands = 1
	MaxSearchCommands = 20
	MinSearchResults  = 1
	MaxSearchResults  = 30
	MinTimeout        = time.Second
	MaxTimeout        = 300 * time.Second

	DefaultRepoMapMode         = "bootstrap_hotspot"
	DefaultBootstrapTreeDepth  = 1
	DefaultHotspotTopK         = 4
	DefaultHotspotTreeDepth    = 2
	DefaultHotspotMaxBytes     = 120 * 1024
	DefaultBootstrapMaxTurns   = 2
	DefaultBootstrapMaxCommand = 6

	EnvMaxTurns             = "FC_MAX_TURNS"
	EnvMaxCommands          = "FC_MAX_COMMANDS"
	EnvTimeoutMS            = "FC_TIMEOUT_MS"
	EnvRepoMapMode          = "FC_REPO_MAP_MODE"
	EnvBootstrapEnabled     = "FC_BOOTSTRAP_ENABLED"
	EnvBootstrapTreeDepth   = "FC_BOOTSTRAP_TREE_DEPTH"
	EnvBootstrapMaxTurns    = "FC_BOOTSTRAP_MAX_TURNS"
	EnvBootstrapMaxCommands = "FC_BOOTSTRAP_MAX_COMMANDS"
	EnvHotspotTopK          = "FC_HOTSPOT_TOP_K"
	EnvHotspotTreeDepth     = "FC_HOTSPOT_TREE_DEPTH"
	EnvHotspotMaxBytes      = "FC_HOTSPOT_MAX_BYTES"
	EnvIncludeSnippets      = "FC_INCLUDE_SNIPPETS"
	EnvDebug                = "FAST_CONTEXT_DEBUG"
)

// DefaultExcludePaths mirrors upstream 1.5.2 DEFAULT_EXCLUDE_PATHS: directories
// and patterns that are almost never source code. Always applied on top of the
// user-provided exclude list before the original-root policy is shared across
// repo maps, local tools, retries, and final result filtering.
var DefaultExcludePaths = []string{
	// dependency dirs
	"node_modules",
	"vendor",
	".venv",
	"venv",
	// version control
	".git",
	".svn",
	".hg",
	// build output
	"dist",
	"build",
	"out",
	"target",
	".next",
	".nuxt",
	".output",
	// caches
	"__pycache__",
	".cache",
	".pytest_cache",
	// minified artifacts
	"*.min.*",
	// common irrelevant dirs
	"coverage",
	".idea",
	".vscode",
}

// LargeRepoHintExcludes is used in no-result hint output.
var LargeRepoHintExcludes = []string{
	"node_modules", "dist", "build", "vendor", "coverage", "ent", "generated",
}

// MergeExcludePaths prepends the default excludes to the user list, dropping
// duplicates and empty entries.
func MergeExcludePaths(excludePaths []string) []string {
	merged := append([]string(nil), DefaultExcludePaths...)
	seen := map[string]bool{}
	for _, p := range merged {
		seen[p] = true
	}
	for _, p := range excludePaths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		merged = append(merged, p)
	}
	return merged
}

// Runtime carries env-configurable defaults (flag values still win).
type Runtime struct {
	MaxTurns             int
	MaxCommands          int
	Timeout              time.Duration
	RepoMapMode          string // classic | bootstrap_hotspot
	BootstrapTreeDepth   int
	HotspotTopK          int
	HotspotTreeDepth     int
	HotspotMaxBytes      int
	BootstrapEnabled     bool
	BootstrapMaxTurns    int
	BootstrapMaxCommands int
	IncludeSnippets      bool
	Debug                bool
}

// ReadRuntime mirrors upstream readRuntimeConfig: FC_* env vars with clamped
// defaults.
func ReadRuntime() Runtime {
	mode := DefaultRepoMapMode
	if os.Getenv(EnvRepoMapMode) == "classic" {
		mode = "classic"
	}
	return Runtime{
		MaxTurns:             ReadIntEnv(EnvMaxTurns, DefaultMaxTurns, MinSearchTurns, MaxSearchTurns),
		MaxCommands:          ReadIntEnv(EnvMaxCommands, DefaultMaxCommands, MinSearchCommands, MaxSearchCommands),
		Timeout:              time.Duration(ReadIntEnv(EnvTimeoutMS, int(DefaultTimeout/time.Millisecond), int(MinTimeout/time.Millisecond), int(MaxTimeout/time.Millisecond))) * time.Millisecond,
		RepoMapMode:          mode,
		BootstrapTreeDepth:   ReadIntEnv(EnvBootstrapTreeDepth, DefaultBootstrapTreeDepth, 1, 3),
		HotspotTopK:          ReadIntEnv(EnvHotspotTopK, DefaultHotspotTopK, 0, 8),
		HotspotTreeDepth:     ReadIntEnv(EnvHotspotTreeDepth, DefaultHotspotTreeDepth, 1, 4),
		HotspotMaxBytes:      ReadIntEnv(EnvHotspotMaxBytes, DefaultHotspotMaxBytes, 16*1024, 256*1024),
		BootstrapEnabled:     ReadBoolEnv(EnvBootstrapEnabled, true),
		BootstrapMaxTurns:    ReadIntEnv(EnvBootstrapMaxTurns, DefaultBootstrapMaxTurns, 1, 3),
		BootstrapMaxCommands: ReadIntEnv(EnvBootstrapMaxCommands, DefaultBootstrapMaxCommand, 1, 8),
		IncludeSnippets:      ReadBoolEnv(EnvIncludeSnippets, false),
		Debug:                os.Getenv(EnvDebug) == "1" || os.Getenv(EnvDebug) == "true",
	}
}

func ReadIntEnv(name string, def, minValue, maxValue int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return ClampInt(n, minValue, maxValue)
}

func ReadBoolEnv(name string, def bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch raw {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func ClampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
