package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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

func runSkills(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printSkillsHelp(stdout)
		return 0
	}

	switch strings.ToLower(args[0]) {
	case "list", "ls":
		return runSkillsList(args[1:], stdout, stderr)
	case "show", "get", "read":
		return runSkillsShow(args[1:], stdout, stderr)
	default:
		return usageError(stderr, "unknown skills command: "+args[0])
	}
}

func runSkillsList(args []string, stdout, stderr io.Writer) int {
	format, positionals, code := parseSkillsFormat(args, "text", stdout, stderr)
	if code >= 0 {
		return code
	}
	if len(positionals) != 0 {
		return usageError(stderr, "skills list does not accept positional arguments")
	}
	if format != "text" && format != "json" {
		return usageError(stderr, "skills list --format must be text or json")
	}

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

func runSkillsShow(args []string, stdout, stderr io.Writer) int {
	format, positionals, code := parseSkillsFormat(args, "content", stdout, stderr)
	if code >= 0 {
		return code
	}
	if len(positionals) != 1 {
		return usageError(stderr, "skills show requires exactly one skill name")
	}
	if format != "content" && format != "json" {
		return usageError(stderr, "skills show --format must be content or json")
	}

	definition, err := skills.Describe(positionals[0])
	if err != nil {
		return usageError(stderr, err.Error())
	}
	content, err := skills.ReadMarkdown(positionals[0])
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

func parseSkillsFormat(args []string, defaultFormat string, stdout, stderr io.Writer) (string, []string, int) {
	format := defaultFormat
	positionals := make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-h" || arg == "--help" {
			printSkillsHelp(stdout)
			return "", nil, 0
		}
		if !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}
		name, value, hasValue := splitFlag(arg)
		if name != "--format" {
			return "", nil, usageError(stderr, "unknown flag: "+name)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return "", nil, usageError(stderr, "missing value for --format")
			}
			i++
			value = args[i]
		}
		format = strings.ToLower(strings.TrimSpace(value))
	}
	return format, positionals, -1
}

func skillDefinitionData(definition skills.Definition) skillDefinitionOutput {
	return skillDefinitionOutput{
		ID:           definition.ID,
		Aliases:      append([]string(nil), definition.Aliases...),
		Capabilities: append([]string(nil), definition.Capabilities...),
		Description:  definition.Description,
	}
}

func encodeJSON(w io.Writer, value any) int {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return 1
	}
	return 0
}

func printSkillsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  fast-context skills list [--format text|json]
  fast-context skills show <skill> [--format content|json]

`)
}
