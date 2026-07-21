package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deqiying/fast-context/internal/skills"
)

func executeForTest(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestVersionAliasesMatch(t *testing.T) {
	var expected string
	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		code, stdout, stderr := executeForTest(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("Execute(%v) code=%d stderr=%q", args, code, stderr)
		}
		if expected == "" {
			expected = stdout
		} else if stdout != expected {
			t.Fatalf("Execute(%v) = %q, want %q", args, stdout, expected)
		}
	}
}

func TestSkillsListAndShow(t *testing.T) {
	code, stdout, stderr := executeForTest(t, "skills", "list", "--format", "json")
	if code != 0 || stderr != "" {
		t.Fatalf("skills list code=%d stderr=%q", code, stderr)
	}
	var list skillsListOutput
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatal(err)
	}
	if !list.OK || list.Total != 1 || len(list.Skills) != 1 || list.Skills[0].ID != "fast-context" {
		t.Fatalf("unexpected skills list: %#v", list)
	}

	code, stdout, stderr = executeForTest(t, "skills", "show", "code-context", "--format=content")
	if code != 0 || stderr != "" {
		t.Fatalf("skills show code=%d stderr=%q", code, stderr)
	}
	want, err := skills.ReadMarkdown("fast-context")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != want {
		t.Fatal("skills show content differs from the embedded SKILL.md")
	}

	code, stdout, stderr = executeForTest(t, "skills", "show", "fast-context", "--format", "json")
	if code != 0 || stderr != "" {
		t.Fatalf("skills show json code=%d stderr=%q", code, stderr)
	}
	var shown skillShowOutput
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatal(err)
	}
	if !shown.OK || shown.Skill.ID != "fast-context" || shown.Content != want {
		t.Fatalf("unexpected skills show JSON: %#v", shown)
	}
}

func TestSkillsUnknownReturnsUsageExit(t *testing.T) {
	code, stdout, stderr := executeForTest(t, "skills", "show", "missing")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "available skills: fast-context") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestDoctorJSONIncludesAutomationFields(t *testing.T) {
	project := t.TempDir()
	rgPath := filepath.Join(t.TempDir(), "rg-test")
	if err := os.WriteFile(rgPath, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FC_RG_PATH", rgPath)
	t.Setenv("FAST_CONTEXT_KEY", "fixture-api-key-123456")
	t.Setenv("WINDSURF_API_KEY", "")

	code, stdout, stderr := executeForTest(t, "doctor", "--project", project, "--format", "json")
	if code != 0 || stderr != "" {
		t.Fatalf("doctor code=%d stderr=%q", code, stderr)
	}
	var report struct {
		OK      bool `json:"ok"`
		Project struct {
			Exists bool `json:"exists"`
		} `json:"project"`
		Ripgrep struct {
			OK     bool   `json:"ok"`
			Source string `json:"source"`
		} `json:"ripgrep"`
		Credentials struct {
			OK         bool   `json:"ok"`
			Source     string `json:"source"`
			SourceType string `json:"source_type"`
			Key        string `json:"key"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || !report.Project.Exists || !report.Ripgrep.OK || report.Ripgrep.Source != "fc_rg_path" || !report.Credentials.OK || report.Credentials.Source != "FAST_CONTEXT_KEY" || report.Credentials.SourceType != "env" {
		t.Fatalf("unexpected doctor report: %#v", report)
	}
	if strings.Contains(report.Credentials.Key, "fixture-api-key-123456") {
		t.Fatal("doctor returned the unredacted API key")
	}
}
