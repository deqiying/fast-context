package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate 把凭据查找限定在临时 HOME，避免读取开发机的真实凭据。
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("FAST_CONTEXT_KEY", "")
	t.Setenv("WINDSURF_API_KEY", "")
	return home
}

func writeRipgrepFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rg-fixture")
	if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInspectReadyEnvironment(t *testing.T) {
	isolate(t)
	t.Setenv("FC_RG_PATH", writeRipgrepFixture(t))
	t.Setenv("FAST_CONTEXT_KEY", "fixture-api-key-123456")
	project := t.TempDir()

	report := Inspect(project)
	if !report.OK() || !report.ProjectExists || report.RipgrepErr != nil || report.CredentialsErr != nil {
		t.Fatalf("expected ready report: %#v", report)
	}
	payload := report.JSON()
	if payload["ok"] != true {
		t.Fatalf("JSON ok=%v", payload["ok"])
	}
	creds := payload["credentials"].(map[string]any)
	if creds["source"] != "FAST_CONTEXT_KEY" || creds["source_type"] != "env" || creds["error"] != "" {
		t.Fatalf("unexpected credentials: %#v", creds)
	}
	if key, _ := creds["key"].(string); key == "" || strings.Contains(key, "fixture-api-key-123456") {
		t.Fatalf("key must be redacted, got %q", key)
	}
	for _, candidate := range creds["candidates"].([]string) {
		if strings.EqualFold(filepath.Base(candidate), "state.vscdb") {
			t.Fatalf("doctor must not report an automatic state database candidate: %s", candidate)
		}
	}
	rg := payload["ripgrep"].(map[string]any)
	if rg["ok"] != true || rg["source"] != "fc_rg_path" {
		t.Fatalf("unexpected ripgrep: %#v", rg)
	}
}

func TestInspectReportsEachMissingComponent(t *testing.T) {
	isolate(t)
	t.Setenv("FC_RG_PATH", filepath.Join(t.TempDir(), "missing-rg"))
	missingProject := filepath.Join(t.TempDir(), "missing-project")

	report := Inspect(missingProject)
	if report.OK() || report.ProjectExists || report.RipgrepErr == nil || report.CredentialsErr == nil {
		t.Fatalf("expected every component to fail: %#v", report)
	}
	payload := report.JSON()
	if payload["ok"] != false {
		t.Fatalf("JSON ok=%v", payload["ok"])
	}
	project := payload["project"].(map[string]any)
	if project["exists"] != false || project["path"] != missingProject {
		t.Fatalf("unexpected project: %#v", project)
	}
	rg := payload["ripgrep"].(map[string]any)
	if rg["ok"] != false || rg["error"] == "" {
		t.Fatalf("unexpected ripgrep: %#v", rg)
	}
	creds := payload["credentials"].(map[string]any)
	if creds["ok"] != false || creds["error"] == "" || creds["key"] != "" {
		t.Fatalf("unexpected credentials: %#v", creds)
	}
}

func TestInspectTreatsRegularFileAsMissingProject(t *testing.T) {
	isolate(t)
	t.Setenv("FC_RG_PATH", writeRipgrepFixture(t))
	t.Setenv("FAST_CONTEXT_KEY", "fixture-api-key-123456")
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if report := Inspect(file); report.ProjectExists || report.OK() {
		t.Fatalf("a regular file is not a project directory: %#v", report)
	}
}
