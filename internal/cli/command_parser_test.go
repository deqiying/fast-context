package cli

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deqiying/fast-context/internal/commandcontract"
	"github.com/deqiying/fast-context/internal/config"
)

func definitionForTest(t *testing.T, id string) commandcontract.CommandDefinition {
	t.Helper()
	definition, ok := defaultCommandRegistry.LookupID(id)
	if !ok {
		t.Fatalf("command definition %q is missing", id)
	}
	return definition
}

func TestParseSearchCompatibility(t *testing.T) {
	definition := definitionForTest(t, "search")
	parsed, err := parseCommand(definition, []string{
		"find", "auth", "flow",
		"--project", "fixture",
		"--exclude", "vendor",
		"--format=json",
		"--exclude=generated",
		"--max-turns", "99",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.String("query") != "find auth flow" || parsed.String("project") != "fixture" || parsed.String("format") != "json" || parsed.Int("max_turns") != config.MaxSearchTurns {
		t.Fatalf("unexpected parsed search: %#v", parsed.Values)
	}
	if !reflect.DeepEqual(parsed.Strings("exclude"), []string{"vendor", "generated"}) {
		t.Fatalf("exclude order changed: %v", parsed.Strings("exclude"))
	}
	if parsed.Int("tree_depth") != config.DefaultTreeDepth || parsed.Int("max_results") != config.DefaultMaxResults {
		t.Fatalf("fixed defaults are missing: %#v", parsed.Values)
	}
}

func TestParseSearchEndOfOptionsAndLeadingDashValues(t *testing.T) {
	definition := definitionForTest(t, "search")
	parsed, err := parseCommand(definition, []string{
		"--project=-fixture",
		"--exclude=-generated",
		"--",
		"-leading", "query",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.String("query") != "-leading query" || parsed.String("project") != "-fixture" || !reflect.DeepEqual(parsed.Strings("exclude"), []string{"-generated"}) {
		t.Fatalf("unexpected leading-dash parse: %#v", parsed.Values)
	}
}

func TestParseDurationCompatibility(t *testing.T) {
	definition := definitionForTest(t, "search")
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{raw: "30s", want: 30 * time.Second},
		{raw: "30000", want: 30 * time.Second},
		{raw: "1500us", want: 1500 * time.Microsecond},
		{raw: "1ns", want: time.Nanosecond},
		{raw: "-2s", want: -2 * time.Second},
		{raw: "600s", want: 600 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			parsed, err := parseCommand(definition, []string{"query", "--timeout", test.raw})
			if err != nil {
				t.Fatal(err)
			}
			if got := parsed.Duration("timeout_ms"); got != test.want {
				t.Fatalf("timeout=%v, want %v", got, test.want)
			}
		})
	}
	if _, err := parseCommand(definition, []string{"query", "--timeout", "invalid"}); err == nil {
		t.Fatal("invalid duration was accepted")
	}
}

func TestParseBooleanTriState(t *testing.T) {
	definition := definitionForTest(t, "search")
	tests := []struct {
		args        []string
		name        string
		want        bool
		wantPresent bool
	}{
		{args: []string{"query"}, name: "verbose", want: false, wantPresent: false},
		{args: []string{"query", "--verbose"}, name: "verbose", want: true, wantPresent: true},
		{args: []string{"query", "--verbose=true"}, name: "verbose", want: true, wantPresent: true},
		{args: []string{"query", "--verbose=false"}, name: "verbose", want: false, wantPresent: true},
		{args: []string{"query", "--include-snippets=false"}, name: "include_snippets", want: false, wantPresent: true},
		{args: []string{"query", "--no-bootstrap=false"}, name: "no_bootstrap", want: false, wantPresent: true},
	}
	for _, test := range tests {
		parsed, err := parseCommand(definition, test.args)
		if err != nil {
			t.Fatalf("parseCommand(%v): %v", test.args, err)
		}
		if parsed.Bool(test.name) != test.want || parsed.Present[test.name] != test.wantPresent {
			t.Fatalf("parseCommand(%v) %s=(%t,present=%t), want (%t,present=%t)", test.args, test.name, parsed.Bool(test.name), parsed.Present[test.name], test.want, test.wantPresent)
		}
	}
	if _, err := parseCommand(definition, []string{"query", "--verbose=maybe"}); err == nil {
		t.Fatal("invalid boolean was accepted")
	}
}

func TestPrettyBooleanAndNonJSONCompatibility(t *testing.T) {
	definition := definitionForTest(t, "search")
	tests := []struct {
		args        []string
		want        bool
		wantPresent bool
	}{
		{args: []string{"query"}, want: false, wantPresent: false},
		{args: []string{"query", "--pretty"}, want: true, wantPresent: true},
		{args: []string{"query", "--pretty=true"}, want: true, wantPresent: true},
		{args: []string{"query", "--pretty=false"}, want: false, wantPresent: true},
	}
	for _, test := range tests {
		parsed, err := parseCommand(definition, test.args)
		if err != nil {
			t.Fatalf("parseCommand(%v): %v", test.args, err)
		}
		if parsed.Bool("pretty") != test.want || parsed.Present["pretty"] != test.wantPresent {
			t.Fatalf("parseCommand(%v) pretty=(%t,present=%t), want (%t,present=%t)", test.args, parsed.Bool("pretty"), parsed.Present["pretty"], test.want, test.wantPresent)
		}
	}

	content, err := parseCommand(definitionForTest(t, "skills.show"), []string{"fast-context", "--format", "content", "--pretty"})
	if err != nil || content.String("format") != "content" || !content.Bool("pretty") {
		t.Fatalf("content/pretty parse values=%#v err=%v", content.Values, err)
	}
	if _, err := parseCommand(definitionForTest(t, "version"), []string{"--pretty"}); err == nil {
		t.Fatal("version unexpectedly accepted --pretty")
	}
}

func TestBuildSearchOptionsRespectsRuntimeAndExplicitOverrides(t *testing.T) {
	definition := definitionForTest(t, "search")
	runtimeDefaults := config.Runtime{
		MaxTurns:             4,
		MaxCommands:          11,
		Timeout:              45 * time.Second,
		RepoMapMode:          "classic",
		BootstrapTreeDepth:   2,
		HotspotTopK:          5,
		HotspotTreeDepth:     3,
		HotspotMaxBytes:      64000,
		BootstrapEnabled:     true,
		BootstrapMaxTurns:    3,
		BootstrapMaxCommands: 7,
		IncludeSnippets:      true,
		Debug:                true,
	}
	inherited, err := parseCommand(definition, []string{"query"})
	if err != nil {
		t.Fatal(err)
	}
	inheritedOptions := buildSearchOptions(inherited, runtimeDefaults, "fixture-cwd")
	if inheritedOptions.ProjectRoot != "fixture-cwd" || inheritedOptions.MaxTurns != 4 || inheritedOptions.MaxCommands != 11 || inheritedOptions.Timeout != 45*time.Second || inheritedOptions.RepoMapMode != "classic" || !inheritedOptions.IncludeSnippets || !inheritedOptions.Verbose || !inheritedOptions.BootstrapEnabled {
		t.Fatalf("runtime defaults were not inherited: %#v", inheritedOptions)
	}

	explicit, err := parseCommand(definition, []string{
		"query",
		"--project", "fixture-project",
		"--max-turns", "2",
		"--max-commands", "3",
		"--timeout", "1ns",
		"--repo-map-mode", "bootstrap_hotspot",
		"--include-snippets=false",
		"--verbose=false",
		"--no-bootstrap",
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitOptions := buildSearchOptions(explicit, runtimeDefaults, "fixture-cwd")
	if explicitOptions.ProjectRoot != "fixture-project" || explicitOptions.MaxTurns != 2 || explicitOptions.MaxCommands != 3 || explicitOptions.Timeout != time.Nanosecond || explicitOptions.RepoMapMode != "bootstrap_hotspot" || explicitOptions.IncludeSnippets || explicitOptions.Verbose || explicitOptions.BootstrapEnabled {
		t.Fatalf("explicit values did not override runtime: %#v", explicitOptions)
	}

	falseOverride, err := parseCommand(definition, []string{"query", "--no-bootstrap=false"})
	if err != nil {
		t.Fatal(err)
	}
	if got := buildSearchOptions(falseOverride, runtimeDefaults, "fixture-cwd"); !got.BootstrapEnabled {
		t.Fatal("--no-bootstrap=false must not disable runtime bootstrap")
	}
}

func TestDefinitionFallbacksTrackConfigRuntime(t *testing.T) {
	for _, name := range []string{
		config.EnvMaxTurns,
		config.EnvMaxCommands,
		config.EnvTimeoutMS,
		config.EnvRepoMapMode,
		config.EnvBootstrapEnabled,
		config.EnvIncludeSnippets,
		config.EnvDebug,
	} {
		t.Setenv(name, "")
	}
	definition := definitionForTest(t, "search")
	parsed, err := parseCommand(definition, []string{"query"})
	if err != nil {
		t.Fatal(err)
	}
	runtimeDefaults := config.ReadRuntime()
	opts := buildSearchOptions(parsed, runtimeDefaults, "fixture-cwd")
	if opts.MaxTurns != config.DefaultMaxTurns || opts.MaxCommands != config.DefaultMaxCommands || opts.Timeout != config.DefaultTimeout || opts.RepoMapMode != config.DefaultRepoMapMode || opts.IncludeSnippets || opts.Verbose || !opts.BootstrapEnabled {
		t.Fatalf("config fallback drifted from command binding: %#v", opts)
	}

	t.Setenv(config.EnvMaxTurns, "99")
	t.Setenv(config.EnvIncludeSnippets, "true")
	t.Setenv(config.EnvDebug, "true")
	runtimeDefaults = config.ReadRuntime()
	opts = buildSearchOptions(parsed, runtimeDefaults, "fixture-cwd")
	if opts.MaxTurns != config.MaxSearchTurns || !opts.IncludeSnippets || !opts.Verbose {
		t.Fatalf("environment defaults were not inherited: %#v", opts)
	}
	explicit, err := parseCommand(definition, []string{"query", "--max-turns", "1", "--include-snippets=false", "--verbose=false"})
	if err != nil {
		t.Fatal(err)
	}
	opts = buildSearchOptions(explicit, runtimeDefaults, "fixture-cwd")
	if opts.MaxTurns != config.MinSearchTurns || opts.IncludeSnippets || opts.Verbose {
		t.Fatalf("explicit values did not override environment defaults: %#v", opts)
	}
}

func TestSkillsFormatNormalizationIsCommandSpecific(t *testing.T) {
	list := definitionForTest(t, "skills.list")
	parsed, err := parseCommand(list, []string{"--format", " JSON "})
	if err != nil || parsed.String("format") != "json" {
		t.Fatalf("skills format normalization failed: values=%v err=%v", parsed.Values, err)
	}
	search := definitionForTest(t, "search")
	if _, err := parseCommand(search, []string{"query", "--format", "JSON"}); err == nil {
		t.Fatal("search format unexpectedly became case-insensitive")
	}
}

func TestParseRejectsInvalidArgumentsBeforeHandlers(t *testing.T) {
	tests := []struct {
		id   string
		args []string
	}{
		{id: "search", args: nil},
		{id: "search", args: []string{"query", "--unknown"}},
		{id: "search", args: []string{"query", "--max-turns", "invalid"}},
		{id: "search", args: []string{"query", "--project="}},
		{id: "key.extract", args: []string{"extra"}},
		{id: "key.extract", args: []string{"--path="}},
		{id: "doctor", args: []string{"extra"}},
		{id: "doctor", args: []string{"--project="}},
		{id: "skills.list", args: []string{"extra"}},
		{id: "skills.show", args: nil},
		{id: "skills.show", args: []string{"one", "two"}},
		{id: "version", args: []string{"extra"}},
		{id: "schema", args: []string{"--format", "yaml"}},
	}
	for _, test := range tests {
		t.Run(test.id+strings.Join(test.args, "_"), func(t *testing.T) {
			if _, err := parseCommand(definitionForTest(t, test.id), test.args); err == nil {
				t.Fatalf("parseCommand(%s, %v) unexpectedly succeeded", test.id, test.args)
			}
		})
	}
}

func TestCanonicalManifestBindingsRenderParseableArgv(t *testing.T) {
	manifest, err := BuildManifest(defaultCommandRegistry, "test", []string{"search"})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"query":            "-leading query",
		"project":          "-fixture",
		"max_turns":        5,
		"timeout_ms":       1000,
		"exclude":          []string{"vendor", ""},
		"format":           "json",
		"include_snippets": false,
		"pretty":           true,
		"verbose":          true,
	}
	argv, err := renderCanonicalArgvForTest(manifest.ArgvConventions, manifest.Commands[0], input)
	if err != nil {
		t.Fatal(err)
	}
	definition, consumed, ok := defaultCommandRegistry.ResolveInvocation(argv)
	if !ok || consumed != 1 {
		t.Fatalf("rendered argv did not resolve: %v", argv)
	}
	parsed, err := parseCommand(definition, argv[consumed:])
	if err != nil {
		t.Fatalf("rendered argv did not parse: %v: %v", argv, err)
	}
	if parsed.String("query") != "-leading query" || parsed.String("project") != "-fixture" || parsed.Duration("timeout_ms") != time.Second || parsed.Bool("include_snippets") || !parsed.Present["include_snippets"] || !parsed.Bool("pretty") || !reflect.DeepEqual(parsed.Strings("exclude"), []string{"vendor", ""}) {
		t.Fatalf("rendered values changed: argv=%v parsed=%#v", argv, parsed.Values)
	}
}

func renderCanonicalArgvForTest(conventions commandcontract.ArgvConventions, command commandcontract.ManifestCommand, input map[string]any) ([]string, error) {
	if !reflect.DeepEqual(conventions.Ordering, []string{"command_path", "options", "end_of_options_when_needed", "positionals"}) {
		return nil, fmt.Errorf("unsupported argv ordering: %v", conventions.Ordering)
	}
	properties, ok := command.InputSchema["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("manifest properties have type %T", command.InputSchema["properties"])
	}
	argv := append([]string{}, command.Path...)
	propertyNames := make([]string, 0, len(input))
	for name := range input {
		propertyNames = append(propertyNames, name)
	}
	sort.Slice(propertyNames, func(i, j int) bool {
		leftProperty := properties[propertyNames[i]].(map[string]any)
		rightProperty := properties[propertyNames[j]].(map[string]any)
		leftBinding := leftProperty["x-cli-binding"].(map[string]any)
		rightBinding := rightProperty["x-cli-binding"].(map[string]any)
		leftOrder, leftIsFlag := leftBinding["order"].(int)
		rightOrder, rightIsFlag := rightBinding["order"].(int)
		if leftIsFlag && rightIsFlag {
			return leftOrder < rightOrder
		}
		if leftIsFlag != rightIsFlag {
			return leftIsFlag
		}
		return propertyNames[i] < propertyNames[j]
	})
	type positionalValue struct {
		index  int
		values []string
	}
	positionals := []positionalValue{}
	for _, name := range propertyNames {
		rawProperty, exists := properties[name]
		if !exists {
			return nil, fmt.Errorf("unknown manifest property %q", name)
		}
		property := rawProperty.(map[string]any)
		binding := property["x-cli-binding"].(map[string]any)
		switch binding["kind"] {
		case "positional":
			index := binding["index"].(int)
			values, err := renderPositionalValue(input[name])
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			positionals = append(positionals, positionalValue{index: index, values: values})
		case "flag":
			token := binding["token"].(string)
			switch value := input[name].(type) {
			case bool:
				if value {
					argv = append(argv, token)
				} else {
					argv = append(argv, token+"=false")
				}
			case []string:
				for _, item := range value {
					argv = append(argv, renderFlagValue(token, item, conventions)...)
				}
			case int:
				argv = append(argv, token, strconv.Itoa(value))
			case string:
				argv = append(argv, renderFlagValue(token, value, conventions)...)
			default:
				return nil, fmt.Errorf("unsupported flag value %T", input[name])
			}
		default:
			return nil, fmt.Errorf("unsupported binding kind %v", binding["kind"])
		}
	}
	sort.Slice(positionals, func(i, j int) bool { return positionals[i].index < positionals[j].index })
	needsEndOfOptions := false
	for _, positional := range positionals {
		for _, value := range positional.values {
			if strings.HasPrefix(value, "-") {
				needsEndOfOptions = true
			}
		}
	}
	if needsEndOfOptions {
		argv = append(argv, "--")
	}
	for _, positional := range positionals {
		argv = append(argv, positional.values...)
	}
	return argv, nil
}

func renderFlagValue(token, value string, conventions commandcontract.ArgvConventions) []string {
	if value == "" || strings.HasPrefix(value, "-") {
		if conventions.LeadingDashOrEmptyFlagValue != "inline_equals" {
			panic("unsupported leading-dash value convention")
		}
		return []string{token + "=" + value}
	}
	return []string{token, value}
}

func renderPositionalValue(value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case []string:
		return append([]string{}, typed...), nil
	default:
		return nil, fmt.Errorf("unsupported positional value %T", value)
	}
}
