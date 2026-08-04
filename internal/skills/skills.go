package skills

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed assets/fast-context/**
var assets embed.FS

type Definition struct {
	ID           string
	Aliases      []string
	Capabilities []string
	Description  string
}

var definition = Definition{
	ID:           "fast-context",
	Aliases:      []string{"semantic-code-search", "code-context"},
	Capabilities: []string{"semantic_code_search", "code_context"},
	Description:  "Locate unknown local code entrypoints with remote semantic search, then verify candidates with deterministic local tools.",
}

var referenceAssets = map[string]string{
	"cli-contract": "references/cli-contract.md",
}

func Definitions() []Definition {
	return []Definition{cloneDefinition(definition)}
}

func Describe(name string) (Definition, error) {
	if !matches(name) {
		return Definition{}, unknownSkillError(name)
	}
	return cloneDefinition(definition), nil
}

func ReadMarkdown(name string) (string, error) {
	if !matches(name) {
		return "", unknownSkillError(name)
	}
	return readTextAsset("SKILL.md")
}

func ReadReference(name, reference string) (string, string, error) {
	if !matches(name) {
		return "", "", unknownSkillError(name)
	}
	resource, ok := referenceAssets[reference]
	if !ok {
		return "", "", fmt.Errorf("unknown reference: %s; available references: %s", reference, strings.Join(referenceNames(), ", "))
	}
	content, err := readTextAsset(resource)
	if err != nil {
		return "", "", err
	}
	return content, resource, nil
}

func readTextAsset(resource string) (string, error) {
	data, err := assets.ReadFile("assets/fast-context/" + resource)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

func referenceNames() []string {
	names := make([]string, 0, len(referenceAssets))
	for name := range referenceAssets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Names() []string {
	return []string{definition.ID}
}

func matches(name string) bool {
	normalized := normalize(name)
	if normalized == normalize(definition.ID) {
		return true
	}
	for _, alias := range definition.Aliases {
		if normalized == normalize(alias) {
			return true
		}
	}
	return false
}

func normalize(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "_", "-")
}

func unknownSkillError(name string) error {
	return fmt.Errorf("unknown skill: %s; available skills: %s", name, strings.Join(Names(), ", "))
}

func cloneDefinition(value Definition) Definition {
	value.Aliases = append([]string(nil), value.Aliases...)
	value.Capabilities = append([]string(nil), value.Capabilities...)
	return value
}
