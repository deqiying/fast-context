package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileAndPathEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "internal", "service.go")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	got := exec.ReadFile("/codebase/internal/service.go", 2, 3)
	if !strings.Contains(got, "2:two") || !strings.Contains(got, "3:three") {
		t.Fatalf("unexpected readfile output:\n%s", got)
	}
	escaped := exec.ReadFile("/codebase/../secret.txt", 1, 1)
	if !strings.Contains(escaped, "path escapes project root") {
		t.Fatalf("escape was not rejected: %s", escaped)
	}
}

func TestGlob(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	got := exec.ExecCommand(context.Background(), Command{Type: "glob", Pattern: "**/*.go", Path: "/codebase", TypeFilter: "file"})
	if !strings.Contains(got, "/codebase/pkg/a.go") {
		t.Fatalf("unexpected glob output: %s", got)
	}
}
