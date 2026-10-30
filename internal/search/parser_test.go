package search

import (
	"errors"
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

func TestParseToolCallMarksMalformedJSONRecoverable(t *testing.T) {
	text := `thinking[TOOL_CALLS]restricted_exec[ARGS]{"command1",{"type":"rg","pattern":"Auth","path":"/codebase"}}`
	call, thinking, err := ParseToolCall(text)
	if call != nil {
		t.Fatalf("call = %#v, want nil", call)
	}
	if thinking != "thinking" {
		t.Fatalf("thinking = %q, want thinking", thinking)
	}
	var malformed *MalformedToolCallError
	if !errors.As(err, &malformed) {
		t.Fatalf("error = %T %v, want MalformedToolCallError", err, err)
	}
}

func TestParseToolCallRepairsCommonJSONDefects(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{name: "unquoted key", args: `{"command1":{"type":"rg","pattern":"Auth","path":"/codebase",exclude:["**/dist/**"]}}`},
		{name: "missing opening quote", args: `{"command1":{"type":"rg","pattern":"Auth","path":"/codebase",exclude":["**/dist/**"]}}`},
		{name: "trailing commas", args: `{"command1":{"type":"rg","pattern":"Auth","path":"/codebase","exclude":["**/dist/**",],},}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call, thinking, err := ParseToolCall("thinking[TOOL_CALLS]restricted_exec[ARGS]" + tc.args + " trailing")
			if err != nil {
				t.Fatal(err)
			}
			if thinking != "thinking" || call == nil || !call.Repaired {
				t.Fatalf("thinking=%q call=%#v", thinking, call)
			}
			cmd := call.Args["command1"].(map[string]any)
			exclude, ok := cmd["exclude"].([]any)
			if !ok || len(exclude) != 1 || exclude[0] != "**/dist/**" || cmd["pattern"] != "Auth" {
				t.Fatalf("unexpected repaired command: %#v", cmd)
			}
		})
	}
}

func TestRepairToolArgsJSONLeavesStringContentUntouched(t *testing.T) {
	raw := `{"command1":{"type":"rg","pattern":"a, b: {c,}","path":"/codebase/中文",levels:2}}`
	fixed, changed := repairToolArgsJSON(raw)
	want := `{"command1":{"type":"rg","pattern":"a, b: {c,}","path":"/codebase/中文","levels":2}}`
	if !changed || fixed != want {
		t.Fatalf("fixed=%q changed=%t", fixed, changed)
	}
	if _, changed := repairToolArgsJSON(`{"a":[true, false],"b":"x"}`); changed {
		t.Fatal("valid JSON must not be rewritten")
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
