package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/deqiying/fast-context/internal/commandcontract"
	"github.com/deqiying/fast-context/internal/version"
)

func TestSchemaFullAndTargetedManifest(t *testing.T) {
	full, err := BuildManifest(defaultCommandRegistry, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !full.OK || full.Kind != commandcontract.ManifestKind || full.ManifestVersion != 1 || full.CLI.Name != "fast-context" || full.CLI.Version != "test" || full.Scope.Mode != "all" || len(full.Scope.Path) != 0 || len(full.Commands) != 7 {
		t.Fatalf("unexpected full manifest envelope: %#v", full)
	}
	if !reflect.DeepEqual(full.ArgvConventions.Ordering, []string{"command_path", "options", "end_of_options_when_needed", "positionals"}) {
		t.Fatalf("unexpected argv conventions: %#v", full.ArgvConventions)
	}

	for _, path := range [][]string{{"search"}, {"key", "extract"}, {"schema"}} {
		target, err := BuildManifest(defaultCommandRegistry, "test", path)
		if err != nil {
			t.Fatalf("BuildManifest(%v): %v", path, err)
		}
		if target.Scope.Mode != "command" || !reflect.DeepEqual(target.Scope.Path, path) || len(target.Commands) != 1 {
			t.Fatalf("unexpected target envelope for %v: %#v", path, target)
		}
		var fromFull *commandcontract.ManifestCommand
		for index := range full.Commands {
			if full.Commands[index].ID == target.Commands[0].ID {
				fromFull = &full.Commands[index]
				break
			}
		}
		if fromFull == nil || !reflect.DeepEqual(*fromFull, target.Commands[0]) {
			t.Fatalf("target command %v differs from full manifest", path)
		}
	}
}

func TestSchemaCLIReportsStructuredArgumentErrors(t *testing.T) {
	tests := [][]string{
		{"schema", "skills", "ls"},
		{"schema", "key"},
		{"schema", "missing"},
		{"schema", "--format", "yaml"},
		{"schema", "--unknown"},
		{"schema", "Skills", "list"},
	}
	wantPaths := []string{"doctor", "key extract", "schema", "search", "skills list", "skills show", "version"}
	for _, args := range tests {
		code, stdout, stderr := executeForTest(t, args...)
		if code != 2 || stderr != "" {
			t.Fatalf("Execute(%v) code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		var envelope schemaErrorEnvelope
		if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
			t.Fatalf("Execute(%v) returned invalid JSON: %v", args, err)
		}
		if envelope.OK || envelope.Error.Code != "INVALID_ARGUMENT" || envelope.Error.Message == "" || !reflect.DeepEqual(envelope.AvailablePaths, wantPaths) {
			t.Fatalf("Execute(%v) returned unexpected error: %#v", args, envelope)
		}
	}
}

func TestSchemaErrorsDoNotReflectUnknownInput(t *testing.T) {
	const marker = "review-secret-argv-marker"
	for _, args := range [][]string{{"schema", "-" + marker}, {"schema", marker}} {
		code, stdout, stderr := executeForTest(t, args...)
		if code != 2 || stderr != "" {
			t.Fatalf("Execute(%v) code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		if strings.Contains(stdout, marker) {
			t.Fatalf("Execute(%v) reflected an unknown argv token: %s", args, stdout)
		}
	}
}

func TestSchemaOutputIsDeterministicAndUsesRuntimeVersion(t *testing.T) {
	firstCode, first, firstErr := executeForTest(t, "schema", "--format", "json")
	secondCode, second, secondErr := executeForTest(t, "schema")
	if firstCode != 0 || secondCode != 0 || firstErr != "" || secondErr != "" || first != second {
		t.Fatalf("schema output is not deterministic: codes=(%d,%d) stderr=(%q,%q) equal=%t", firstCode, secondCode, firstErr, secondErr, first == second)
	}
	var manifest commandcontract.Manifest
	if err := json.Unmarshal([]byte(first), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.CLI.Version != version.Version {
		t.Fatalf("manifest version=%q, want runtime %q", manifest.CLI.Version, version.Version)
	}
	if strings.Contains(first, "\\u003c") || !strings.HasSuffix(first, "\n") {
		t.Fatalf("schema encoder contract changed: %q", first)
	}
}

func TestSchemaDoesNotReadRuntimeStateOrNormalHandlers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "hostile-home-marker")
	configDir := filepath.Join(home, ".config", "fast-context")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "hostile-project-marker")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FAST_CONTEXT_KEY", "hostile-credential-marker")
	t.Setenv("WINDSURF_API_KEY", "hostile-windsurf-marker")
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })

	called := map[string]int{}
	handlers := map[string]commandHandler{}
	for id, handler := range defaultCommandHandlers {
		if id == "schema" {
			handlers[id] = handler
			continue
		}
		commandID := id
		handlers[id] = func(context.Context, *commandcontract.Registry, ParsedCommand, io.Writer, io.Writer) int {
			called[commandID]++
			panic("normal handler was called while serving schema")
		}
	}
	for _, args := range [][]string{{"schema"}, {"schema", "search"}, {"schema", "--help"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := executeWithRegistryAndHandlers(context.Background(), defaultCommandRegistry, handlers, args, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("Execute(%v) code=%d stderr=%q", args, code, stderr.String())
		}
		for _, marker := range []string{"hostile-home-marker", "hostile-project-marker", "hostile-credential-marker", "hostile-windsurf-marker"} {
			if strings.Contains(stdout.String(), marker) {
				t.Fatalf("Execute(%v) leaked %q", args, marker)
			}
		}
	}
	if len(called) != 0 {
		t.Fatalf("schema called normal handlers: %v", called)
	}
}

func TestSchemaWriterFailureReturnsOne(t *testing.T) {
	definition := definitionForTest(t, "schema")
	parsed, err := parseCommand(definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := runSchema(defaultCommandRegistry, parsed, failingWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "failed to write schema") {
		t.Fatalf("runSchema writer failure code=%d stderr=%q", code, stderr.String())
	}
}

func TestSchemaContractMetadata(t *testing.T) {
	manifest, err := BuildManifest(defaultCommandRegistry, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]commandcontract.ManifestCommand{}
	for _, command := range manifest.Commands {
		byID[command.ID] = command
		if command.InputSchema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || command.InputSchema["type"] != "object" || command.InputSchema["additionalProperties"] != false {
			t.Fatalf("command %q has invalid input schema envelope", command.ID)
		}
	}
	if !containsString(byID["search"].SideEffects, "credential_source_read") || !containsString(byID["doctor"].SideEffects, "credential_source_read") {
		t.Fatal("search and doctor must disclose credential source reads")
	}
	if len(byID["skills.list"].SideEffects) != 0 || !reflect.DeepEqual(byID["skills.show"].SideEffects, []string{"embedded_resource_read"}) {
		t.Fatal("skills side effects drifted")
	}
	if byID["skills.list"].PathMatching == nil || !reflect.DeepEqual(byID["skills.list"].PathMatching.CaseInsensitiveSegments, []int{1}) || byID["search"].PathMatching != nil {
		t.Fatal("path matching metadata drifted")
	}
	if byID["doctor"].Output.ExitCodes[0].Meaning != "completed_check_json_ok_may_be_false" {
		t.Fatalf("doctor exit semantics drifted: %#v", byID["doctor"].Output.ExitCodes)
	}
	if byID["key.extract"].Output.Redaction != "api_key_redacted" || !containsString(byID["key.extract"].SideEffects, "temporary_filesystem_write_when_sqlite") {
		t.Fatal("key extract redaction or SQLite side effect drifted")
	}
}

func TestSchemaMatchesFixedVersionGolden(t *testing.T) {
	manifest, err := BuildManifest(defaultCommandRegistry, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := encodeSchemaJSON(&buffer, manifest); err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join("testdata", "cli-command-manifest-v1.golden.json")
	if os.Getenv("UPDATE_FAST_CONTEXT_CLI_MANIFEST_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, buffer.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buffer.Bytes(), want) {
		t.Fatal("CLI command manifest differs from golden; review the contract diff and rerun with UPDATE_FAST_CONTEXT_CLI_MANIFEST_GOLDEN=1")
	}
}

func TestAvailableCanonicalPathsAreSorted(t *testing.T) {
	paths := availableCanonicalPaths(defaultCommandRegistry)
	if !sort.StringsAreSorted(paths) {
		t.Fatalf("available paths are not sorted: %v", paths)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture write failure")
}
