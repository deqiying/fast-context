package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/deqiying/fast-context/internal/skills"
)

func writeCLIStateDBFixture(t *testing.T, apiKey string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.state.vscdb")
	return writeCLIStateDBFixtureAt(t, path, apiKey)
}

func writeCLIStateDBFixtureAt(t *testing.T, path, apiKey string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"apiKey": apiKey})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO ItemTable (key, value) VALUES (?, ?)", "windsurfAuthStatus", string(payload)); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func legacyStateDBPath(home string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Windsurf", "User", "globalStorage", "state.vscdb")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Windsurf", "User", "globalStorage", "state.vscdb")
	default:
		return filepath.Join(home, ".config", "Windsurf", "User", "globalStorage", "state.vscdb")
	}
}

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
			OK         bool     `json:"ok"`
			Source     string   `json:"source"`
			SourceType string   `json:"source_type"`
			Key        string   `json:"key"`
			Candidates []string `json:"candidates"`
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
	for _, candidate := range report.Credentials.Candidates {
		if strings.EqualFold(filepath.Base(candidate), "state.vscdb") {
			t.Fatalf("doctor returned an automatic state database candidate: %s", candidate)
		}
	}
}

func TestKeyExtractExplicitSQLite(t *testing.T) {
	const apiKey = "devin-session-token$eyJfixture"
	path := writeCLIStateDBFixture(t, apiKey)
	code, stdout, stderr := executeForTest(t, "key", "extract", "--path", path, "--format", "json")
	if code != 0 || stderr != "" {
		t.Fatalf("key extract code=%d stderr=%q", code, stderr)
	}
	var result struct {
		OK         bool   `json:"ok"`
		Source     string `json:"source"`
		SourceType string `json:"source_type"`
		Key        string `json:"key"`
		Length     int    `json:"length"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Source != path || result.SourceType != "windsurf_state_db" || result.Length != len(apiKey) {
		t.Fatalf("unexpected key extract result: %#v", result)
	}
	if result.Key == apiKey || strings.Contains(stdout, apiKey) {
		t.Fatal("key extract returned the unredacted API key")
	}
}

func TestKeyExtractMissingExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.state.vscdb")
	code, stdout, stderr := executeForTest(t, "key", "extract", "--path", path)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "credential source not found") {
		t.Fatalf("key extract code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCommandsIgnoreLegacyStateDatabase(t *testing.T) {
	const apiKey = "devin-session-token$eyJmust-not-be-discovered"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("FAST_CONTEXT_KEY", "")
	t.Setenv("WINDSURF_API_KEY", "")
	writeCLIStateDBFixtureAt(t, legacyStateDBPath(home), apiKey)

	code, stdout, stderr := executeForTest(t, "key", "extract", "--format", "json")
	if code != 1 || stderr != "" {
		t.Fatalf("key extract code=%d stderr=%q", code, stderr)
	}
	var extracted struct {
		OK         bool     `json:"ok"`
		Error      string   `json:"error"`
		TriedPaths []string `json:"tried_paths"`
	}
	if err := json.Unmarshal([]byte(stdout), &extracted); err != nil {
		t.Fatal(err)
	}
	if extracted.OK || !strings.Contains(extracted.Error, "credential source not found") || strings.Contains(stdout, apiKey) {
		t.Fatalf("key extract discovered a legacy database: %s", stdout)
	}
	for _, path := range extracted.TriedPaths {
		if strings.EqualFold(filepath.Base(path), "state.vscdb") {
			t.Fatalf("key extract tried an automatic state database path: %s", path)
		}
	}

	project := t.TempDir()
	rgPath := filepath.Join(t.TempDir(), "rg-test")
	if err := os.WriteFile(rgPath, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FC_RG_PATH", rgPath)
	code, stdout, stderr = executeForTest(t, "doctor", "--project", project, "--format", "json")
	if code != 0 || stderr != "" {
		t.Fatalf("doctor code=%d stderr=%q", code, stderr)
	}
	var report struct {
		OK          bool `json:"ok"`
		Credentials struct {
			OK         bool     `json:"ok"`
			Error      string   `json:"error"`
			Candidates []string `json:"candidates"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Credentials.OK || !strings.Contains(report.Credentials.Error, "credential not configured") || strings.Contains(stdout, apiKey) {
		t.Fatalf("doctor discovered a legacy database: %s", stdout)
	}
	for _, candidate := range report.Credentials.Candidates {
		if strings.EqualFold(filepath.Base(candidate), "state.vscdb") {
			t.Fatalf("doctor returned an automatic state database candidate: %s", candidate)
		}
	}
}
