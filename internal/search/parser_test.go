package search

import (
	"path/filepath"
	"testing"
)

func TestParseToolCall(t *testing.T) {
	text := `thinking[TOOL_CALLS]restricted_exec[ARGS]{"command1":{"type":"rg","pattern":"Auth","path":"/codebase"}} trailing`
	call, thinking, err := ParseToolCall(text)
	if err != nil {
		t.Fatal(err)
	}
	if thinking != "thinking" {
		t.Fatalf("thinking mismatch: %q", thinking)
	}
	if call == nil || call.Name != "restricted_exec" {
		t.Fatalf("unexpected call: %#v", call)
	}
	cmd := call.Args["command1"].(map[string]any)
	if cmd["type"] != "rg" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParseAnswerRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	xml := `<ANSWER>
<file path="/codebase/internal/service.go"><range>10-20</range></file>
<file path="/codebase/../secret.txt"><range>1-2</range></file>
</ANSWER>`
	result := ParseAnswer(xml, root)
	if len(result.Files) != 1 {
		t.Fatalf("got %d files", len(result.Files))
	}
	if result.Files[0].Path != "internal/service.go" {
		t.Fatalf("bad path: %s", result.Files[0].Path)
	}
	wantFull := filepath.Join(root, "internal", "service.go")
	if result.Files[0].FullPath != wantFull {
		t.Fatalf("bad full path: %s", result.Files[0].FullPath)
	}
}
