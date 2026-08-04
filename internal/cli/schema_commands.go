package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/deqiying/fast-context/internal/commandcontract"
	"github.com/deqiying/fast-context/internal/version"
)

type schemaErrorEnvelope struct {
	OK             bool              `json:"ok"`
	Error          schemaErrorDetail `json:"error"`
	AvailablePaths []string          `json:"available_paths"`
}

type schemaErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func runSchema(registry *commandcontract.Registry, parsed ParsedCommand, stdout, stderr io.Writer) int {
	pretty := parsed.Bool("pretty")
	manifest, err := BuildManifest(registry, version.Version, parsed.Strings("command_path"))
	if err != nil {
		return writeSchemaArgumentError(registry, stdout, stderr, err.Error(), pretty)
	}
	if err := encodeSchemaJSON(stdout, manifest, pretty); err != nil {
		fmt.Fprintf(stderr, "Error: failed to write schema: %s\n", err)
		return 1
	}
	return 0
}

func BuildManifest(registry *commandcontract.Registry, cliVersion string, path []string) (commandcontract.Manifest, error) {
	if registry == nil {
		return commandcontract.Manifest{}, fmt.Errorf("command registry is unavailable")
	}
	definitions := registry.Commands()
	scope := commandcontract.ManifestScope{Mode: "all", Path: []string{}}
	if len(path) > 0 {
		definition, ok := registry.LookupCanonical(path...)
		if !ok {
			return commandcontract.Manifest{}, fmt.Errorf("unknown canonical command path")
		}
		definitions = []commandcontract.CommandDefinition{definition}
		scope = commandcontract.ManifestScope{Mode: "command", Path: append([]string{}, path...)}
	}
	sort.Slice(definitions, func(i, j int) bool {
		leftRank := commandcontract.CategoryRank(definitions[i].Category)
		rightRank := commandcontract.CategoryRank(definitions[j].Category)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return strings.Join(definitions[i].Path, "\x00") < strings.Join(definitions[j].Path, "\x00")
	})
	commands := make([]commandcontract.ManifestCommand, 0, len(definitions))
	for _, definition := range definitions {
		commands = append(commands, definition.Manifest())
	}
	return commandcontract.Manifest{
		OK:              true,
		Kind:            commandcontract.ManifestKind,
		ManifestVersion: commandcontract.ManifestVersion,
		CLI:             commandcontract.CLIInfo{Name: "fast-context", Version: cliVersion},
		Scope:           scope,
		ArgvConventions: commandcontract.DefaultArgvConventions(),
		Commands:        commands,
	}, nil
}

func writeSchemaArgumentError(registry *commandcontract.Registry, stdout, stderr io.Writer, message string, pretty bool) int {
	message = sanitizeSchemaArgumentError(message)
	envelope := schemaErrorEnvelope{
		OK: false,
		Error: schemaErrorDetail{
			Code:    "INVALID_ARGUMENT",
			Message: message,
		},
		AvailablePaths: availableCanonicalPaths(registry),
	}
	if err := encodeSchemaJSON(stdout, envelope, pretty); err != nil {
		fmt.Fprintf(stderr, "Error: failed to write schema error: %s\n", err)
		return 1
	}
	return 2
}

func sanitizeSchemaArgumentError(message string) string {
	if strings.HasPrefix(message, "unknown flag:") {
		return "unknown flag"
	}
	if strings.HasPrefix(message, "unknown canonical command path") {
		return "unknown canonical command path"
	}
	return message
}

func availableCanonicalPaths(registry *commandcontract.Registry) []string {
	if registry == nil {
		return []string{}
	}
	paths := make([]string, 0, len(registry.Commands()))
	for _, definition := range registry.Commands() {
		paths = append(paths, strings.Join(definition.Path, " "))
	}
	sort.Strings(paths)
	return paths
}

func encodeSchemaJSON(w io.Writer, value any, pretty bool) error {
	encoder := newJSONEncoder(w, pretty)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
