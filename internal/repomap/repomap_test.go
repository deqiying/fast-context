package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAutoDepth(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Build(root, 0, nil)
	if !m.AutoDepth {
		t.Fatal("AutoDepth flag not set for tree_depth=0")
	}
	if m.Depth != 4 { // <500 entries → suggested depth 4
		t.Fatalf("auto depth = %d, want 4", m.Depth)
	}
}

func TestBuildOptimizedHotspot(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("auth/handlers/login.go", "package handlers\nfunc Login() {}\n")
	mk("auth/jwt.go", "package auth\nfunc VerifyJWT() {}\n")
	mk("billing/invoice.go", "package billing\n")

	m := BuildOptimized("user login authentication jwt", root, 3, nil, OptimizerOptions{
		Mode:               "bootstrap_hotspot",
		BootstrapTreeDepth: 1,
		HotspotTopK:        2,
		HotspotTreeDepth:   2,
		MaxBytes:           120 * 1024,
	}, nil, nil)
	if m.Strategy != "bootstrap_hotspot" {
		t.Fatalf("strategy = %q", m.Strategy)
	}
	if len(m.HotDirs) == 0 || m.HotDirs[0] != "auth" {
		t.Fatalf("expected auth as top hot dir, got %v", m.HotDirs)
	}
	if !strings.Contains(m.Tree, "# Hotspot Subtrees") {
		t.Fatalf("missing hotspot section:\n%s", m.Tree)
	}
	if !strings.Contains(m.Tree, "/codebase/auth") {
		t.Fatalf("missing auth subtree:\n%s", m.Tree)
	}

	classic := BuildOptimized("query", root, 2, nil, OptimizerOptions{Mode: "classic"}, nil, nil)
	if classic.Strategy != "classic" || strings.Contains(classic.Tree, "# Hotspot Subtrees") {
		t.Fatalf("classic mode leaked hotspot content: %q", classic.Strategy)
	}
}

func TestBuildExcludes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Build(root, 2, []string{"node_modules"})
	if strings.Contains(m.Tree, "node_modules") {
		t.Fatalf("exclude failed:\n%s", m.Tree)
	}
	if !strings.Contains(m.Tree, "main.go") {
		t.Fatalf("missing main.go:\n%s", m.Tree)
	}
}
