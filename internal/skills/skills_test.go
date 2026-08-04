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
	if !strings.Contains(content, "does not read runtime configuration, credentials, project files, or environment values") {
		t.Fatal("SKILL.md must describe schema discovery as static")
	}
	if !strings.Contains(content, "does not access the network, write files, or run command handlers") {
		t.Fatal("SKILL.md must describe schema discovery side-effect boundaries")
	}
	if !strings.Contains(content, "compact by default") || !strings.Contains(content, "`--pretty`") {
		t.Fatal("SKILL.md must describe compact JSON and explicit human pretty output")
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
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("cli-contract.md must contain %q", required)
		}
	}
}

func TestUnknownSkillListsAvailableName(t *testing.T) {
	_, err := ReadMarkdown("missing")
	if err == nil || !strings.Contains(err.Error(), "available skills: fast-context") {
		t.Fatalf("unexpected error: %v", err)
	}
}
