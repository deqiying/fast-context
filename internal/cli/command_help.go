package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/deqiying/fast-context/internal/commandcontract"
)

func printRegistryRootHelp(w io.Writer, registry *commandcontract.Registry) {
	fmt.Fprintln(w, "fast-context is a CLI version of the fast-context MCP code search tool.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	commands := registry.Commands()
	sort.Slice(commands, func(i, j int) bool {
		leftRank := commandcontract.CategoryRank(commands[i].Category)
		rightRank := commandcontract.CategoryRank(commands[j].Category)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return strings.Join(commands[i].Path, " ") < strings.Join(commands[j].Path, " ")
	})
	for _, definition := range commands {
		fmt.Fprintf(w, "  %-43s %s\n", commandUsage(definition), definition.Summary)
	}
	fmt.Fprintln(w, "  fast-context --version")
	fmt.Fprintln(w, "  fast-context -v")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Credentials:")
	fmt.Fprintln(w, "  FAST_CONTEXT_KEY   Explicit fast-context key (highest priority)")
	fmt.Fprintln(w, "  Local config       $HOME/.config/fast-context/config.json")
	fmt.Fprintln(w, "  WINDSURF_API_KEY   Compatibility environment variable")
	fmt.Fprintln(w)
}

func printNamespaceHelp(w io.Writer, registry *commandcontract.Registry, namespace commandcontract.NamespaceDefinition) {
	fmt.Fprintf(w, "%s\n\n", namespace.Summary)
	fmt.Fprintln(w, "Usage:")
	prefix := strings.Join(namespace.Path, " ")
	for _, definition := range registry.Commands() {
		if len(definition.Path) <= len(namespace.Path) || strings.Join(definition.Path[:len(namespace.Path)], " ") != prefix {
			continue
		}
		fmt.Fprintf(w, "  %-43s %s\n", commandUsage(definition), definition.Summary)
	}
	fmt.Fprintln(w)
}

func printCommandHelp(w io.Writer, definition commandcontract.CommandDefinition) {
	fmt.Fprintf(w, "%s\n\n", definition.Summary)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintf(w, "  %s\n", commandUsage(definition))
	if len(definition.Aliases) > 0 {
		aliases := make([]string, 0, len(definition.Aliases))
		for _, alias := range definition.Aliases {
			aliases = append(aliases, "fast-context "+strings.Join(alias, " "))
		}
		sort.Strings(aliases)
		fmt.Fprintf(w, "\nAliases: %s\n", strings.Join(aliases, ", "))
	}
	if len(definition.Positionals) > 0 {
		fmt.Fprintln(w, "\nArguments:")
		for _, positional := range definition.Positionals {
			fmt.Fprintf(w, "  %-27s %s\n", positionalUsage(positional), positional.Description)
		}
	}
	if len(definition.Options) > 0 {
		fmt.Fprintln(w, "\nFlags:")
		for _, option := range definition.Options {
			fmt.Fprintf(w, "  %-27s %s%s\n", optionUsage(option), option.Description, optionHelpSuffix(option))
		}
	}
	fmt.Fprintln(w, "  -h, --help                  Show this help.")
	printCommandAppendix(w, definition.ID)
	fmt.Fprintln(w)
}

func commandUsage(definition commandcontract.CommandDefinition) string {
	parts := []string{"fast-context", strings.Join(definition.Path, " ")}
	if definition.ID == "schema" && len(definition.Options) > 0 {
		parts = append(parts, "[flags]")
	}
	for _, positional := range definition.Positionals {
		parts = append(parts, positionalUsage(positional))
	}
	if definition.ID != "schema" && len(definition.Options) > 0 {
		parts = append(parts, "[flags]")
	}
	return strings.Join(parts, " ")
}

func positionalUsage(definition commandcontract.PositionalDefinition) string {
	name := strings.ReplaceAll(definition.Name, "_", "-")
	if definition.Variadic {
		if definition.Required {
			return "<" + name + "...>"
		}
		return "[" + name + "...]"
	}
	if definition.Required {
		return "<" + name + ">"
	}
	return "[" + name + "]"
}

func optionUsage(definition commandcontract.OptionDefinition) string {
	tokens := append([]string{definition.Flag}, definition.Aliases...)
	label := strings.Join(tokens, ", ")
	if definition.Type == commandcontract.TypeBoolean {
		return label + "[=true|false]"
	}
	value := "value"
	if len(definition.Enum) > 0 {
		value = strings.Join(definition.Enum, "|")
	} else if definition.ArgvDecoder != "" {
		value = "duration|milliseconds"
	} else if definition.Type == commandcontract.TypeInteger {
		value = "integer"
	}
	return label + " <" + value + ">"
}

func optionHelpSuffix(definition commandcontract.OptionDefinition) string {
	parts := []string{}
	if definition.HasDefault {
		parts = append(parts, fmt.Sprintf("default: %v", definition.Default))
	} else if kind, ok := definition.DefaultSource["kind"].(string); ok {
		switch kind {
		case "current_working_directory":
			parts = append(parts, "default: current directory")
		case "environment_or_fallback":
			parts = append(parts, fmt.Sprintf("default: %v via %v", definition.DefaultSource["fallback"], definition.DefaultSource["environment"]))
		}
	}
	if definition.Clamp && definition.Minimum != nil && definition.Maximum != nil {
		parts = append(parts, fmt.Sprintf("clamped: %d..%d", *definition.Minimum, *definition.Maximum))
	}
	if definition.Repeatable {
		parts = append(parts, "repeatable")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

func printCommandAppendix(w io.Writer, commandID string) {
	switch commandID {
	case "search":
		fmt.Fprintln(w, "\nEnvironment:")
		fmt.Fprintln(w, "  FC_MAX_TURNS, FC_MAX_COMMANDS, FC_TIMEOUT_MS, FC_REPO_MAP_MODE,")
		fmt.Fprintln(w, "  FC_BOOTSTRAP_ENABLED, FC_BOOTSTRAP_TREE_DEPTH, FC_BOOTSTRAP_MAX_TURNS,")
		fmt.Fprintln(w, "  FC_BOOTSTRAP_MAX_COMMANDS, FC_HOTSPOT_TOP_K, FC_HOTSPOT_TREE_DEPTH,")
		fmt.Fprintln(w, "  FC_HOTSPOT_MAX_BYTES, FC_INCLUDE_SNIPPETS, FC_RESULT_MAX_LINES,")
		fmt.Fprintln(w, "  FC_LINE_MAX_CHARS, FC_RG_PATH, FC_INSECURE_TLS, FAST_CONTEXT_DEBUG,")
		fmt.Fprintln(w, "  FAST_CONTEXT_KEY, WINDSURF_API_KEY")
		fmt.Fprintln(w, "\nFC_TIMEOUT_MS is clamped to 1000..300000. Explicit --timeout keeps compatibility with any parseable Go duration or integer milliseconds.")
	case "key.extract":
		fmt.Fprintln(w, "\nWithout --path, this command checks only the default Linux/WSL Devin CLI TOML.")
		fmt.Fprintln(w, "Windsurf/Devin state.vscdb files are never discovered automatically; pass --path to inspect one explicitly.")
	case "doctor":
		fmt.Fprintln(w, "\nCredentials are resolved in this order: FAST_CONTEXT_KEY, local config,")
		fmt.Fprintln(w, "WINDSURF_API_KEY, then the Linux/WSL Devin CLI TOML. A failed check is reported through JSON ok while the command still exits 0.")
	case "schema":
		fmt.Fprintln(w, "\nThe manifest is static and JSON-only. Target lookup accepts canonical command paths only.")
		fmt.Fprintln(w, "It does not register a native tool or read runtime configuration, credentials, project files, environment values, or network state.")
	}
}
