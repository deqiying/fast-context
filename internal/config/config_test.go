package config

import (
	"testing"
	"time"
)

func TestMergeExcludePaths(t *testing.T) {
	merged := MergeExcludePaths([]string{"custom", "node_modules", ""})
	seen := map[string]int{}
	for _, p := range merged {
		seen[p]++
	}
	if seen["node_modules"] != 1 {
		t.Fatalf("node_modules should appear exactly once, got %d", seen["node_modules"])
	}
	if seen["custom"] != 1 {
		t.Fatalf("custom exclude missing: %v", merged)
	}
	if seen[""] != 0 {
		t.Fatal("empty exclude should be dropped")
	}
}

func TestReadRuntimeEnv(t *testing.T) {
	t.Setenv("FC_MAX_TURNS", "9") // above max 5 → clamped
	t.Setenv("FC_TIMEOUT_MS", "45000")
	t.Setenv("FC_REPO_MAP_MODE", "classic")
	t.Setenv("FC_INCLUDE_SNIPPETS", "true")
	rt := ReadRuntime()
	if rt.MaxTurns != 5 {
		t.Fatalf("MaxTurns = %d, want clamped 5", rt.MaxTurns)
	}
	if rt.Timeout != 45*time.Second {
		t.Fatalf("Timeout = %v, want 45s", rt.Timeout)
	}
	if rt.RepoMapMode != "classic" {
		t.Fatalf("RepoMapMode = %q", rt.RepoMapMode)
	}
	if !rt.IncludeSnippets {
		t.Fatal("IncludeSnippets should be true")
	}
}
