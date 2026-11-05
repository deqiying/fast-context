package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deqiying/fast-context/internal/doctor"
)

// isolateCredentials 把凭据查找限定在临时 HOME，避免读取开发机的真实凭据。
func isolateCredentials(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("FAST_CONTEXT_KEY", "")
	t.Setenv("WINDSURF_API_KEY", "")
}

func writeRipgrepFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rg-fixture")
	if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDoctorToolMatchesCLIEncodingAndRedactsKey(t *testing.T) {
	isolateCredentials(t)
	t.Setenv("FC_RG_PATH", writeRipgrepFixture(t))
	t.Setenv("FAST_CONTEXT_KEY", "fixture-api-key-123456")
	project := t.TempDir()
	fake := &fakeSearch{}
	session := connectInMemory(t, testConfig(fake, io.Discard))

	result := callTool(t, session, doctorToolName, map[string]any{"project_path": project})
	text := resultText(t, result)
	if result.IsError {
		t.Fatalf("doctor returned isError: %s", text)
	}
	var want bytes.Buffer
	if err := json.NewEncoder(&want).Encode(doctor.Inspect(project).JSON()); err != nil {
		t.Fatal(err)
	}
	if text != want.String() {
		t.Fatalf("doctor text differs from CLI encoding:\n got %s\nwant %s", text, want.String())
	}

	var report struct {
		OK          bool `json:"ok"`
		Credentials struct {
			Key        string   `json:"key"`
			Candidates []string `json:"candidates"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Credentials.Key == "" || strings.Contains(report.Credentials.Key, "fixture-api-key-123456") {
		t.Fatalf("unexpected doctor report or unredacted key: %s", text)
	}
	for _, candidate := range report.Credentials.Candidates {
		if strings.EqualFold(filepath.Base(candidate), "state.vscdb") {
			t.Fatalf("doctor returned an automatic state database candidate: %s", candidate)
		}
	}
	if fake.callCount() != 0 {
		t.Fatalf("doctor triggered %d searches", fake.callCount())
	}
}

func TestDoctorToolPathValidation(t *testing.T) {
	isolateCredentials(t)
	t.Setenv("FC_RG_PATH", writeRipgrepFixture(t))
	session := connectInMemory(t, testConfig(&fakeSearch{}, io.Discard))

	for _, path := range []string{"", "   ", "relative/path"} {
		result := callTool(t, session, doctorToolName, map[string]any{"project_path": path})
		if !result.IsError {
			t.Fatalf("project_path %q must be rejected, got %s", path, resultText(t, result))
		}
	}
	result := callTool(t, session, doctorToolName, map[string]any{})
	if !result.IsError {
		t.Fatalf("missing project_path must be rejected, got %s", resultText(t, result))
	}

	// 不存在的绝对路径是检查结论而非调用错误：isError=false，由 ok/project.exists 表达。
	missing := filepath.Join(t.TempDir(), "missing")
	result = callTool(t, session, doctorToolName, map[string]any{"project_path": missing})
	text := resultText(t, result)
	var report struct {
		OK      bool `json:"ok"`
		Project struct {
			Path   string `json:"path"`
			Exists bool   `json:"exists"`
		} `json:"project"`
	}
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("doctor text is not JSON: %q", text)
	}
	if result.IsError || report.OK || report.Project.Exists || report.Project.Path != missing {
		t.Fatalf("missing project: isError=%v report=%s", result.IsError, text)
	}
}
