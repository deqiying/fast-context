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

func TestErrorFormatsPreserveStableCode(t *testing.T) {
	err := codedError{code: "AUTH_ERROR", err: errors.New("fixture authentication failure")}
	formatted := FormatError(err, search.Options{TreeDepth: 3, MaxTurns: 3, MaxResults: 10, MaxCommands: 8, Timeout: 30 * time.Second}, search.Result{})
	if !strings.Contains(formatted, "error_type=AUTH_ERROR") || !strings.Contains(formatted, "fast-context key extract") || !strings.Contains(formatted, "FAST_CONTEXT_KEY") || !strings.Contains(formatted, "config.json") {
		t.Fatalf("unexpected formatted error:\n%s", formatted)
	}
	if strings.Contains(formatted, "fixture authentication failure") == false {
		t.Fatal("formatted error should preserve the non-sensitive error message")
	}

	structured := ErrorJSON(err, search.Result{})
	errorObject, ok := structured["error"].(map[string]any)
	if !ok || errorObject["code"] != "AUTH_ERROR" || errorObject["message"] != "fixture authentication failure" {
		t.Fatalf("unexpected structured error: %#v", structured)
	}
}
