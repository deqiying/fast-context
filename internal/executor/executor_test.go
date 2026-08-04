package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/deqiying/fast-context/internal/pathfilter"
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

func TestReadFileTruncationKeepsValidUTF8(t *testing.T) {
	root := t.TempDir()
	// ReadFile prefixes the line with "1:", so place the rune's first byte at
	// the 250-byte truncation boundary.
	line := strings.Repeat("a", 247) + "中"
	if err := os.WriteFile(filepath.Join(root, "utf8.txt"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FC_LINE_MAX_CHARS", "250")

	exec, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	got := exec.ReadFile("/codebase/utf8.txt", 1, 1)
	if !utf8.ValidString(got) {
		t.Fatalf("readfile output is not valid UTF-8: %q", got)
	}
	if len(got) > 250 {
		t.Fatalf("readfile output exceeds byte budget: %d", len(got))
	}
}

func TestExecToolCallReportsReadFileUTF8Repair(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "invalid.bin"), []byte{'x', 0xff, '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	exec, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	result := exec.ExecToolCall(context.Background(), []NamedCommand{{
		Name: "command1",
		Command: Command{
			Type: "readfile",
			File: "/codebase/invalid.bin",
		},
	}})
	if !result.UTF8Repaired || !utf8.ValidString(result.Text) {
		t.Fatalf("repair stats do not reflect normalized readfile content: %#v", result)
	}
}

func TestExecutorPolicyAppliesToAllRestrictedTools(t *testing.T) {
	if _, err := FindRipgrep(); err != nil {
		t.Skip("rg not available")
	}
	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/keep.go", "package src\nvar FixtureNeedle = true\n")
	write("src/private/secret.go", "package private\nvar FixtureNeedle = \"secret-content\"\n")
	write("node_modules/pkg/index.js", "const FixtureNeedle = 'dependency'\n")

	policy, err := pathfilter.New(root, []string{"src/private", "node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	exec, err := NewWithPolicy(root, policy)
	if err != nil {
		t.Fatal(err)
	}

	if got := exec.ReadFile("/codebase/src/private/secret.go", 1, 10); !strings.Contains(got, "path is excluded") || strings.Contains(got, "secret-content") {
		t.Fatalf("readfile exclude failed: %s", got)
	}
	if got := exec.Tree("/codebase", 4); strings.Contains(got, "private") || strings.Contains(got, "node_modules") || !strings.Contains(got, "keep.go") {
		t.Fatalf("tree exclude failed:\n%s", got)
	}
	if got := exec.LS("/codebase/src", false, true); strings.Contains(got, "private") || !strings.Contains(got, "keep.go") {
		t.Fatalf("ls exclude failed:\n%s", got)
	}
	if got := exec.Glob("**/*", "/codebase", "file"); strings.Contains(got, "secret.go") || strings.Contains(got, "index.js") || !strings.Contains(got, "keep.go") {
		t.Fatalf("glob exclude failed:\n%s", got)
	}
	if got := exec.RG(context.Background(), "FixtureNeedle", "/codebase", []string{"**/*"}, nil); strings.Contains(got, "secret.go") || strings.Contains(got, "index.js") || !strings.Contains(got, "keep.go") {
		t.Fatalf("rg exclude failed:\n%s", got)
	}
}

func TestExecutorScopedExcludeKeepsNestedSameName(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/generated/direct.go", "src/feature/generated/keep.go"} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package fixture\nvar ScopedNeedle = true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := pathfilter.New(root, []string{"src/generated"})
	if err != nil {
		t.Fatal(err)
	}
	exec, err := NewWithPolicy(root, policy)
	if err != nil {
		t.Fatal(err)
	}
	if exec.rgPath == "" {
		t.Skip("rg not available")
	}
	got := exec.RG(context.Background(), "ScopedNeedle", "/codebase/src", nil, nil)
	if strings.Contains(got, "/codebase/src/generated/direct.go") || !strings.Contains(got, "/codebase/src/feature/generated/keep.go") {
		t.Fatalf("scoped exclude matched a nested same-name directory:\n%s", got)
	}
}

func TestExecutorSupportsSymlinkProjectRoot(t *testing.T) {
	physicalRoot := t.TempDir()
	file := filepath.Join(physicalRoot, "src", "keep.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package src\nvar SymlinkRootNeedle = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(physicalRoot, aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy, err := pathfilter.New(aliasRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := NewWithPolicy(aliasRoot, policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := exec.Tree("/codebase", 3); !strings.Contains(got, "keep.go") || strings.Contains(got, "path escapes project root") {
		t.Fatalf("tree rejected symlink project root:\n%s", got)
	}
	if got := exec.ReadFile("/codebase/src/keep.go", 1, 3); !strings.Contains(got, "SymlinkRootNeedle") || strings.Contains(got, "path escapes project root") {
		t.Fatalf("readfile rejected symlink project root:\n%s", got)
	}
	if exec.rgPath != "" {
		if got := exec.RG(context.Background(), "SymlinkRootNeedle", "/codebase", nil, nil); !strings.Contains(got, "keep.go") || strings.Contains(got, "path escapes project root") {
			t.Fatalf("rg rejected symlink project root:\n%s", got)
		}
	}
}

func TestAggregateResultsKeepsAllWrappersWithinBudget(t *testing.T) {
	items := make([]commandResultItem, 20)
	for i := range items {
		items[i] = commandResultItem{
			key: fmt.Sprintf("command%d", i+1),
			out: strings.Repeat("中", 5000),
		}
	}
	result := aggregateResults(items, normalResultBudget)
	if !result.Truncated || result.BytesBefore <= result.BytesAfter {
		t.Fatalf("unexpected aggregate stats: %#v", result)
	}
	if len(result.Text) > normalResultBudget || !utf8.ValidString(result.Text) {
		t.Fatalf("invalid aggregate output: bytes=%d valid=%t", len(result.Text), utf8.ValidString(result.Text))
	}
	for i := 1; i <= 20; i++ {
		for _, tag := range []string{fmt.Sprintf("<command%d_result>", i), fmt.Sprintf("</command%d_result>", i)} {
			if !strings.Contains(result.Text, tag) {
				t.Fatalf("missing wrapper %s", tag)
			}
		}
	}
}

func TestExecutorPolicyPreservesSymlinkAliasForDescendants(t *testing.T) {
	if _, err := FindRipgrep(); err != nil {
		t.Skip("rg not available")
	}
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "private"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "keep.go"), []byte("package real\nvar AliasNeedle = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "private", "secret.go"), []byte("package private\nvar AliasNeedle = \"secret\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "src")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy, err := pathfilter.New(root, []string{"src/private"})
	if err != nil {
		t.Fatal(err)
	}
	exec, err := NewWithPolicy(root, policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := exec.Tree("/codebase/src", 3); strings.Contains(got, "private") || !strings.Contains(got, "keep.go") {
		t.Fatalf("alias tree bypassed policy:\n%s", got)
	}
	if got := exec.ReadFile("/codebase/src/private/secret.go", 1, 5); !strings.Contains(got, "path is excluded") {
		t.Fatalf("alias readfile bypassed policy: %s", got)
	}
	if got := exec.RG(context.Background(), "AliasNeedle", "/codebase/src", []string{"**/*"}, nil); strings.Contains(got, "secret.go") || !strings.Contains(got, "keep.go") {
		t.Fatalf("alias rg bypassed policy:\n%s", got)
	}
}
