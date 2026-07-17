package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRipgrepReportsConfiguredSource(t *testing.T) {
	rgPath := filepath.Join(t.TempDir(), "rg-fixture")
	if err := os.WriteFile(rgPath, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FC_RG_PATH", rgPath)

	path, source, err := FindRipgrepWithSource()
	if err != nil {
		t.Fatal(err)
	}
	if path != rgPath || source != "fc_rg_path" {
		t.Fatalf("path=%q source=%q", path, source)
	}
}

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

func TestRGDashPatternAndExcludeExpansion(t *testing.T) {
	if _, err := FindRipgrep(); err != nil {
		t.Skip("rg not available")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("flag --verbose here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "bundle.js"), []byte("flag --verbose here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	got := exec.RG(context.Background(), "--verbose", "/codebase", nil, nil)
	if !strings.Contains(got, "main.go") {
		t.Fatalf("dash-leading pattern was not searched literally:\n%s", got)
	}
	got = exec.RG(context.Background(), "--verbose", "/codebase", nil, []string{"dist"})
	if strings.Contains(got, "bundle.js") {
		t.Fatalf("bare exclude glob did not exclude nested dir:\n%s", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Fatalf("exclude removed unrelated file:\n%s", got)
	}
}

func TestExpandExcludeGlobs(t *testing.T) {
	got := expandExcludeGlobs("dist")
	if len(got) != 2 || got[0] != "dist" || got[1] != "**/dist" {
		t.Fatalf("unexpected expansion: %#v", got)
	}
	got = expandExcludeGlobs("**/node_modules")
	if len(got) != 1 || got[0] != "**/node_modules" {
		t.Fatalf("unexpected expansion: %#v", got)
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
