package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/deqiying/fast-context/internal/commandcontract"
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

func TestCanonicalVersionIsStrictButGlobalAliasesRemainCompatible(t *testing.T) {
	code, stdout, stderr := executeForTest(t, "version", "extra")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "unexpected positional argument") {
		t.Fatalf("canonical version trailing arg code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{{"--version", "extra"}, {"-v", "extra"}} {
		code, stdout, stderr = executeForTest(t, args...)
		if code != 0 || stdout == "" || stderr != "" {
			t.Fatalf("global version alias %v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestRegistryBackedHelpIsStatic(t *testing.T) {
	handlers := map[string]commandHandler{}
	for id := range defaultCommandHandlers {
		commandID := id
		handlers[id] = func(context.Context, *commandcontract.Registry, ParsedCommand, io.Writer, io.Writer) int {
			panic("handler called while serving help: " + commandID)
		}
	}
	paths := [][]string{{"search"}, {"key", "extract"}, {"doctor"}, {"skills", "list"}, {"skills", "show"}, {"version"}, {"schema"}, {"skills", "LS"}, {"skills", "GET"}, {"--version"}, {"-v"}}
	for _, path := range paths {
		args := append(append([]string{}, path...), "--help")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := executeWithRegistryAndHandlers(context.Background(), defaultCommandRegistry, handlers, args, &stdout, &stderr)
		if code != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
			t.Fatalf("help %v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	for _, args := range [][]string{{}, {"--help"}, {"key"}, {"skills"}, {"skills", "help"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := executeWithRegistryAndHandlers(context.Background(), defaultCommandRegistry, handlers, args, &stdout, &stderr)
		if code != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
			t.Fatalf("group help %v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRootAndSearchHelpCoverRegistryAndRuntimeContract(t *testing.T) {
	code, root, stderr := executeForTest(t, "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("root help code=%d stderr=%q", code, stderr)
	}
	for _, definition := range defaultCommandRegistry.Commands() {
		if !strings.Contains(root, strings.Join(definition.Path, " ")) {
			t.Fatalf("root help omits %q", strings.Join(definition.Path, " "))
		}
	}
	code, searchHelp, stderr := executeForTest(t, "search", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("search help code=%d stderr=%q", code, stderr)
	}
	for _, identifier := range []string{"FC_BOOTSTRAP_TREE_DEPTH", "FC_HOTSPOT_TOP_K", "FC_RESULT_MAX_LINES", "FC_LINE_MAX_CHARS", "FC_RG_PATH", "FC_INSECURE_TLS", "FAST_CONTEXT_KEY", "WINDSURF_API_KEY"} {
		if !strings.Contains(searchHelp, identifier) {
			t.Fatalf("search help omits %s", identifier)
		}
	}
	code, schemaHelp, stderr := executeForTest(t, "schema", "--help")
	if code != 0 || stderr != "" || !strings.Contains(schemaHelp, "fast-context schema [flags] [command-path...]") {
		t.Fatalf("schema help does not use canonical option ordering: code=%d help=%q stderr=%q", code, schemaHelp, stderr)
	}
}

func TestCommandBindingsCoverRegistryExactly(t *testing.T) {
	if err := validateCommandBindings(defaultCommandRegistry, defaultCommandHandlers); err != nil {
		t.Fatal(err)
	}
	missing := make(map[string]commandHandler, len(defaultCommandHandlers)-1)
	for id, handler := range defaultCommandHandlers {
		if id != "schema" {
			missing[id] = handler
		}
	}
	if err := validateCommandBindings(defaultCommandRegistry, missing); err == nil {
		t.Fatal("binding validation accepted a missing handler")
	}
}

func TestInvalidArgumentsReturnBeforeHandlers(t *testing.T) {
	handlers := map[string]commandHandler{}
	for id := range defaultCommandHandlers {
		commandID := id
		handlers[id] = func(context.Context, *commandcontract.Registry, ParsedCommand, io.Writer, io.Writer) int {
			panic("handler called for invalid argv: " + commandID)
		}
	}
	tests := [][]string{
		{"search", "--unknown"},
		{"key", "extract", "extra"},
		{"doctor", "extra"},
		{"skills", "list", "extra"},
		{"skills", "show"},
		{"version", "extra"},
		{"schema", "--format", "yaml"},
	}
	for _, args := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := executeWithRegistryAndHandlers(context.Background(), defaultCommandRegistry, handlers, args, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("invalid argv %v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestHelpTokenUsedAsFlagValueIsNotIntercepted(t *testing.T) {
	definition := definitionForTest(t, "schema")
	if hasCommandHelpToken(definition, []string{"--format", "--help"}) {
		t.Fatal("--help used as --format value was intercepted as help")
	}
	code, stdout, stderr := executeForTest(t, "schema", "--format", "--help")
	if code != 2 || stderr != "" {
		t.Fatalf("schema value/help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope schemaErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || envelope.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("schema value/help returned invalid error envelope: %#v err=%v", envelope, err)
	}

	for _, test := range []struct {
		id   string
		args []string
	}{
		{id: "search", args: []string{"query", "--project", "--help"}},
		{id: "key.extract", args: []string{"--path", "--help"}},
		{id: "doctor", args: []string{"--project", "--help"}},
		{id: "skills.list", args: []string{"--format", "--help"}},
	} {
		if hasCommandHelpToken(definitionForTest(t, test.id), test.args) {
			t.Fatalf("%s intercepted --help used as a flag value", test.id)
		}
	}
	if !hasCommandHelpToken(definitionForTest(t, "search"), []string{"query", "--verbose", "--help"}) {
		t.Fatal("help after a boolean presence flag was not recognized")
	}

	code, stdout, stderr = executeForTest(t, "skills", "typo", "--help")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "unknown skills command: typo") {
		t.Fatalf("unknown namespace command was swallowed by help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{{"schema", "--unknown", "--help"}, {"schema", "--format", "yaml", "--help"}} {
		code, stdout, stderr = executeForTest(t, args...)
		if code != 2 || stderr != "" {
			t.Fatalf("invalid option before help %v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if hasCommandHelpToken(definitionForTest(t, "search"), []string{"query", "--max-turns", "invalid", "--help"}) {
		t.Fatal("invalid option value before --help was swallowed")
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

func TestSkillsAliasesAndJSONEscapingRemainCompatible(t *testing.T) {
	code, stdout, stderr := executeForTest(t, "skills", "LS", "--format", " JSON ")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("skills LS code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = executeForTest(t, "skills", "GET", "fast-context", "--format", "json")
	if code != 0 || stderr != "" {
		t.Fatalf("skills GET code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `\u003c`) {
		t.Fatal("skills JSON no longer uses encoding/json HTML escaping")
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
