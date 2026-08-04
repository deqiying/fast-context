package cli

import (
	"strings"

	"github.com/deqiying/fast-context/internal/commandcontract"
)

var defaultCommandRegistry = commandcontract.MustDefaultRegistry()

type commandRoute struct {
	Definition commandcontract.CommandDefinition
	Consumed   int
}

func resolveCommandRoute(registry *commandcontract.Registry, args []string) (commandRoute, bool) {
	definition, consumed, ok := registry.ResolveInvocation(args)
	if !ok {
		return commandRoute{}, false
	}
	return commandRoute{Definition: definition, Consumed: consumed}, true
}

func namespaceForInvocation(registry *commandcontract.Registry, args []string) (commandcontract.NamespaceDefinition, bool) {
	if len(args) == 0 {
		return commandcontract.NamespaceDefinition{}, false
	}
	return registry.LookupNamespace(args[0])
}

func hasCommandHelpToken(definition commandcontract.CommandDefinition, args []string) bool {
	flags := map[string]commandcontract.OptionDefinition{}
	for _, option := range definition.Options {
		flags[option.Flag] = option
		for _, alias := range option.Aliases {
			flags[alias] = option
		}
	}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			return false
		}
		if argument == "-h" || argument == "--help" {
			return true
		}
		if !strings.HasPrefix(argument, "-") {
			continue
		}
		name, inlineValue, hasValue := splitFlag(argument)
		option, ok := flags[name]
		if !ok {
			return false
		}
		if option.Type == commandcontract.TypeBoolean {
			if hasValue {
				if _, err := parseExplicitBoolean(inlineValue); err != nil {
					return false
				}
			}
			continue
		}
		rawValue := inlineValue
		if !hasValue {
			if index+1 >= len(args) {
				return false
			}
			index++
			rawValue = args[index]
		}
		if _, err := parseOptionValue(option, rawValue); err != nil {
			return false
		}
	}
	return false
}
