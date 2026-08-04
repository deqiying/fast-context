package pathfilter

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExcludedMatchesRelativeNestedBasenameAndGlob(t *testing.T) {
	root := t.TempDir()
	absoluteExcluded := filepath.Join(root, "src", "private")
	policy, err := New(root, []string{"node_modules", "src/generated", "*.tmp", "**/cache", "src/**/nested-generated/**", absoluteExcluded})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		want bool
	}{
		{path: "src/generated/output.go", want: true},
		{path: filepath.Join(root, "src", "generated", "output.go"), want: true},
		{path: "packages/node_modules/pkg/index.js", want: true},
		{path: "src/cache/data.json", want: true},
		{path: "src/feature/nested-generated", want: true},
		{path: "src/feature/nested-generated/output.go", want: true},
		{path: "tmp/build.tmp", want: true},
		{path: "src/private/secret.go", want: true},
		{path: "src/keep.go", want: false},
	}
	for _, tt := range tests {
		if got := policy.Excluded(tt.path); got != tt.want {
			t.Fatalf("Excluded(%q) = %t, want %t", tt.path, got, tt.want)
		}
	}
}

func TestRGGlobsRebaseToRetryRoot(t *testing.T) {
	root := t.TempDir()
	policy, err := New(root, []string{"node_modules", "src/generated", "*.tmp", "**/cache"})
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(root, "src")
	got := policy.RGGlobs(active)
	generated := filepath.ToSlash(filepath.Join(active, "generated"))
	cache := filepath.ToSlash(filepath.Join(active, "**", "cache"))
	for _, want := range []string{generated, generated + "/**", "*.tmp", "**/*.tmp", cache, cache + "/**"} {
		if !containsString(got, want) {
			t.Fatalf("RGGlobs(%q) missing %q: %v", active, want, got)
		}
	}
	for _, unwanted := range []string{"generated", "generated/**", "**/generated", "**/generated/**"} {
		if containsString(got, unwanted) {
			t.Fatalf("RGGlobs(%q) was not rebased: %q in %v", active, unwanted, got)
		}
	}
}

func TestRGGlobsRebaseCrossingGlobWithoutExcludingEverything(t *testing.T) {
	root := t.TempDir()
	policy, err := New(root, []string{"src/**/generated/**"})
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(root, "src", "feature")
	got := policy.RGGlobs(active)
	want := filepath.ToSlash(filepath.Join(active, "**", "generated", "**"))
	if !containsString(got, want) {
		t.Fatalf("crossing glob was not rebased to its unmatched suffix: %v", got)
	}
	if containsString(got, filepath.ToSlash(filepath.Join(active, "**"))) {
		t.Fatalf("crossing glob must not exclude the entire active root: %v", got)
	}
}

func TestWindowsMatchingUsesFilesystemCaseSemantics(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows filesystem semantics")
	}
	root := t.TempDir()
	policy, err := New(root, []string{"SRC/PRIVATE"})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.CaseInsensitive() {
		t.Fatal("Windows policy should report case-insensitive matching")
	}
	if !policy.Excluded(filepath.Join(root, "src", "private", "secret.go")) {
		t.Fatal("exclude matching should ignore path case on Windows")
	}
	got := policy.RGGlobs(filepath.Join(root, "src"))
	private := filepath.ToSlash(filepath.Join(root, "src", "private"))
	if !containsString(got, private) || !containsString(got, private+"/**") {
		t.Fatalf("ripgrep globs should be case-normalized and rebased: %v", got)
	}
}

func TestExcludedVirtualUsesActiveRootAndOriginalPolicy(t *testing.T) {
	root := t.TempDir()
	policy, err := New(root, []string{"src/private"})
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(root, "src")
	if !policy.ExcludedVirtual(active, "/codebase/private/secret.go") {
		t.Fatal("narrow-root virtual path should retain original-root excludes")
	}
	if policy.ExcludedVirtual(active, "/codebase/keep.go") {
		t.Fatal("included virtual path was excluded")
	}
	if !policy.ExcludedVirtual(active, "/codebase/../outside.go") {
		t.Fatal("virtual path escape should be rejected")
	}
}

func TestExcludedPathGlobDoesNotExcludeSiblingFiles(t *testing.T) {
	root := t.TempDir()
	policy, err := New(root, []string{"src/*.gen"})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excluded("src/generated.gen") {
		t.Fatal("matching path glob should be excluded")
	}
	if policy.Excluded("src/keep.go") {
		t.Fatal("path glob must not exclude unrelated sibling files")
	}
}

func TestExcludedChecksResolvedSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	alias := filepath.Join(root, "src", "alias")
	if err := os.MkdirAll(private, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(private, "secret.go")
	if err := os.WriteFile(secret, []byte("package private\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy, err := New(root, []string{"private"})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excluded(filepath.Join(alias, "secret.go")) {
		t.Fatal("resolved symlink target should remain excluded")
	}
}

func TestExcludedRejectsSymlinkOutsideOriginalRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.go")
	if err := os.WriteFile(outsideFile, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "outside.go")
	if err := os.Symlink(outsideFile, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excluded(alias) {
		t.Fatal("symlink outside original root should be excluded")
	}
}

func TestNewRejectsNonDirectoryRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(file, nil); err == nil {
		t.Fatal("New should reject a file root")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
