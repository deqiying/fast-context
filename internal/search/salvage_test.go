package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSalvagesPlainTextResponseEvidence(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "auth/login.go", "package auth\nfunc Login() {}\n")
	writeProjectFile(t, root, "auth/session.go", "package auth\nfunc Session() {}\nfunc End() {}\n")
	writeProjectFile(t, root, "auth/private/key.go", "package private\n")

	text := "Relevant files: /codebase/auth/login.go, /codebase/missing.go, /codebase/../outside.go and " +
		"/codebase/auth/private/key.go.\n" +
		`<ANSWER><file path="/codebase/auth/session.go"><range>2-3</range></file></ANSWER>`
	client := &scriptedClient{responses: []scriptedResponse{{thinking: text}}}
	opts := testOptions(root)
	opts.ExcludePaths = []string{"auth/private"}

	result, err := RunPipeline(context.Background(), opts, client)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Meta.Salvaged {
		t.Fatalf("expected salvaged_response meta: %#v", result.Meta)
	}
	if len(result.RetryNotes) != 0 || client.calls != 1 {
		t.Fatalf("salvaged result must not trigger narrow-root retry: calls=%d notes=%v", client.calls, result.RetryNotes)
	}
	got := map[string][]LineRange{}
	for _, file := range result.Files {
		got[file.Path] = file.Ranges
	}
	if len(got) != 2 {
		t.Fatalf("unexpected salvaged files: %#v", result.Files)
	}
	if ranges := got["auth/session.go"]; len(ranges) != 1 || ranges[0] != (LineRange{Start: 2, End: 3}) {
		t.Fatalf("answer XML ranges were not preserved: %#v", result.Files)
	}
	if ranges, ok := got["auth/login.go"]; !ok || ranges == nil {
		t.Fatalf("loose path missing or has null ranges: %#v", result.Files)
	}
	if result.Raw != text {
		t.Fatal("raw_response must remain available for diagnostics")
	}
}

func TestRunSalvagesReadfileEvidenceWhenTurnsRunOut(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "auth/login.go", "package auth\nfunc Login() {}\nfunc Logout() {}\n")

	client := &scriptedClient{responses: []scriptedResponse{
		{toolCall: &ToolCall{Name: "restricted_exec", Args: map[string]any{
			"command1": map[string]any{"type": "readfile", "file": "/codebase/auth/login.go", "start_line": 1, "end_line": 3},
			"command2": map[string]any{"type": "readfile", "file": "/codebase/auth/ghost.go"},
		}}},
	}}
	result, err := Run(context.Background(), testOptions(root), client)
	if err != nil {
		t.Fatalf("expected salvaged success, got %v", err)
	}
	if !result.Meta.Salvaged || len(result.Files) != 1 || result.Files[0].Path != "auth/login.go" {
		t.Fatalf("unexpected salvage result: meta=%#v files=%#v", result.Meta, result.Files)
	}
	if ranges := result.Files[0].Ranges; len(ranges) != 1 || ranges[0] != (LineRange{Start: 1, End: 3}) {
		t.Fatalf("readfile ranges were not merged once: %#v", ranges)
	}
}

func TestRunStillFailsWhenTurnsRunOutWithoutEvidence(t *testing.T) {
	t.Setenv("WINDSURF_API_KEY", "test-key")
	root := t.TempDir()
	writeProjectFile(t, root, "auth/login.go", "package auth\n")

	client := &scriptedClient{responses: []scriptedResponse{
		{toolCall: &ToolCall{Name: "restricted_exec", Args: map[string]any{
			"command1": map[string]any{"type": "tree", "path": "/codebase", "levels": 1},
		}}},
	}}
	result, err := Run(context.Background(), testOptions(root), client)
	if err == nil || !strings.Contains(err.Error(), "max turns") {
		t.Fatalf("expected max turns error, got %v", err)
	}
	if result.Meta.Salvaged {
		t.Fatal("empty evidence must not be marked salvaged")
	}
}

func TestRejectBroadProjectRoot(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rejectBroadProjectRootWithHome(home, pathVariants(home)); err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("home directory was not rejected: %v", err)
	}
	if err := rejectBroadProjectRootWithHome(repo, pathVariants(home)); err != nil {
		t.Fatalf("repository under home was rejected: %v", err)
	}
	fsRoot := filepath.VolumeName(home) + string(filepath.Separator)
	if err := rejectBroadProjectRootWithHome(fsRoot, nil); err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("filesystem root %q was not rejected: %v", fsRoot, err)
	}
}

func TestRunRejectsHomeProjectBeforeClientCalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("WINDSURF_API_KEY", "test-key")

	client := &callCountingClient{}
	_, err := Run(context.Background(), testOptions(home), client)
	if err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("expected home directory rejection, got %v", err)
	}
	if client.fetchJWTCalls != 0 || client.rateLimitCalls != 0 || client.streamCalls != 0 || client.parseCalls != 0 {
		t.Fatalf("client called before project rejection: %#v", client)
	}
}
