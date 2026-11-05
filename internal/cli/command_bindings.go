package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/deqiying/fast-context/internal/commandcontract"
)

type commandHandler func(context.Context, *commandcontract.Registry, ParsedCommand, io.Writer, io.Writer) int

var defaultCommandHandlers = map[string]commandHandler{
	"search": func(ctx context.Context, _ *commandcontract.Registry, parsed ParsedCommand, stdout, stderr io.Writer) int {
		return runParsedSearch(ctx, parsed, stdout, stderr)
	},
	"key.extract": func(_ context.Context, _ *commandcontract.Registry, parsed ParsedCommand, stdout, stderr io.Writer) int {
		return runParsedKeyExtract(parsed, stdout, stderr)
	},
	"doctor": func(_ context.Context, _ *commandcontract.Registry, parsed ParsedCommand, stdout, stderr io.Writer) int {
		return runParsedDoctor(parsed, stdout, stderr)
	},
	"mcp.serve": func(ctx context.Context, _ *commandcontract.Registry, _ ParsedCommand, stdout, stderr io.Writer) int {
		return runParsedMCPServe(ctx, stdout, stderr)
	},
	"skills.list": func(_ context.Context, _ *commandcontract.Registry, parsed ParsedCommand, stdout, stderr io.Writer) int {
		return runParsedSkillsList(parsed, stdout, stderr)
	},
	"skills.show": func(_ context.Context, _ *commandcontract.Registry, parsed ParsedCommand, stdout, stderr io.Writer) int {
		return runParsedSkillsShow(parsed, stdout, stderr)
	},
	"version": func(_ context.Context, _ *commandcontract.Registry, _ ParsedCommand, stdout, _ io.Writer) int {
		printVersion(stdout)
		return 0
	},
	"schema": func(_ context.Context, registry *commandcontract.Registry, parsed ParsedCommand, stdout, stderr io.Writer) int {
		return runSchema(registry, parsed, stdout, stderr)
	},
}

func init() {
	if err := validateCommandBindings(defaultCommandRegistry, defaultCommandHandlers); err != nil {
		panic(err)
	}
}

func validateCommandBindings(registry *commandcontract.Registry, handlers map[string]commandHandler) error {
	if registry == nil {
		return fmt.Errorf("nil command registry")
	}
	definitions := registry.Commands()
	if len(definitions) != len(handlers) {
		return fmt.Errorf("command binding count %d does not match public command count %d", len(handlers), len(definitions))
	}
	for _, definition := range definitions {
		if handlers[definition.ID] == nil {
			return fmt.Errorf("command %q has no handler binding", definition.ID)
		}
	}
	for id := range handlers {
		if _, ok := registry.LookupID(id); !ok {
			return fmt.Errorf("handler binding %q has no command definition", id)
		}
	}
	return nil
}
