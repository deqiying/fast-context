package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/deqiying/fast-context/internal/commandcontract"
	"github.com/deqiying/fast-context/internal/config"
)

type ParsedCommand struct {
	Definition commandcontract.CommandDefinition
	Values     map[string]any
	Present    map[string]bool
}

func parseCommand(definition commandcontract.CommandDefinition, args []string) (ParsedCommand, error) {
	parsed := ParsedCommand{
		Definition: definition,
		Values:     map[string]any{},
		Present:    map[string]bool{},
	}
	for _, option := range definition.Options {
		if option.HasDefault {
			parsed.Values[option.Name] = option.Default
		}
	}

	flags := map[string]commandcontract.OptionDefinition{}
	for _, option := range definition.Options {
		flags[option.Flag] = option
		for _, alias := range option.Aliases {
			flags[alias] = option
		}
	}

	positionals := []string{}
	endOfOptions := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !endOfOptions && argument == "--" {
			endOfOptions = true
			continue
		}
		if endOfOptions || !strings.HasPrefix(argument, "-") || argument == "-" {
			positionals = append(positionals, argument)
			continue
		}

		name, rawValue, hasValue := splitFlag(argument)
		option, ok := flags[name]
		if !ok {
			return ParsedCommand{}, fmt.Errorf("unknown flag: %s", name)
		}
		if option.Type == commandcontract.TypeBoolean {
			value := true
			if hasValue {
				parsedValue, err := parseExplicitBoolean(rawValue)
				if err != nil {
					return ParsedCommand{}, fmt.Errorf("invalid %s: expected true or false", option.Flag)
				}
				value = parsedValue
			}
			parsed.Values[option.Name] = value
			parsed.Present[option.Name] = true
			continue
		}
		if !hasValue {
			if index+1 >= len(args) {
				return ParsedCommand{}, fmt.Errorf("missing value for %s", option.Flag)
			}
			index++
			rawValue = args[index]
		}
		value, err := parseOptionValue(option, rawValue)
		if err != nil {
			return ParsedCommand{}, err
		}
		if option.Repeatable {
			values, _ := parsed.Values[option.Name].([]string)
			parsed.Values[option.Name] = append(values, value.(string))
		} else {
			parsed.Values[option.Name] = value
		}
		parsed.Present[option.Name] = true
	}

	if err := bindPositionals(&parsed, positionals); err != nil {
		return ParsedCommand{}, err
	}
	return parsed, nil
}

func parseOptionValue(option commandcontract.OptionDefinition, rawValue string) (any, error) {
	switch option.Type {
	case commandcontract.TypeString, commandcontract.TypeStringArray:
		value := rawValue
		if option.TrimSpaceThenLowercase {
			value = strings.ToLower(strings.TrimSpace(value))
		}
		if option.MinLength > 0 && len(value) < option.MinLength {
			return nil, fmt.Errorf("invalid %s", option.Flag)
		}
		if option.Pattern != "" {
			matched, err := regexp.MatchString(option.Pattern, value)
			if err != nil || !matched {
				return nil, fmt.Errorf("invalid %s", option.Flag)
			}
		}
		if len(option.Enum) > 0 && !containsString(option.Enum, value) {
			return nil, fmt.Errorf("%s must be %s", option.Flag, formatEnum(option.Enum))
		}
		return value, nil
	case commandcontract.TypeInteger:
		if option.ArgvDecoder == "go_duration_or_integer_milliseconds" {
			value, err := parseDuration(rawValue)
			if err != nil {
				return nil, fmt.Errorf("invalid %s", option.Flag)
			}
			return value, nil
		}
		value, err := strconv.Atoi(rawValue)
		if err != nil {
			return nil, fmt.Errorf("invalid %s", option.Flag)
		}
		if option.Clamp && option.Minimum != nil && option.Maximum != nil {
			value = config.ClampInt(value, *option.Minimum, *option.Maximum)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported option type for %s", option.Flag)
	}
}

func bindPositionals(parsed *ParsedCommand, values []string) error {
	cursor := 0
	for _, positional := range parsed.Definition.Positionals {
		if positional.JoinWithSingleSpace {
			value := strings.TrimSpace(strings.Join(values[cursor:], " "))
			if err := validatePositionalValue(positional, value); err != nil {
				return err
			}
			if value != "" || positional.Required {
				parsed.Values[positional.Name] = value
			}
			if cursor < len(values) {
				parsed.Present[positional.Name] = true
			}
			cursor = len(values)
			continue
		}
		if positional.Variadic {
			remaining := append([]string{}, values[cursor:]...)
			if positional.Required && len(remaining) == 0 {
				return fmt.Errorf("missing %s", positional.Name)
			}
			if positional.MinItems > 0 && len(remaining) < positional.MinItems {
				return fmt.Errorf("%s requires at least %d value(s)", positional.Name, positional.MinItems)
			}
			if positional.MaxItems > 0 && len(remaining) > positional.MaxItems {
				return fmt.Errorf("%s accepts at most %d value(s)", positional.Name, positional.MaxItems)
			}
			for _, value := range remaining {
				if err := validatePositionalValue(positional, value); err != nil {
					return err
				}
			}
			parsed.Values[positional.Name] = remaining
			if len(remaining) > 0 {
				parsed.Present[positional.Name] = true
			}
			cursor = len(values)
			continue
		}
		if cursor >= len(values) {
			if positional.Required {
				return fmt.Errorf("missing %s", positional.Name)
			}
			continue
		}
		value := values[cursor]
		if err := validatePositionalValue(positional, value); err != nil {
			return err
		}
		parsed.Values[positional.Name] = value
		parsed.Present[positional.Name] = true
		cursor++
	}
	if cursor < len(values) {
		return fmt.Errorf("unexpected positional argument: %s", values[cursor])
	}
	return nil
}

func validatePositionalValue(definition commandcontract.PositionalDefinition, value string) error {
	if definition.Required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("missing %s", definition.Name)
	}
	if definition.MinLength > 0 && len(value) < definition.MinLength {
		return fmt.Errorf("invalid %s", definition.Name)
	}
	if definition.Pattern != "" {
		matched, err := regexp.MatchString(definition.Pattern, value)
		if err != nil || !matched {
			return fmt.Errorf("invalid %s", definition.Name)
		}
	}
	return nil
}

func parseExplicitBoolean(rawValue string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(rawValue)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false")
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func formatEnum(values []string) string {
	if len(values) == 0 {
		return "a valid value"
	}
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}

func (p ParsedCommand) String(name string) string {
	value, _ := p.Values[name].(string)
	return value
}

func (p ParsedCommand) Int(name string) int {
	value, _ := p.Values[name].(int)
	return value
}

func (p ParsedCommand) Bool(name string) bool {
	value, _ := p.Values[name].(bool)
	return value
}

func (p ParsedCommand) Strings(name string) []string {
	value, _ := p.Values[name].([]string)
	return append([]string{}, value...)
}

func (p ParsedCommand) Duration(name string) time.Duration {
	value, _ := p.Values[name].(time.Duration)
	return value
}
