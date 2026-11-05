package search

import (
	"testing"
	"time"

	"github.com/deqiying/fast-context/internal/config"
)

func TestDefaultOptionsMapsRuntimeDefaults(t *testing.T) {
	runtime := config.Runtime{
		MaxTurns:             2,
		MaxCommands:          5,
		Timeout:              7 * time.Second,
		RepoMapMode:          "classic",
		BootstrapTreeDepth:   2,
		HotspotTopK:          3,
		HotspotTreeDepth:     4,
		HotspotMaxBytes:      65536,
		BootstrapEnabled:     false,
		BootstrapMaxTurns:    1,
		BootstrapMaxCommands: 4,
		IncludeSnippets:      true,
		Debug:                true,
	}
	opts := DefaultOptions(runtime)
	if opts.Query != "" || opts.ProjectRoot != "" || len(opts.ExcludePaths) != 0 || opts.Progress != nil {
		t.Fatalf("caller-owned fields must stay empty: %#v", opts)
	}
	if opts.TreeDepth != config.DefaultTreeDepth || opts.MaxResults != config.DefaultMaxResults || opts.Format != "text" {
		t.Fatalf("fixed defaults drifted: %#v", opts)
	}
	if opts.MaxTurns != 2 || opts.MaxCommands != 5 || opts.Timeout != 7*time.Second {
		t.Fatalf("budget defaults not taken from runtime: %#v", opts)
	}
	if opts.RepoMapMode != "classic" || opts.BootstrapEnabled || opts.BootstrapTreeDepth != 2 || opts.BootstrapMaxTurns != 1 || opts.BootstrapMaxCommands != 4 {
		t.Fatalf("bootstrap defaults not taken from runtime: %#v", opts)
	}
	if opts.HotspotTopK != 3 || opts.HotspotTreeDepth != 4 || opts.HotspotMaxBytes != 65536 {
		t.Fatalf("hotspot defaults not taken from runtime: %#v", opts)
	}
	if !opts.IncludeSnippets || !opts.Verbose {
		t.Fatalf("snippet/debug defaults not taken from runtime: %#v", opts)
	}
}
