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

func TestUnknownSkillListsAvailableName(t *testing.T) {
	_, err := ReadMarkdown("missing")
	if err == nil || !strings.Contains(err.Error(), "available skills: fast-context") {
		t.Fatalf("unexpected error: %v", err)
	}
}
