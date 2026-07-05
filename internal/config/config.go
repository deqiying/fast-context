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

	DefaultRepoMapMode         = "bootstrap_hotspot"
	DefaultBootstrapTreeDepth  = 1
	DefaultHotspotTopK         = 4
	DefaultHotspotTreeDepth    = 2
	DefaultHotspotMaxBytes     = 120 * 1024
	DefaultBootstrapMaxTurns   = 2
	DefaultBootstrapMaxCommand = 6
)

// DefaultExcludePaths mirrors upstream 1.5.2 DEFAULT_EXCLUDE_PATHS: directories
// and patterns that are almost never source code. Always applied on top of the
// user-provided exclude list for repo map, bootstrap, and local grep expansion.
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
	if os.Getenv("FC_REPO_MAP_MODE") == "classic" {
		mode = "classic"
	}
	return Runtime{
		MaxTurns:             ReadIntEnv("FC_MAX_TURNS", DefaultMaxTurns, 1, 5),
		MaxCommands:          ReadIntEnv("FC_MAX_COMMANDS", DefaultMaxCommands, 1, 20),
		Timeout:              time.Duration(ReadIntEnv("FC_TIMEOUT_MS", int(DefaultTimeout/time.Millisecond), 1000, 300000)) * time.Millisecond,
		RepoMapMode:          mode,
		BootstrapTreeDepth:   ReadIntEnv("FC_BOOTSTRAP_TREE_DEPTH", DefaultBootstrapTreeDepth, 1, 3),
		HotspotTopK:          ReadIntEnv("FC_HOTSPOT_TOP_K", DefaultHotspotTopK, 0, 8),
		HotspotTreeDepth:     ReadIntEnv("FC_HOTSPOT_TREE_DEPTH", DefaultHotspotTreeDepth, 1, 4),
		HotspotMaxBytes:      ReadIntEnv("FC_HOTSPOT_MAX_BYTES", DefaultHotspotMaxBytes, 16*1024, 256*1024),
		BootstrapEnabled:     ReadBoolEnv("FC_BOOTSTRAP_ENABLED", true),
		BootstrapMaxTurns:    ReadIntEnv("FC_BOOTSTRAP_MAX_TURNS", DefaultBootstrapMaxTurns, 1, 3),
		BootstrapMaxCommands: ReadIntEnv("FC_BOOTSTRAP_MAX_COMMANDS", DefaultBootstrapMaxCommand, 1, 8),
		IncludeSnippets:      ReadBoolEnv("FC_INCLUDE_SNIPPETS", false),
		Debug:                os.Getenv("FAST_CONTEXT_DEBUG") == "1" || os.Getenv("FAST_CONTEXT_DEBUG") == "true",
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
