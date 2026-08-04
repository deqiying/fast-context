package commandcontract

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Registry is an immutable, validated index of the public CLI contract.
type Registry struct {
	commands   []CommandDefinition
	namespaces []NamespaceDefinition
	byID       map[string]int
	byPath     map[string]int
	byAlias    map[string]int
}

func NewRegistry(commands []CommandDefinition, namespaces []NamespaceDefinition) (*Registry, error) {
	if err := ValidateDefinitions(commands, namespaces); err != nil {
		return nil, err
	}
	registry := &Registry{
		commands:   cloneCommands(commands),
		namespaces: cloneNamespaces(namespaces),
		byID:       map[string]int{},
		byPath:     map[string]int{},
		byAlias:    map[string]int{},
	}
	sort.Slice(registry.commands, func(i, j int) bool {
		return pathKey(registry.commands[i].Path) < pathKey(registry.commands[j].Path)
	})
	sort.Slice(registry.namespaces, func(i, j int) bool {
		return pathKey(registry.namespaces[i].Path) < pathKey(registry.namespaces[j].Path)
	})
	for index := range registry.commands {
		definition := registry.commands[index]
		registry.byID[definition.ID] = index
		registry.byPath[pathKey(definition.Path)] = index
		for _, alias := range definition.Aliases {
			registry.byAlias[pathKey(alias)] = index
		}
	}
	return registry, nil
}

func DefaultRegistry() (*Registry, error) {
	return NewRegistry(commandDefinitions(), namespaceDefinitions())
}

func MustDefaultRegistry() *Registry {
	registry, err := DefaultRegistry()
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Commands() []CommandDefinition {
	if r == nil {
		return nil
	}
	return cloneCommands(r.commands)
}

func (r *Registry) Namespaces() []NamespaceDefinition {
	if r == nil {
		return nil
	}
	return cloneNamespaces(r.namespaces)
}

func (r *Registry) LookupID(id string) (CommandDefinition, bool) {
	if r == nil {
		return CommandDefinition{}, false
	}
	index, ok := r.byID[id]
	if !ok {
		return CommandDefinition{}, false
	}
	return cloneCommand(r.commands[index]), true
}

func (r *Registry) LookupCanonical(path ...string) (CommandDefinition, bool) {
	if r == nil {
		return CommandDefinition{}, false
	}
	index, ok := r.byPath[pathKey(path)]
	if !ok {
		return CommandDefinition{}, false
	}
	return cloneCommand(r.commands[index]), true
}

func (r *Registry) LookupAlias(path ...string) (CommandDefinition, bool) {
	if r == nil {
		return CommandDefinition{}, false
	}
	index, ok := r.byAlias[pathKey(path)]
	if !ok {
		return CommandDefinition{}, false
	}
	return cloneCommand(r.commands[index]), true
}

// ResolveInvocation returns the longest canonical or compatibility-alias path
// that prefixes args. Only segments explicitly marked in PathMatching may be
// matched case-insensitively.
func (r *Registry) ResolveInvocation(args []string) (CommandDefinition, int, bool) {
	if r == nil {
		return CommandDefinition{}, 0, false
	}
	bestIndex := -1
	bestLength := 0
	for index, definition := range r.commands {
		paths := append([][]string{definition.Path}, definition.Aliases...)
		for _, path := range paths {
			if len(path) <= bestLength || len(path) > len(args) {
				continue
			}
			if pathMatches(args[:len(path)], path, definition.PathMatching.CaseInsensitiveSegments) {
				bestIndex = index
				bestLength = len(path)
			}
		}
	}
	if bestIndex < 0 {
		return CommandDefinition{}, 0, false
	}
	return cloneCommand(r.commands[bestIndex]), bestLength, true
}

func (r *Registry) LookupNamespace(path ...string) (NamespaceDefinition, bool) {
	if r == nil {
		return NamespaceDefinition{}, false
	}
	for _, namespace := range r.namespaces {
		if pathKey(namespace.Path) == pathKey(path) {
			return cloneNamespace(namespace), true
		}
	}
	return NamespaceDefinition{}, false
}

func (r *Registry) Validate() error {
	if r == nil {
		return fmt.Errorf("nil registry")
	}
	return ValidateDefinitions(r.commands, r.namespaces)
}

func ValidateDefinitions(commands []CommandDefinition, namespaces []NamespaceDefinition) error {
	ids := map[string]bool{}
	paths := map[string]string{}
	for index, command := range commands {
		if err := validateCommand(command); err != nil {
			return fmt.Errorf("command[%d]: %w", index, err)
		}
		if ids[command.ID] {
			return fmt.Errorf("duplicate command id %q", command.ID)
		}
		ids[command.ID] = true
		key := pathKey(command.Path)
		if previous, ok := paths[key]; ok {
			return fmt.Errorf("command path %q conflicts with %q", displayPath(command.Path), previous)
		}
		paths[key] = command.ID
	}
	for index, namespace := range namespaces {
		if err := validateNamespace(namespace); err != nil {
			return fmt.Errorf("namespace[%d]: %w", index, err)
		}
		key := pathKey(namespace.Path)
		if previous, ok := paths[key]; ok {
			return fmt.Errorf("namespace path %q conflicts with %q", displayPath(namespace.Path), previous)
		}
		paths[key] = "namespace"
	}
	for _, command := range commands {
		seenAliases := map[string]bool{}
		for _, alias := range command.Aliases {
			if err := validatePath(alias); err != nil {
				return fmt.Errorf("command %q alias: %w", command.ID, err)
			}
			key := pathKey(alias)
			if key == pathKey(command.Path) || seenAliases[key] {
				return fmt.Errorf("command %q has duplicate alias %q", command.ID, displayPath(alias))
			}
			if previous, ok := paths[key]; ok {
				return fmt.Errorf("command %q alias %q conflicts with %q", command.ID, displayPath(alias), previous)
			}
			for _, segment := range command.PathMatching.CaseInsensitiveSegments {
				if segment >= len(alias) {
					return fmt.Errorf("command %q alias %q is shorter than case-insensitive segment %d", command.ID, displayPath(alias), segment)
				}
			}
			seenAliases[key] = true
			paths[key] = command.ID
		}
	}
	if err := validateInvocationDomains(commands, namespaces); err != nil {
		return err
	}
	byPath := map[string]CommandDefinition{}
	for _, command := range commands {
		byPath[pathKey(command.Path)] = command
	}
	for _, command := range commands {
		if err := validateAvailability(command, byPath); err != nil {
			return fmt.Errorf("command %q availability: %w", command.ID, err)
		}
	}
	return nil
}

type invocationDomain struct {
	owner                   string
	path                    []string
	caseInsensitiveSegments []int
}

func validateInvocationDomains(commands []CommandDefinition, namespaces []NamespaceDefinition) error {
	domains := []invocationDomain{}
	for _, command := range commands {
		domains = append(domains, invocationDomain{
			owner:                   command.ID + " canonical path",
			path:                    command.Path,
			caseInsensitiveSegments: command.PathMatching.CaseInsensitiveSegments,
		})
		for _, alias := range command.Aliases {
			domains = append(domains, invocationDomain{
				owner:                   command.ID + " alias " + displayPath(alias),
				path:                    alias,
				caseInsensitiveSegments: command.PathMatching.CaseInsensitiveSegments,
			})
		}
	}
	for _, namespace := range namespaces {
		domains = append(domains, invocationDomain{owner: "namespace " + displayPath(namespace.Path), path: namespace.Path})
	}
	for left := 0; left < len(domains); left++ {
		for right := left + 1; right < len(domains); right++ {
			if invocationDomainsOverlap(domains[left], domains[right]) {
				return fmt.Errorf("invocation domains %q and %q are ambiguous", domains[left].owner, domains[right].owner)
			}
		}
	}
	return nil
}

func invocationDomainsOverlap(left, right invocationDomain) bool {
	if len(left.path) != len(right.path) {
		return false
	}
	leftInsensitive := indexSet(left.caseInsensitiveSegments)
	rightInsensitive := indexSet(right.caseInsensitiveSegments)
	for index := range left.path {
		if left.path[index] == right.path[index] {
			continue
		}
		if !strings.EqualFold(left.path[index], right.path[index]) || (!leftInsensitive[index] && !rightInsensitive[index]) {
			return false
		}
	}
	return true
}

func indexSet(values []int) map[int]bool {
	out := make(map[int]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func validateCommand(command CommandDefinition) error {
	if strings.TrimSpace(command.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if err := validatePath(command.Path); err != nil {
		return err
	}
	if command.Category != CategoryWorkflow && command.Category != CategoryUtility {
		return fmt.Errorf("invalid category %q", command.Category)
	}
	if strings.TrimSpace(command.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	caseInsensitive := map[int]bool{}
	for _, segment := range command.PathMatching.CaseInsensitiveSegments {
		if segment < 0 || segment >= len(command.Path) || caseInsensitive[segment] {
			return fmt.Errorf("invalid case-insensitive path segment %d", segment)
		}
		caseInsensitive[segment] = true
	}
	properties := map[string]bool{}
	optionalSeen := false
	for index, positional := range command.Positionals {
		if strings.TrimSpace(positional.Name) == "" || properties[positional.Name] {
			return fmt.Errorf("positional name must be non-empty and unique")
		}
		if err := validateValueType(positional.Type); err != nil {
			return fmt.Errorf("positional %q: %w", positional.Name, err)
		}
		if positional.Type != TypeString {
			return fmt.Errorf("positional %q must use string type", positional.Name)
		}
		if optionalSeen && positional.Required {
			return fmt.Errorf("required positional %q follows optional positional", positional.Name)
		}
		if !positional.Required {
			optionalSeen = true
		}
		if (positional.Variadic || positional.JoinWithSingleSpace) && index != len(command.Positionals)-1 {
			return fmt.Errorf("variadic or joined positional %q must be last", positional.Name)
		}
		if positional.Variadic && positional.JoinWithSingleSpace {
			return fmt.Errorf("positional %q cannot be variadic and joined", positional.Name)
		}
		if positional.MinLength < 0 || positional.MinItems < 0 || positional.MaxItems < 0 || (positional.MaxItems > 0 && positional.MinItems > positional.MaxItems) {
			return fmt.Errorf("positional %q has invalid bounds", positional.Name)
		}
		if positional.Pattern != "" {
			if _, err := regexp.Compile(positional.Pattern); err != nil {
				return fmt.Errorf("positional %q has invalid pattern: %w", positional.Name, err)
			}
		}
		properties[positional.Name] = true
	}
	flags := map[string]bool{}
	for _, option := range command.Options {
		if strings.TrimSpace(option.Name) == "" || properties[option.Name] {
			return fmt.Errorf("option property %q must be non-empty and unique", option.Name)
		}
		if !strings.HasPrefix(option.Flag, "--") || option.Flag == "--" || strings.ContainsAny(option.Flag, " =\t\r\n") {
			return fmt.Errorf("option %q has invalid canonical flag %q", option.Name, option.Flag)
		}
		if flags[option.Flag] {
			return fmt.Errorf("duplicate flag %q", option.Flag)
		}
		flags[option.Flag] = true
		for _, alias := range option.Aliases {
			if !strings.HasPrefix(alias, "-") || alias == "-" || flags[alias] {
				return fmt.Errorf("option %q has invalid or duplicate alias %q", option.Name, alias)
			}
			flags[alias] = true
		}
		if err := validateValueType(option.Type); err != nil {
			return fmt.Errorf("option %q: %w", option.Name, err)
		}
		if option.Minimum != nil && option.Maximum != nil && *option.Minimum > *option.Maximum {
			return fmt.Errorf("option %q minimum exceeds maximum", option.Name)
		}
		if (option.Minimum != nil || option.Maximum != nil) && option.Type != TypeInteger {
			return fmt.Errorf("option %q numeric bounds require integer type", option.Name)
		}
		if len(option.Enum) > 0 && option.Type != TypeString {
			return fmt.Errorf("option %q enum requires string type", option.Name)
		}
		if option.Repeatable && option.Type != TypeStringArray {
			return fmt.Errorf("option %q repeatable requires string_array type", option.Name)
		}
		if option.Clamp && (option.Type != TypeInteger || option.Minimum == nil || option.Maximum == nil) {
			return fmt.Errorf("option %q clamp requires integer bounds", option.Name)
		}
		if option.TrimSpaceThenLowercase && option.Type != TypeString {
			return fmt.Errorf("option %q trim/lower normalization requires string type", option.Name)
		}
		if option.MinLength < 0 || (option.MinLength > 0 && option.Type != TypeString && option.Type != TypeStringArray) {
			return fmt.Errorf("option %q has invalid min length", option.Name)
		}
		if option.Pattern != "" {
			if option.Type != TypeString && option.Type != TypeStringArray {
				return fmt.Errorf("option %q pattern requires string type", option.Name)
			}
			if _, err := regexp.Compile(option.Pattern); err != nil {
				return fmt.Errorf("option %q has invalid pattern: %w", option.Name, err)
			}
		}
		if option.EnvironmentBacked && option.Type != TypeBoolean {
			return fmt.Errorf("option %q environment-backed tri-state requires boolean type", option.Name)
		}
		if option.HasDefault {
			if err := validateValue(option.Type, option.Default); err != nil {
				return fmt.Errorf("option %q default: %w", option.Name, err)
			}
			if len(option.Enum) > 0 && !stringIn(option.Default.(string), option.Enum) {
				return fmt.Errorf("option %q default is outside enum", option.Name)
			}
		}
		properties[option.Name] = true
	}
	if strings.TrimSpace(command.Output.DefaultFormat) == "" || strings.TrimSpace(command.Output.Contract) == "" {
		return fmt.Errorf("output default format and contract are required")
	}
	if !stringIn(command.Output.DefaultFormat, command.Output.Formats) {
		return fmt.Errorf("output default format %q is not declared", command.Output.DefaultFormat)
	}
	exitCodes := map[int]bool{}
	for _, exitCode := range command.Output.ExitCodes {
		if exitCodes[exitCode.Code] || strings.TrimSpace(exitCode.Meaning) == "" {
			return fmt.Errorf("output exit codes must be unique and described")
		}
		exitCodes[exitCode.Code] = true
	}
	return nil
}

func validateNamespace(namespace NamespaceDefinition) error {
	if err := validatePath(namespace.Path); err != nil {
		return err
	}
	if namespace.Category != CategoryWorkflow && namespace.Category != CategoryUtility {
		return fmt.Errorf("invalid category %q", namespace.Category)
	}
	if strings.TrimSpace(namespace.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	return nil
}

func validateAvailability(command CommandDefinition, byPath map[string]CommandDefinition) error {
	availability := command.Availability
	if !availability.Dynamic {
		if len(availability.CheckCommand) > 0 || availability.JSONPointer != "" || len(availability.InputBindings) > 0 {
			return fmt.Errorf("static availability cannot declare a dynamic check")
		}
		return nil
	}
	if len(availability.CheckCommand) == 0 || availability.JSONPointer == "" {
		return fmt.Errorf("dynamic availability requires check_command and json_pointer")
	}
	check, ok := byPath[pathKey(availability.CheckCommand)]
	if !ok {
		return fmt.Errorf("check command %q is not canonical", displayPath(availability.CheckCommand))
	}
	sourceProperties := definitionProperties(command)
	targetProperties := definitionProperties(check)
	for _, binding := range availability.InputBindings {
		if !targetProperties[binding.Target] {
			return fmt.Errorf("binding target %q does not exist on %q", binding.Target, check.ID)
		}
		if binding.Source != "" && !sourceProperties[binding.Source] {
			return fmt.Errorf("binding source %q does not exist on %q", binding.Source, command.ID)
		}
		if binding.Source == "" && binding.Constant == nil {
			return fmt.Errorf("binding target %q requires source or constant", binding.Target)
		}
		if binding.Constant != nil {
			if option, ok := optionByName(check, binding.Target); ok {
				if err := validateValue(option.Type, binding.Constant); err != nil {
					return fmt.Errorf("binding target %q constant: %w", binding.Target, err)
				}
				if len(option.Enum) > 0 && !stringIn(binding.Constant.(string), option.Enum) {
					return fmt.Errorf("binding target %q constant is outside enum", binding.Target)
				}
			}
		}
	}
	return nil
}

func definitionProperties(command CommandDefinition) map[string]bool {
	properties := map[string]bool{}
	for _, positional := range command.Positionals {
		properties[positional.Name] = true
	}
	for _, option := range command.Options {
		properties[option.Name] = true
	}
	return properties
}

func optionByName(command CommandDefinition, name string) (OptionDefinition, bool) {
	for _, option := range command.Options {
		if option.Name == name {
			return option, true
		}
	}
	return OptionDefinition{}, false
}

func validatePath(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("path is required")
	}
	for _, segment := range path {
		if strings.TrimSpace(segment) == "" || segment == "." || segment == ".." {
			return fmt.Errorf("path contains invalid segment")
		}
	}
	return nil
}

func validateValueType(value ValueType) error {
	switch value {
	case TypeString, TypeInteger, TypeBoolean, TypeStringArray:
		return nil
	default:
		return fmt.Errorf("unsupported value type %q", value)
	}
}

func validateValue(valueType ValueType, value any) error {
	switch valueType {
	case TypeString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("want string, got %T", value)
		}
	case TypeInteger:
		if _, ok := value.(int); !ok {
			return fmt.Errorf("want integer, got %T", value)
		}
	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("want boolean, got %T", value)
		}
	case TypeStringArray:
		if _, ok := value.([]string); !ok {
			return fmt.Errorf("want string array, got %T", value)
		}
	default:
		return fmt.Errorf("unsupported value type %q", valueType)
	}
	return nil
}

func pathMatches(input, candidate []string, caseInsensitiveSegments []int) bool {
	if len(input) != len(candidate) {
		return false
	}
	insensitive := map[int]bool{}
	for _, index := range caseInsensitiveSegments {
		insensitive[index] = true
	}
	for index := range input {
		if insensitive[index] {
			if !strings.EqualFold(input[index], candidate[index]) {
				return false
			}
			continue
		}
		if input[index] != candidate[index] {
			return false
		}
	}
	return true
}

func stringIn(value string, values []string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func cloneCommand(command CommandDefinition) CommandDefinition {
	command.Path = append([]string{}, command.Path...)
	command.Aliases = copyPaths(command.Aliases)
	command.Capabilities = append([]string{}, command.Capabilities...)
	command.PathMatching = clonePathMatching(command.PathMatching)
	command.Positionals = append([]PositionalDefinition{}, command.Positionals...)
	command.Options = append([]OptionDefinition{}, command.Options...)
	for index := range command.Options {
		option := &command.Options[index]
		option.Aliases = append([]string{}, option.Aliases...)
		option.Enum = append([]string{}, option.Enum...)
		option.Default = cloneAny(option.Default)
		option.DefaultSource = cloneMap(option.DefaultSource)
		option.Compatibility = cloneMap(option.Compatibility)
		if option.Minimum != nil {
			value := *option.Minimum
			option.Minimum = &value
		}
		if option.Maximum != nil {
			value := *option.Maximum
			option.Maximum = &value
		}
	}
	command.Availability = cloneAvailability(command.Availability)
	command.SideEffects = append([]string{}, command.SideEffects...)
	command.Output = cloneOutput(command.Output)
	return command
}

func cloneCommands(commands []CommandDefinition) []CommandDefinition {
	out := make([]CommandDefinition, len(commands))
	for index, command := range commands {
		out[index] = cloneCommand(command)
	}
	return out
}

func cloneNamespace(namespace NamespaceDefinition) NamespaceDefinition {
	namespace.Path = append([]string{}, namespace.Path...)
	return namespace
}

func cloneNamespaces(namespaces []NamespaceDefinition) []NamespaceDefinition {
	out := make([]NamespaceDefinition, len(namespaces))
	for index, namespace := range namespaces {
		out[index] = cloneNamespace(namespace)
	}
	return out
}

func clonePathMatching(value PathMatchingDefinition) PathMatchingDefinition {
	value.CaseInsensitiveSegments = append([]int{}, value.CaseInsensitiveSegments...)
	return value
}

func cloneAvailability(value AvailabilityDefinition) AvailabilityDefinition {
	value.CheckCommand = append([]string{}, value.CheckCommand...)
	value.InputBindings = append([]AvailabilityInputBinding{}, value.InputBindings...)
	for index := range value.InputBindings {
		value.InputBindings[index].Constant = cloneAny(value.InputBindings[index].Constant)
	}
	value.DoesNotProve = append([]string{}, value.DoesNotProve...)
	return value
}

func cloneOutput(value OutputDefinition) OutputDefinition {
	value.Formats = append([]string{}, value.Formats...)
	value.Variants = append([]string{}, value.Variants...)
	value.ExitCodes = append([]ExitCodeDefinition{}, value.ExitCodes...)
	return value
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = cloneAny(item)
	}
	return out
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneAny(item)
		}
		return out
	default:
		return typed
	}
}

func CategoryRank(category Category) int {
	switch category {
	case CategoryWorkflow:
		return 0
	case CategoryUtility:
		return 1
	default:
		return 2
	}
}
