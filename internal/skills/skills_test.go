package skills

import (
	"os"
	"strings"
	"testing"
)

func TestReadMarkdownMatchesSourceAsset(t *testing.T) {
	content, err := ReadMarkdown("code-context")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("assets/fast-context/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(string(source), "\r\n", "\n")
	if content != want {
		t.Fatal("embedded SKILL.md differs from the source asset")
	}
	if !strings.Contains(content, "external-service operation") {
		t.Fatal("SKILL.md must disclose the external transmission boundary")
	}
	if !strings.Contains(content, "do not request separate per-search authorization") {
		t.Fatal("SKILL.md must allow search after the required preflight")
	}
	if !strings.Contains(content, "ok: true") {
		t.Fatal("SKILL.md must require a successful doctor preflight")
	}
	if !strings.Contains(content, "`state.vscdb` files are never discovered automatically") {
		t.Fatal("SKILL.md must prohibit automatic state.vscdb discovery")
	}
	if !strings.Contains(content, "key extract --path") {
		t.Fatal("SKILL.md must document explicit state.vscdb extraction")
	}
	if !strings.Contains(content, "known commands directly") {
		t.Fatal("SKILL.md must allow known commands to be used directly")
	}
	if !strings.Contains(content, "targeted schema") {
		t.Fatal("SKILL.md must recommend targeted schema discovery for exact command contracts")
	}
	if !strings.Contains(content, "full schema") {
		t.Fatal("SKILL.md must reserve full schema discovery for general command discovery")
	}
	for _, required := range []string{
		"does not read runtime configuration, credentials, project files, or environment values",
		"access the network",
		"write files",
		"run command handlers",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("SKILL.md must describe schema discovery boundary %q", required)
		}
	}
	if !strings.Contains(content, "compact by default") || !strings.Contains(content, "`--pretty`") {
		t.Fatal("SKILL.md must describe compact JSON and explicit human pretty output")
	}
	for _, required := range []string{
		"Reuse the same `cli.version + manifest_version + canonical path` contract",
		"Use `--help` only for human help",
		"fast-context skills show --format content fast-context",
		"fast-context skills show --format content --reference cli-contract fast-context",
		"do not resolve, schema-query, or preflight the CLI",
		"Treat the loaded version-matched Skill as the sole CLI workflow",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("SKILL.md must contain %q", required)
		}
	}
	if strings.Contains(content, "--tree-depth 0") {
		t.Fatal("SKILL.md must not prescribe classic auto-depth in the default search example")
	}
}

func TestDefinitionAndAliases(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 1 {
		t.Fatalf("Definitions returned %d items, want 1", len(definitions))
	}
	definition, err := Describe("semantic_code_search")
	if err != nil {
		t.Fatal(err)
	}
	if definition.ID != "fast-context" {
		t.Fatalf("Describe returned ID %q", definition.ID)
	}
	definition.Aliases[0] = "mutated"
	clean, err := Describe("fast-context")
	if err != nil {
		t.Fatal(err)
	}
	if clean.Aliases[0] == "mutated" {
		t.Fatal("Describe exposed mutable package state")
	}
}

func TestEmbeddedCLIContractDescribesStaticSchemaDiscovery(t *testing.T) {
	const assetPath = "assets/fast-context/references/cli-contract.md"
	embedded, err := assets.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.ReplaceAll(string(embedded), "\r\n", "\n")
	want := strings.ReplaceAll(string(source), "\r\n", "\n")
	if content != want {
		t.Fatal("embedded cli-contract.md differs from the source asset")
	}
	for _, required := range []string{
		"fast-context schema --format json",
		"canonical path",
		"JSON-only and static",
		"does not read credentials",
		"not native-tool registration",
		"doctor --project . --format json",
		"one compact JSON line",
		"`--pretty`",
		"fast-context skills show --format content --reference cli-contract fast-context",
		"Emit options in `x-cli-binding.order`",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("cli-contract.md must contain %q", required)
		}
	}
}

func TestReadReferenceMatchesSourceAssetAndRejectsPaths(t *testing.T) {
	content, resource, err := ReadReference("code-context", "cli-contract")
	if err != nil {
		t.Fatal(err)
	}
	if resource != "references/cli-contract.md" {
		t.Fatalf("ReadReference resource=%q", resource)
	}
	source, err := os.ReadFile("assets/fast-context/references/cli-contract.md")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(string(source), "\r\n", "\n")
	if content != want {
		t.Fatal("embedded cli-contract.md differs from the source asset")
	}
	for _, reference := range []string{"", "missing", "../cli-contract", "references/cli-contract.md", `C:\private\cli-contract.md`} {
		_, _, err := ReadReference("fast-context", reference)
		if err == nil || !strings.Contains(err.Error(), "available references: cli-contract") {
			t.Fatalf("ReadReference(%q) error=%v", reference, err)
		}
	}
	if _, _, err := ReadReference("missing", "cli-contract"); err == nil || !strings.Contains(err.Error(), "available skills: fast-context") {
		t.Fatalf("unexpected unknown skill error: %v", err)
	}
}

func TestUnknownSkillListsAvailableName(t *testing.T) {
	_, err := ReadMarkdown("missing")
	if err == nil || !strings.Contains(err.Error(), "available skills: fast-context") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAIMetadataMatchesUnknownEntrypointWorkflow(t *testing.T) {
	content, err := os.ReadFile("assets/fast-context/agents/openai.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	for _, required := range []string{
		`default_prompt: "Use $fast-context to locate an unknown local-code entrypoint, then verify every candidate with deterministic local tools."`,
		"allow_implicit_invocation: true",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("openai.yaml must contain %q", required)
		}
	}
}
