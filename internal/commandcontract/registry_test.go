package commandcontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/deqiying/fast-context/internal/config"
)

func TestDefaultRegistryContract(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	commands := registry.Commands()
	if len(commands) != 7 {
		t.Fatalf("Commands returned %d definitions, want 7", len(commands))
	}
	if len(registry.Namespaces()) != 2 {
		t.Fatalf("Namespaces returned %d definitions, want 2", len(registry.Namespaces()))
	}
	wantPaths := map[string]string{
		"doctor":      "doctor",
		"key.extract": "key extract",
		"schema":      "schema",
		"search":      "search",
		"skills.list": "skills list",
		"skills.show": "skills show",
		"version":     "version",
	}
	for _, definition := range commands {
		if got := displayPath(definition.Path); got != wantPaths[definition.ID] {
			t.Fatalf("command %q path=%q, want %q", definition.ID, got, wantPaths[definition.ID])
		}
		if definition.Summary == "" || definition.Output.Contract == "" || len(definition.Output.ExitCodes) == 0 {
			t.Fatalf("command %q has an incomplete public contract", definition.ID)
		}
	}

	search, ok := registry.LookupID("search")
	if !ok {
		t.Fatal("search definition is missing")
	}
	if !reflect.DeepEqual(search.Capabilities, []string{"semantic_code_search", "code_context"}) {
		t.Fatalf("search capabilities=%v", search.Capabilities)
	}
	for _, required := range []string{"credential_source_read", "external_network", "project_filesystem_read", "repository_context_transmission"} {
		if !contains(search.SideEffects, required) {
			t.Fatalf("search side effects omit %q: %v", required, search.SideEffects)
		}
	}
	if !search.Availability.Dynamic || !reflect.DeepEqual(search.Availability.CheckCommand, []string{"doctor"}) || search.Availability.JSONPointer != "/ok" {
		t.Fatalf("unexpected search availability: %#v", search.Availability)
	}

	doctor, _ := registry.LookupID("doctor")
	if !contains(doctor.SideEffects, "credential_source_read") {
		t.Fatalf("doctor side effects omit credential read: %v", doctor.SideEffects)
	}
	list, _ := registry.LookupID("skills.list")
	show, _ := registry.LookupID("skills.show")
	if len(list.SideEffects) != 0 || !reflect.DeepEqual(show.SideEffects, []string{"embedded_resource_read"}) {
		t.Fatalf("unexpected skills side effects: list=%v show=%v", list.SideEffects, show.SideEffects)
	}
}

func TestRegistryLookupAndInvocationCompatibility(t *testing.T) {
	registry := MustDefaultRegistry()
	for _, path := range [][]string{{"skills", "list"}, {"skills", "show"}, {"version"}} {
		if _, ok := registry.LookupCanonical(path...); !ok {
			t.Fatalf("canonical lookup failed for %v", path)
		}
	}
	for _, alias := range [][]string{{"skills", "ls"}, {"skills", "get"}, {"skills", "read"}, {"--version"}, {"-v"}} {
		if _, ok := registry.LookupAlias(alias...); !ok {
			t.Fatalf("alias lookup failed for %v", alias)
		}
	}
	for _, invocation := range [][]string{{"skills", "LIST"}, {"skills", "LS"}, {"skills", "SHOW"}, {"skills", "GET"}, {"skills", "READ"}} {
		definition, consumed, ok := registry.ResolveInvocation(invocation)
		if !ok || consumed != 2 || definition.ID == "" {
			t.Fatalf("ResolveInvocation(%v)=(%q,%d,%t)", invocation, definition.ID, consumed, ok)
		}
	}
	if _, _, ok := registry.ResolveInvocation([]string{"Search"}); ok {
		t.Fatal("top-level commands must remain case-sensitive")
	}
	if _, ok := registry.LookupCanonical("skills", "LS"); ok {
		t.Fatal("canonical lookup must remain exact for targeted schema")
	}
}

func TestDefinitionsProduceBoundInputSchemas(t *testing.T) {
	registry := MustDefaultRegistry()
	for _, definition := range registry.Commands() {
		schema := definition.InputSchema()
		if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("command %q has invalid schema envelope: %#v", definition.ID, schema)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("command %q properties have type %T", definition.ID, schema["properties"])
		}
		for name, rawProperty := range properties {
			property, ok := rawProperty.(map[string]any)
			if !ok || property["x-cli-binding"] == nil {
				t.Fatalf("command %q property %q lacks a binding", definition.ID, name)
			}
		}
		for index, option := range definition.Options {
			property := properties[option.Name].(map[string]any)
			binding := property["x-cli-binding"].(map[string]any)
			if binding["order"] != index {
				t.Fatalf("command %q option %q order=%v, want %d", definition.ID, option.Name, binding["order"], index)
			}
		}
	}

	search, _ := registry.LookupID("search")
	properties := search.InputSchema()["properties"].(map[string]any)
	timeout := properties["timeout_ms"].(map[string]any)
	if timeout["minimum"] != int(config.MinTimeout/time.Millisecond) || timeout["maximum"] != int(config.MaxTimeout/time.Millisecond) {
		t.Fatalf("timeout canonical bounds drifted: %#v", timeout)
	}
	defaultSource := timeout["x-cli-default-source"].(map[string]any)
	if defaultSource["environment"] != config.EnvTimeoutMS || defaultSource["fallback"] != int(config.DefaultTimeout/time.Millisecond) {
		t.Fatalf("timeout default source drifted: %#v", defaultSource)
	}
	verbose := properties["verbose"].(map[string]any)["x-cli-binding"].(map[string]any)
	if verbose["absent"] != "inherit_runtime" || verbose["false"] != "explicit_boolean_value" {
		t.Fatalf("verbose tri-state binding drifted: %#v", verbose)
	}

	schemaCommand, _ := registry.LookupID("schema")
	schemaProperties := schemaCommand.InputSchema()["properties"].(map[string]any)
	commandPath := schemaProperties["command_path"].(map[string]any)
	binding := commandPath["x-cli-binding"].(map[string]any)
	if commandPath["type"] != "array" || binding["variadic"] != true || binding["lookup"] != "canonical_only" {
		t.Fatalf("schema self-description drifted: %#v", commandPath)
	}
}

func TestJSONCommandsExposePrettyPresentationOption(t *testing.T) {
	registry := MustDefaultRegistry()
	wantDefaults := map[string]string{
		"search":      "text",
		"key.extract": "text",
		"doctor":      "text",
		"skills.list": "text",
		"skills.show": "content",
		"schema":      "json",
	}
	for id, wantDefault := range wantDefaults {
		definition, ok := registry.LookupID(id)
		if !ok {
			t.Fatalf("command %q is missing", id)
		}
		if definition.Output.DefaultFormat != wantDefault {
			t.Fatalf("command %q default format=%q, want %q", id, definition.Output.DefaultFormat, wantDefault)
		}
		if len(definition.Options) == 0 {
			t.Fatalf("command %q has no options", id)
		}
		pretty := definition.Options[len(definition.Options)-1]
		defaultValue, defaultOK := pretty.Default.(bool)
		if pretty.Name != "pretty" || pretty.Flag != "--pretty" || pretty.Type != TypeBoolean || !pretty.HasDefault || !defaultOK || defaultValue {
			t.Fatalf("command %q pretty option=%#v", id, pretty)
		}
		for _, variant := range definition.Output.Variants {
			if variant == "pretty" {
				t.Fatalf("command %q exposes pretty as a payload variant", id)
			}
		}

		properties := definition.InputSchema()["properties"].(map[string]any)
		property, ok := properties["pretty"].(map[string]any)
		if !ok || property["type"] != "boolean" || property["default"] != false {
			t.Fatalf("command %q pretty schema=%#v", id, property)
		}
		binding := property["x-cli-binding"].(map[string]any)
		if binding["kind"] != "flag" || binding["token"] != "--pretty" || binding["order"] != len(definition.Options)-1 {
			t.Fatalf("command %q pretty binding=%#v", id, binding)
		}
	}

	version, _ := registry.LookupID("version")
	if len(version.Options) != 0 {
		t.Fatalf("version unexpectedly exposes options: %#v", version.Options)
	}
}

func TestRegistryReturnsDeepCopies(t *testing.T) {
	registry := MustDefaultRegistry()
	first, _ := registry.LookupID("search")
	first.Path[0] = "mutated"
	first.Capabilities[0] = "mutated"
	first.Options[0].Aliases[0] = "mutated"
	first.Options[0].DefaultSource["kind"] = "mutated"
	first.Availability.CheckCommand[0] = "mutated"
	first.Output.Formats[0] = "mutated"
	second, _ := registry.LookupID("search")
	if second.Path[0] != "search" || second.Capabilities[0] == "mutated" || second.Options[0].Aliases[0] != "-p" || second.Options[0].DefaultSource["kind"] == "mutated" || second.Availability.CheckCommand[0] != "doctor" || second.Output.Formats[0] == "mutated" {
		t.Fatalf("registry exposed mutable state: %#v", second)
	}
}

func TestValidateDefinitionsRejectsBrokenContracts(t *testing.T) {
	baseCommands := commandDefinitions()
	baseNamespaces := namespaceDefinitions()
	tests := []struct {
		name   string
		mutate func([]CommandDefinition, []NamespaceDefinition)
	}{
		{name: "duplicate id", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) { commands[1].ID = commands[0].ID }},
		{name: "duplicate path", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) {
			commands[1].Path = append([]string{}, commands[0].Path...)
		}},
		{name: "duplicate flag", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) {
			commands[0].Options[1].Flag = commands[0].Options[0].Flag
		}},
		{name: "invalid path matching", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) {
			commands[0].PathMatching.CaseInsensitiveSegments = []int{4}
		}},
		{name: "ambiguous case-insensitive path", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) {
			commands[4].Path = []string{"skills", "LIST"}
			commands[4].Aliases = nil
		}},
		{name: "repeatable scalar", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) { commands[0].Options[0].Repeatable = true }},
		{name: "missing handler-facing output", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) { commands[0].Output.Formats = nil }},
		{name: "missing availability target", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) {
			commands[0].Availability.CheckCommand = []string{"missing"}
		}},
		{name: "invalid availability binding", mutate: func(commands []CommandDefinition, _ []NamespaceDefinition) {
			commands[0].Availability.InputBindings[0].Target = "missing"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := cloneCommands(baseCommands)
			namespaces := cloneNamespaces(baseNamespaces)
			test.mutate(commands, namespaces)
			if err := ValidateDefinitions(commands, namespaces); err == nil {
				t.Fatal("ValidateDefinitions unexpectedly accepted a broken contract")
			}
		})
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
