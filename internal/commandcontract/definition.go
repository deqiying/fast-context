package commandcontract

import (
	"sort"
	"strings"
)

const (
	ManifestKind    = "fast_context_cli_command_manifest"
	ManifestVersion = 1
)

type Category string

const (
	CategoryWorkflow Category = "workflow"
	CategoryUtility  Category = "utility"
)

type ValueType string

const (
	TypeString      ValueType = "string"
	TypeInteger     ValueType = "integer"
	TypeBoolean     ValueType = "boolean"
	TypeStringArray ValueType = "string_array"
)

type PositionalDefinition struct {
	Name                        string
	Type                        ValueType
	Description                 string
	Required                    bool
	Variadic                    bool
	JoinWithSingleSpace         bool
	MinLength                   int
	Pattern                     string
	MinItems                    int
	MaxItems                    int
	Lookup                      string
	EndOfOptionsWhenLeadingDash bool
}

type OptionDefinition struct {
	Name                   string
	Flag                   string
	Aliases                []string
	Type                   ValueType
	Description            string
	Default                any
	HasDefault             bool
	Enum                   []string
	Minimum                *int
	Maximum                *int
	MinLength              int
	Pattern                string
	Repeatable             bool
	Clamp                  bool
	TrimSpaceThenLowercase bool
	EnvironmentBacked      bool
	DefaultSource          map[string]any
	Compatibility          map[string]any
	Serialize              string
	ArgvDecoder            string
	ParsedValue            string
}

type PathMatchingDefinition struct {
	CaseInsensitiveSegments []int `json:"case_insensitive_segments,omitempty"`
}

type AvailabilityInputBinding struct {
	Target         string `json:"target"`
	Source         string `json:"source,omitempty"`
	Constant       any    `json:"constant,omitempty"`
	OmitWhenAbsent bool   `json:"omit_when_absent,omitempty"`
}

type AvailabilityDefinition struct {
	Dynamic        bool                       `json:"dynamic"`
	CheckCommand   []string                   `json:"check_command,omitempty"`
	InputBindings  []AvailabilityInputBinding `json:"input_bindings,omitempty"`
	JSONPointer    string                     `json:"json_pointer,omitempty"`
	PreflightLevel string                     `json:"preflight_level,omitempty"`
	DoesNotProve   []string                   `json:"does_not_prove,omitempty"`
}

type ExitCodeDefinition struct {
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}

type OutputDefinition struct {
	DefaultFormat string               `json:"default_format"`
	Formats       []string             `json:"formats"`
	Variants      []string             `json:"variants,omitempty"`
	Contract      string               `json:"contract"`
	Redaction     string               `json:"redaction,omitempty"`
	ExitCodes     []ExitCodeDefinition `json:"exit_codes"`
}

type CommandDefinition struct {
	ID           string
	Path         []string
	Aliases      [][]string
	Category     Category
	Summary      string
	Capabilities []string
	PathMatching PathMatchingDefinition
	Positionals  []PositionalDefinition
	Options      []OptionDefinition
	Availability AvailabilityDefinition
	SideEffects  []string
	Output       OutputDefinition
}

type NamespaceDefinition struct {
	Path     []string
	Category Category
	Summary  string
}

type CLIInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ManifestScope struct {
	Mode string   `json:"mode"`
	Path []string `json:"path"`
}

type ArgvConventions struct {
	Ordering                    []string `json:"ordering"`
	ScalarFlagValue             string   `json:"scalar_flag_value"`
	LeadingDashOrEmptyFlagValue string   `json:"leading_dash_or_empty_flag_value"`
	RepeatableArray             string   `json:"repeatable_array"`
	EnvironmentBooleanFalse     string   `json:"environment_boolean_false"`
}

type Manifest struct {
	OK              bool              `json:"ok"`
	Kind            string            `json:"kind"`
	ManifestVersion int               `json:"manifest_version"`
	CLI             CLIInfo           `json:"cli"`
	Scope           ManifestScope     `json:"scope"`
	ArgvConventions ArgvConventions   `json:"argv_conventions"`
	Commands        []ManifestCommand `json:"commands"`
}

type ManifestCommand struct {
	ID           string                  `json:"id"`
	Path         []string                `json:"path"`
	Category     Category                `json:"category"`
	Summary      string                  `json:"summary"`
	Aliases      [][]string              `json:"aliases"`
	Capabilities []string                `json:"capabilities"`
	PathMatching *PathMatchingDefinition `json:"path_matching,omitempty"`
	InputSchema  map[string]any          `json:"input_schema"`
	Availability AvailabilityDefinition  `json:"availability"`
	SideEffects  []string                `json:"side_effects"`
	Output       OutputDefinition        `json:"output"`
}

func DefaultArgvConventions() ArgvConventions {
	return ArgvConventions{
		Ordering:                    []string{"command_path", "options", "end_of_options_when_needed", "positionals"},
		ScalarFlagValue:             "separate_token",
		LeadingDashOrEmptyFlagValue: "inline_equals",
		RepeatableArray:             "repeat_flag_in_input_order",
		EnvironmentBooleanFalse:     "explicit_equals",
	}
}

func (d CommandDefinition) Manifest() ManifestCommand {
	aliases := copyPaths(d.Aliases)
	sort.Slice(aliases, func(i, j int) bool { return pathKey(aliases[i]) < pathKey(aliases[j]) })
	capabilities := sortedStrings(d.Capabilities)
	sideEffects := sortedStrings(d.SideEffects)
	availability := cloneAvailability(d.Availability)
	sort.Strings(availability.DoesNotProve)
	output := cloneOutput(d.Output)
	sort.Strings(output.Formats)
	sort.Strings(output.Variants)
	sort.Slice(output.ExitCodes, func(i, j int) bool { return output.ExitCodes[i].Code < output.ExitCodes[j].Code })
	return ManifestCommand{
		ID:           d.ID,
		Path:         append([]string{}, d.Path...),
		Category:     d.Category,
		Summary:      d.Summary,
		Aliases:      aliases,
		Capabilities: capabilities,
		PathMatching: manifestPathMatching(d.PathMatching),
		InputSchema:  d.InputSchema(),
		Availability: availability,
		SideEffects:  sideEffects,
		Output:       output,
	}
}

func (d CommandDefinition) InputSchema() map[string]any {
	properties := map[string]any{}
	required := []string{}
	for index, positional := range d.Positionals {
		binding := map[string]any{
			"kind":  "positional",
			"index": index,
		}
		if positional.Variadic {
			binding["variadic"] = true
		}
		if positional.Lookup != "" {
			binding["lookup"] = positional.Lookup
		}
		if positional.EndOfOptionsWhenLeadingDash {
			binding["end_of_options"] = "when_value_starts_with_dash"
		}
		property := map[string]any{
			"description":   positional.Description,
			"x-cli-binding": binding,
		}
		if positional.Variadic {
			items := map[string]any{"type": jsonType(positional.Type)}
			if positional.MinLength > 0 {
				items["minLength"] = positional.MinLength
			}
			if positional.Pattern != "" {
				items["pattern"] = positional.Pattern
			}
			property["type"] = "array"
			property["items"] = items
			if positional.MinItems > 0 {
				property["minItems"] = positional.MinItems
			}
			if positional.MaxItems > 0 {
				property["maxItems"] = positional.MaxItems
			}
		} else {
			property["type"] = jsonType(positional.Type)
			if positional.MinLength > 0 {
				property["minLength"] = positional.MinLength
			}
			if positional.Pattern != "" {
				property["pattern"] = positional.Pattern
			}
		}
		if positional.JoinWithSingleSpace {
			property["x-cli-compatibility"] = map[string]any{"additional_positionals": "join_with_single_space"}
		}
		properties[positional.Name] = property
		if positional.Required {
			required = append(required, positional.Name)
		}
	}
	for optionIndex, option := range d.Options {
		binding := map[string]any{
			"kind":  "flag",
			"order": optionIndex,
			"token": option.Flag,
		}
		if len(option.Aliases) > 0 {
			binding["aliases"] = append([]string{}, option.Aliases...)
		}
		if option.Repeatable {
			binding["repeatable"] = true
		}
		if option.Serialize != "" {
			binding["serialize"] = option.Serialize
		}
		if option.ArgvDecoder != "" {
			binding["argv_decoder"] = option.ArgvDecoder
		}
		if option.ParsedValue != "" {
			binding["parsed_value"] = option.ParsedValue
		}
		if option.Type == TypeBoolean {
			binding["true"] = "presence_flag"
			binding["false"] = "explicit_boolean_value"
			if option.EnvironmentBacked {
				binding["absent"] = "inherit_runtime"
			}
		}
		property := map[string]any{
			"type":          jsonType(option.Type),
			"description":   option.Description,
			"x-cli-binding": binding,
		}
		if option.Type == TypeStringArray {
			items := map[string]any{"type": "string"}
			if option.MinLength > 0 {
				items["minLength"] = option.MinLength
			}
			if option.Pattern != "" {
				items["pattern"] = option.Pattern
			}
			property["type"] = "array"
			property["items"] = items
		}
		if option.HasDefault {
			property["default"] = cloneAny(option.Default)
		}
		if len(option.Enum) > 0 {
			property["enum"] = append([]string{}, option.Enum...)
		}
		if option.Minimum != nil {
			property["minimum"] = *option.Minimum
		}
		if option.Maximum != nil {
			property["maximum"] = *option.Maximum
		}
		if option.Type != TypeStringArray && option.MinLength > 0 {
			property["minLength"] = option.MinLength
		}
		if option.Type != TypeStringArray && option.Pattern != "" {
			property["pattern"] = option.Pattern
		}
		if len(option.DefaultSource) > 0 {
			property["x-cli-default-source"] = cloneMap(option.DefaultSource)
		}
		normalization := map[string]any{}
		if option.Clamp && option.Minimum != nil && option.Maximum != nil {
			normalization["kind"] = "clamp"
			normalization["minimum"] = *option.Minimum
			normalization["maximum"] = *option.Maximum
		}
		if option.TrimSpaceThenLowercase {
			normalization["kind"] = "trim_space_then_lowercase"
		}
		if len(normalization) > 0 {
			property["x-cli-normalization"] = normalization
		}
		if len(option.Compatibility) > 0 {
			property["x-cli-compatibility"] = cloneMap(option.Compatibility)
		}
		properties[option.Name] = property
	}
	sort.Strings(required)
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func jsonType(value ValueType) string {
	switch value {
	case TypeInteger:
		return "integer"
	case TypeBoolean:
		return "boolean"
	default:
		return "string"
	}
}

func sortedStrings(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return out
}

func manifestPathMatching(value PathMatchingDefinition) *PathMatchingDefinition {
	if len(value.CaseInsensitiveSegments) == 0 {
		return nil
	}
	cloned := clonePathMatching(value)
	return &cloned
}

func pathKey(path []string) string {
	return strings.Join(path, "\x00")
}

func displayPath(path []string) string {
	return strings.Join(path, " ")
}

func copyPaths(paths [][]string) [][]string {
	out := make([][]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, append([]string{}, path...))
	}
	return out
}
