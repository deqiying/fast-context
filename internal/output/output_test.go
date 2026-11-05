package output

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deqiying/fast-context/internal/search"
)

type codedError struct {
	code string
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }
func (e codedError) Code() string  { return e.code }

func TestFormatTextIncludesCandidateEvidenceAndConfig(t *testing.T) {
	result := search.Result{
		Files: []search.ResultFile{{
			FullPath: "D:/repo/internal/auth/service.go",
			Ranges:   []search.LineRange{{Start: 12, End: 40}},
		}},
		RGPatterns: []string{"AuthService"},
		Meta: search.Meta{
			TreeDepth:   3,
			TreeSizeKB:  12.5,
			MaxTurns:    3,
			MaxResults:  10,
			MaxCommands: 8,
			TimeoutMS:   30000,
		},
	}
	text := FormatText(result, search.Options{Timeout: 30 * time.Second})
	for _, want := range []string{
		"Found 1 relevant files.",
		"D:/repo/internal/auth/service.go (L12-40)",
		"grep keywords: AuthService",
		"tree_depth=3",
		"timeout_ms=30000",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("FormatText missing %q:\n%s", want, text)
		}
	}
}

func TestFormatTextMarksSalvagedResults(t *testing.T) {
	result := search.Result{
		Files: []search.ResultFile{{FullPath: "D:/repo/auth.go", Ranges: []search.LineRange{}}},
		Meta:  search.Meta{Salvaged: true},
	}
	text := FormatText(result, search.Options{Timeout: 30 * time.Second})
	if !strings.Contains(text, "salvaged from its partial output") {
		t.Fatalf("salvaged result note missing:\n%s", text)
	}
	if strings.Contains(FormatText(search.Result{Files: result.Files}, search.Options{}), "salvaged") {
		t.Fatal("normal results must not carry the salvage note")
	}
}

func TestErrorFormatsPreserveStableCode(t *testing.T) {
	err := codedError{code: "AUTH_ERROR", err: errors.New("fixture authentication failure")}
	formatted := FormatError(err, search.Options{TreeDepth: 3, MaxTurns: 3, MaxResults: 10, MaxCommands: 8, Timeout: 30 * time.Second}, search.Result{})
	if !strings.Contains(formatted, "error_type=AUTH_ERROR") || !strings.Contains(formatted, "fast-context key extract --path <state.vscdb>") || !strings.Contains(formatted, "FAST_CONTEXT_KEY") || !strings.Contains(formatted, "config.json") || !strings.Contains(formatted, "WINDSURF_API_KEY") {
		t.Fatalf("unexpected formatted error:\n%s", formatted)
	}
	if strings.Contains(formatted, "fixture authentication failure") == false {
		t.Fatal("formatted error should preserve the non-sensitive error message")
	}
	if !strings.Contains(formatted, "devin login") || !strings.Contains(formatted, "HTTP 403") {
		t.Fatalf("AUTH_ERROR hint should cover the WSL Devin CLI login case:\n%s", formatted)
	}

	structured := ErrorJSON(err, search.Result{})
	errorObject, ok := structured["error"].(map[string]any)
	if !ok || errorObject["code"] != "AUTH_ERROR" || errorObject["message"] != "fixture authentication failure" {
		t.Fatalf("unexpected structured error: %#v", structured)
	}
}

func TestErrorFormatsPreserveRemoteInvalidArgument(t *testing.T) {
	err := codedError{code: "invalid_argument", err: errors.New("invalid_argument: internal error (trace ID: fixture)")}
	result := search.Result{Meta: search.Meta{ErrorCode: "invalid_argument", ContextTrimmed: true}}
	formatted := FormatError(err, search.Options{}, result)
	if !strings.Contains(formatted, "error_type=invalid_argument") || !strings.Contains(formatted, "server-side diagnostics") {
		t.Fatalf("unexpected formatted error:\n%s", formatted)
	}
	structured := ErrorJSON(err, result)
	errorObject := structured["error"].(map[string]any)
	if errorObject["code"] != "invalid_argument" {
		t.Fatalf("unexpected structured error: %#v", structured)
	}
}

func TestSurfaceCLIMatchesDefaultFormatters(t *testing.T) {
	opts := search.Options{TreeDepth: 3, MaxTurns: 3, MaxResults: 10, MaxCommands: 8, Timeout: 30 * time.Second}
	noResult := search.Result{Meta: search.Meta{TreeDepth: 1}}
	if FormatText(noResult, opts) != FormatTextFor(noResult, opts, SurfaceCLI) {
		t.Fatal("FormatText must equal FormatTextFor(SurfaceCLI)")
	}
	for _, code := range []string{"PAYLOAD_TOO_LARGE", "AUTH_ERROR", "OTHER"} {
		err := codedError{code: code, err: errors.New("fixture")}
		if FormatError(err, opts, search.Result{}) != FormatErrorFor(err, opts, search.Result{}, SurfaceCLI) {
			t.Fatalf("FormatError must equal FormatErrorFor(SurfaceCLI) for %s", code)
		}
	}
}

func TestSurfaceMCPUsesToolParameterNames(t *testing.T) {
	opts := search.Options{TreeDepth: 3, MaxTurns: 3, MaxResults: 10, MaxCommands: 8, Timeout: 30 * time.Second}
	cases := map[string]string{
		"no_result": FormatTextFor(search.Result{}, opts, SurfaceMCP),
	}
	for _, code := range []string{"PAYLOAD_TOO_LARGE", "TIMEOUT", "AUTH_ERROR", "OTHER"} {
		cases[code] = FormatErrorFor(codedError{code: code, err: errors.New("fixture")}, opts, search.Result{}, SurfaceMCP)
	}
	for name, text := range cases {
		for _, flag := range []string{"--project", "--exclude", "--tree-depth", "--max-turns"} {
			if strings.Contains(text, flag) {
				t.Fatalf("MCP %s text must not reference CLI flag %s:\n%s", name, flag, text)
			}
		}
	}
	for _, want := range []string{"narrow project_path", "add exclude_paths"} {
		if !strings.Contains(cases["no_result"], want) {
			t.Fatalf("MCP no-result hint missing %q:\n%s", want, cases["no_result"])
		}
	}
	if !strings.Contains(cases["PAYLOAD_TOO_LARGE"], "reduce tree_depth, reduce max_turns, add exclude_paths, or narrow project_path") {
		t.Fatalf("MCP payload hint uses unexpected wording:\n%s", cases["PAYLOAD_TOO_LARGE"])
	}
	for _, code := range []string{"AUTH_ERROR", "OTHER"} {
		if !strings.Contains(cases[code], "fast_context_doctor") {
			t.Fatalf("MCP %s hint must point to fast_context_doctor:\n%s", code, cases[code])
		}
	}
	if !strings.Contains(cases["AUTH_ERROR"], "devin login") || !strings.Contains(cases["AUTH_ERROR"], "error_type=AUTH_ERROR") {
		t.Fatalf("MCP AUTH_ERROR must keep the WSL hint and stable code:\n%s", cases["AUTH_ERROR"])
	}
}
