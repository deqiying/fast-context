package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/deqiying/fast-context/internal/skills"
)

type skillDefinitionOutput struct {
	ID           string   `json:"id"`
	Aliases      []string `json:"aliases"`
	Capabilities []string `json:"capabilities"`
	Description  string   `json:"description"`
}

type skillsListOutput struct {
	OK     bool                    `json:"ok"`
	Skills []skillDefinitionOutput `json:"skills"`
	Total  int                     `json:"total"`
}

type skillShowOutput struct {
	OK      bool                  `json:"ok"`
	Skill   skillDefinitionOutput `json:"skill"`
	Content string                `json:"content"`
}

func runParsedSkillsList(parsed ParsedCommand, stdout, _ io.Writer) int {
	format := parsed.String("format")
	definitions := skills.Definitions()
	items := make([]skillDefinitionOutput, 0, len(definitions))
	for _, definition := range definitions {
		items = append(items, skillDefinitionData(definition))
	}
	if format == "json" {
		return encodeJSON(stdout, skillsListOutput{OK: true, Skills: items, Total: len(items)})
	}
	for _, item := range items {
		fmt.Fprintf(stdout, "%s\t%s\n", item.ID, item.Description)
	}
	return 0
}

func runParsedSkillsShow(parsed ParsedCommand, stdout, stderr io.Writer) int {
	skillName := parsed.String("skill")
	format := parsed.String("format")
	definition, err := skills.Describe(skillName)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	content, err := skills.ReadMarkdown(skillName)
	if err != nil {
		fmt.Fprintf(stderr, "Error: failed to read embedded skill: %s\n", err)
		return 1
	}
	if format == "content" {
		fmt.Fprint(stdout, content)
		return 0
	}
	return encodeJSON(stdout, skillShowOutput{
		OK:      true,
		Skill:   skillDefinitionData(definition),
		Content: content,
	})
}

func skillDefinitionData(definition skills.Definition) skillDefinitionOutput {
	return skillDefinitionOutput{
		ID:           definition.ID,
		Aliases:      append([]string(nil), definition.Aliases...),
		Capabilities: append([]string(nil), definition.Capabilities...),
		Description:  definition.Description,
	}
}

// encodeJSON intentionally keeps encoding/json's default HTML escaping because
// this is part of the existing skills JSON byte contract. Schema output has a
// dedicated encoder and must not change this behavior.
func encodeJSON(w io.Writer, value any) int {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return 1
	}
	return 0
}
