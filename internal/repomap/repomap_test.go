package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
