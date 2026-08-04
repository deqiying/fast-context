package cli

import (
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
	OK       bool                  `json:"ok"`
	Skill    skillDefinitionOutput `json:"skill"`
	Resource string                `json:"resource,omitempty"`
	Content  string                `json:"content"`
}

func runParsedSkillsList(parsed ParsedCommand, stdout, _ io.Writer) int {
	format := parsed.String("format")
	pretty := parsed.Bool("pretty")
	definitions := skills.Definitions()
	items := make([]skillDefinitionOutput, 0, len(definitions))
	for _, definition := range definitions {
		items = append(items, skillDefinitionData(definition))
	}
	if format == "json" {
		return encodeJSON(stdout, skillsListOutput{OK: true, Skills: items, Total: len(items)}, pretty)
	}
	for _, item := range items {
		fmt.Fprintf(stdout, "%s\t%s\n", item.ID, item.Description)
	}
	return 0
}

func runParsedSkillsShow(parsed ParsedCommand, stdout, stderr io.Writer) int {
	skillName := parsed.String("skill")
	format := parsed.String("format")
	pretty := parsed.Bool("pretty")
	reference := parsed.String("reference")
	definition, err := skills.Describe(skillName)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	resource := ""
	var content string
	if reference == "" {
		content, err = skills.ReadMarkdown(skillName)
	} else {
		content, resource, err = skills.ReadReference(skillName, reference)
	}
	if err != nil {
		fmt.Fprintf(stderr, "Error: failed to read embedded skill resource: %s\n", err)
		return 1
	}
	if format == "content" {
		fmt.Fprint(stdout, content)
		return 0
	}
	return encodeJSON(stdout, skillShowOutput{
		OK:       true,
		Skill:    skillDefinitionData(definition),
		Resource: resource,
		Content:  content,
	}, pretty)
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
func encodeJSON(w io.Writer, value any, pretty bool) int {
	encoder := newJSONEncoder(w, pretty)
	if err := encoder.Encode(value); err != nil {
		return 1
	}
	return 0
}
