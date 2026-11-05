package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deqiying/fast-context/internal/commandcontract"
	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/output"
	"github.com/deqiying/fast-context/internal/search"
)

type codedError struct {
	code    string
	message string
}

func (e codedError) Error() string { return e.message }
func (e codedError) Code() string  { return e.code }

// listedTools 返回按名称索引的工具，以及经 JSON 往返后的 inputSchema，确保断言的是宿主实际看到的内容。
func listedTools(t *testing.T, session *mcp.ClientSession) (map[string]*mcp.Tool, map[string]map[string]any) {
	t.Helper()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	schemas := map[string]map[string]any{}
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		schemas[tool.Name] = schema
	}
	return tools, schemas
}

func schemaProperty(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	properties, _ := schema["properties"].(map[string]any)
	property, ok := properties[name].(map[string]any)
	if !ok {
		t.Fatalf("schema has no property %q: %#v", name, schema)
	}
	return property
}

func TestListToolsExposesStrictSchemas(t *testing.T) {
	tools, schemas := listedTools(t, connectInMemory(t, testConfig(&fakeSearch{}, io.Discard)))
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{doctorToolName, searchToolName}) {
		t.Fatalf("unexpected tools: %v", names)
	}

	searchSchema := schemas[searchToolName]
	properties, _ := searchSchema["properties"].(map[string]any)
	propertyNames := make([]string, 0, len(properties))
	for name := range properties {
		propertyNames = append(propertyNames, name)
	}
	sort.Strings(propertyNames)
	wantProperties := []string{"exclude_paths", "include_code_snippets", "max_results", "max_turns", "project_path", "query", "tree_depth"}
	if !reflect.DeepEqual(propertyNames, wantProperties) {
		t.Fatalf("search properties=%v, want %v", propertyNames, wantProperties)
	}
	if !reflect.DeepEqual(searchSchema["required"], []any{"query", "project_path"}) || searchSchema["additionalProperties"] != false {
		t.Fatalf("search schema must require query/project_path and reject unknown keys: %#v", searchSchema)
	}

	doctorSchema := schemas[doctorToolName]
	doctorProperties, _ := doctorSchema["properties"].(map[string]any)
	if len(doctorProperties) != 1 || !reflect.DeepEqual(doctorSchema["required"], []any{"project_path"}) || doctorSchema["additionalProperties"] != false {
		t.Fatalf("doctor schema must only accept project_path: %#v", doctorSchema)
	}

	searchAnnotations := tools[searchToolName].Annotations
	if searchAnnotations == nil || !searchAnnotations.ReadOnlyHint || searchAnnotations.OpenWorldHint == nil || !*searchAnnotations.OpenWorldHint {
		t.Fatalf("search must be read-only and open-world: %#v", searchAnnotations)
	}
	doctorAnnotations := tools[doctorToolName].Annotations
	if doctorAnnotations == nil || !doctorAnnotations.ReadOnlyHint || doctorAnnotations.OpenWorldHint == nil || *doctorAnnotations.OpenWorldHint {
		t.Fatalf("doctor must be read-only and closed-world: %#v", doctorAnnotations)
	}
	if !strings.Contains(tools[searchToolName].Description, "Windsurf") {
		t.Fatalf("search description must disclose external transmission: %q", tools[searchToolName].Description)
	}
}

func TestSearchSchemaRangesMatchCLIContract(t *testing.T) {
	registry, err := commandcontract.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := registry.LookupID("search")
	if !ok {
		t.Fatal("search definition is missing")
	}
	_, schemas := listedTools(t, connectInMemory(t, testConfig(&fakeSearch{}, io.Discard)))
	for _, option := range definition.Options {
		switch option.Name {
		case "tree_depth", "max_turns", "max_results":
		default:
			continue
		}
		property := schemaProperty(t, schemas[searchToolName], option.Name)
		if property["minimum"] != float64(*option.Minimum) || property["maximum"] != float64(*option.Maximum) {
			t.Fatalf("%s range %v..%v differs from CLI %d..%d", option.Name, property["minimum"], property["maximum"], *option.Minimum, *option.Maximum)
		}
	}
}

func TestSearchSchemaDefaultsFollowRuntime(t *testing.T) {
	cfg := testConfig(&fakeSearch{}, io.Discard)
	tools, schemas := listedTools(t, connectInMemory(t, cfg))
	schema := schemas[searchToolName]
	if schemaProperty(t, schema, "max_turns")["default"] != float64(cfg.Runtime.MaxTurns) ||
		schemaProperty(t, schema, "tree_depth")["default"] != float64(config.DefaultTreeDepth) ||
		schemaProperty(t, schema, "max_results")["default"] != float64(config.DefaultMaxResults) ||
		schemaProperty(t, schema, "include_code_snippets")["default"] != false {
		t.Fatalf("unexpected schema defaults: %#v", schema)
	}
	if !strings.Contains(tools[searchToolName].Description, "include_code_snippets: Default false") {
		t.Fatalf("implicit snippets default must use the neutral wording: %q", tools[searchToolName].Description)
	}

	explicit := testConfig(&fakeSearch{}, io.Discard)
	explicit.Runtime.MaxTurns = 4
	explicit.Runtime.IncludeSnippets = true
	explicit.IncludeSnippetsExplicit = true
	tools, schemas = listedTools(t, connectInMemory(t, explicit))
	schema = schemas[searchToolName]
	snippets := schemaProperty(t, schema, "include_code_snippets")
	if schemaProperty(t, schema, "max_turns")["default"] != float64(4) || snippets["default"] != true {
		t.Fatalf("schema defaults must follow Runtime: %#v", schema)
	}
	if description, _ := snippets["description"].(string); !strings.Contains(description, "Server default: true") {
		t.Fatalf("explicit snippets description=%q", description)
	}
	if !strings.Contains(tools[searchToolName].Description, "Server-configured default is true") {
		t.Fatalf("explicit snippets tool description=%q", tools[searchToolName].Description)
	}
}

func TestSearchFillsDefaultsAndTakesServerFieldsFromRuntime(t *testing.T) {
	fake := &fakeSearch{}
	cfg := testConfig(fake, io.Discard)
	session := connectInMemory(t, cfg)
	project := t.TempDir()

	result := callTool(t, session, searchToolName, map[string]any{"query": "login flow", "project_path": project})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}
	got := fake.lastCall(t)
	want := search.DefaultOptions(cfg.Runtime)
	want.Query = "login flow"
	want.ProjectRoot = project
	got.Progress = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("search options mismatch:\n got %#v\nwant %#v", got, want)
	}
	if got.TreeDepth != config.DefaultTreeDepth || got.MaxResults != config.DefaultMaxResults || got.MaxTurns != cfg.Runtime.MaxTurns || got.Format != "text" {
		t.Fatalf("defaults were not applied: %#v", got)
	}
}

func TestSearchMapsExplicitArguments(t *testing.T) {
	fake := &fakeSearch{}
	session := connectInMemory(t, testConfig(fake, io.Discard))
	project := t.TempDir()

	result := callTool(t, session, searchToolName, map[string]any{
		"query":                 "  payment retry  ",
		"project_path":          "  " + project + "  ",
		"tree_depth":            0,
		"max_turns":             5,
		"max_results":           30,
		"exclude_paths":         []string{"dist", "*.min.*"},
		"include_code_snippets": true,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}
	got := fake.lastCall(t)
	if got.Query != "  payment retry  " || got.ProjectRoot != project || got.TreeDepth != 0 || got.MaxTurns != 5 || got.MaxResults != 30 ||
		!reflect.DeepEqual(got.ExcludePaths, []string{"dist", "*.min.*"}) || !got.IncludeSnippets {
		t.Fatalf("explicit arguments were not mapped: %#v", got)
	}
}

func TestSearchRejectsInvalidArgumentsBeforeSearching(t *testing.T) {
	fake := &fakeSearch{}
	session := connectInMemory(t, testConfig(fake, io.Discard))
	project := t.TempDir()
	tests := []map[string]any{
		{"query": "q", "project_path": project, "tree_depth": 7},
		{"query": "q", "project_path": project, "max_turns": 0},
		{"query": "q", "project_path": project, "max_results": 0},
		{"query": "q", "project_path": project, "max_results": 31},
		{"query": "q", "project_path": project, "unknown": true},
		{"query": "   ", "project_path": project},
		{"query": "", "project_path": project},
		{"project_path": project},
		{"query": "q"},
	}
	for _, arguments := range tests {
		result := callTool(t, session, searchToolName, arguments)
		if !result.IsError {
			t.Fatalf("arguments %v must be rejected, got %s", arguments, resultText(t, result))
		}
	}
	if fake.callCount() != 0 {
		t.Fatalf("search ran %d times for invalid arguments", fake.callCount())
	}
}

func TestSearchValidatesProjectPath(t *testing.T) {
	fake := &fakeSearch{}
	session := connectInMemory(t, testConfig(fake, io.Discard))
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing")
	tests := []struct {
		path string
		want string
	}{
		{path: "   ", want: projectPathRequiredMessage},
		{path: "relative/path", want: "Error: project_path must be an absolute path, got: relative/path"},
		{path: missing, want: "Error: project_path does not exist: " + missing},
		{path: file, want: "Error: project_path is not a directory: " + file},
	}
	for _, test := range tests {
		result := callTool(t, session, searchToolName, map[string]any{"query": "q", "project_path": test.path})
		if text := resultText(t, result); !result.IsError || text != test.want {
			t.Fatalf("project_path %q: isError=%v text=%q, want %q", test.path, result.IsError, text, test.want)
		}
	}
	if fake.callCount() != 0 {
		t.Fatalf("search ran %d times for invalid project paths", fake.callCount())
	}
}

func TestSearchSuccessUsesMCPSurfaceFormatting(t *testing.T) {
	project := t.TempDir()
	found := search.Result{
		Files:       []search.ResultFile{{Path: "auth/login.go", Ranges: []search.LineRange{{Start: 10, End: 42}}}},
		RGPatterns:  []string{"Login"},
		ProjectRoot: project,
	}
	fake := &fakeSearch{respond: func(context.Context, search.Options) (search.Result, error) { return found, nil }}
	session := connectInMemory(t, testConfig(fake, io.Discard))

	result := callTool(t, session, searchToolName, map[string]any{"query": "login", "project_path": project})
	if want := output.FormatTextFor(found, fake.lastCall(t), output.SurfaceMCP); result.IsError || resultText(t, result) != want {
		t.Fatalf("isError=%v text=%q, want %q", result.IsError, resultText(t, result), want)
	}
}

func TestSearchWithoutResultsIsNotAnError(t *testing.T) {
	session := connectInMemory(t, testConfig(&fakeSearch{}, io.Discard))
	result := callTool(t, session, searchToolName, map[string]any{"query": "nothing", "project_path": t.TempDir()})
	text := resultText(t, result)
	if result.IsError || !strings.Contains(text, "No relevant files found.") {
		t.Fatalf("no-result call: isError=%v text=%q", result.IsError, text)
	}
	if !strings.Contains(text, "narrow project_path") || !strings.Contains(text, "add exclude_paths") || strings.Contains(text, "--project") || strings.Contains(text, "--exclude") {
		t.Fatalf("no-result hints must use MCP parameter names: %q", text)
	}
}

func TestSearchBusinessErrorsUseMCPHints(t *testing.T) {
	tests := []struct {
		err      error
		wantCode string
		wantHint string
	}{
		{err: codedError{code: "AUTH_ERROR", message: "unauthorized"}, wantCode: "AUTH_ERROR", wantHint: "fast_context_doctor"},
		{err: codedError{code: "PAYLOAD_TOO_LARGE", message: "too large"}, wantCode: "PAYLOAD_TOO_LARGE", wantHint: "reduce tree_depth"},
		{err: errors.New("boom"), wantCode: "UNKNOWN", wantHint: "fast_context_doctor"},
	}
	for _, test := range tests {
		failure := test.err
		fake := &fakeSearch{respond: func(context.Context, search.Options) (search.Result, error) { return search.Result{}, failure }}
		session := connectInMemory(t, testConfig(fake, io.Discard))
		result := callTool(t, session, searchToolName, map[string]any{"query": "q", "project_path": t.TempDir()})
		text := resultText(t, result)
		if !result.IsError || !strings.Contains(text, "error_type="+test.wantCode) || !strings.Contains(text, test.wantHint) {
			t.Fatalf("error %v: isError=%v text=%q", test.err, result.IsError, text)
		}
		if strings.Contains(text, "fast-context doctor") || strings.Contains(text, "--tree-depth") {
			t.Fatalf("MCP error hints must not reference CLI flags or commands: %q", text)
		}
	}
}

func TestSearchCancellationReachesSearch(t *testing.T) {
	started := make(chan struct{})
	observed := make(chan error, 1)
	fake := &fakeSearch{respond: func(ctx context.Context, _ search.Options) (search.Result, error) {
		close(started)
		<-ctx.Done()
		observed <- ctx.Err()
		return search.Result{}, ctx.Err()
	}}
	session := connectInMemory(t, testConfig(fake, io.Discard))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callErr := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: searchToolName, Arguments: map[string]any{"query": "q", "project_path": t.TempDir()}})
		callErr <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("search did not start")
	}
	cancel()
	select {
	case err := <-callErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CallTool after cancel returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CallTool did not return after cancel")
	}
	// client 取消会发送 notifications/cancelled，server 端的 handler ctx 随之取消。
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("search observed %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search ctx was not cancelled")
	}
}

func TestConcurrentSearchCallsAreIndependent(t *testing.T) {
	var arrived sync.WaitGroup
	arrived.Add(2)
	release := make(chan struct{})
	fake := &fakeSearch{respond: func(_ context.Context, opts search.Options) (search.Result, error) {
		arrived.Done()
		<-release // 两个调用都进入 handler 后才返回，证明它们并发执行
		return search.Result{Files: []search.ResultFile{{Path: opts.Query + ".go"}}}, nil
	}}
	session := connectInMemory(t, testConfig(fake, io.Discard))
	project := t.TempDir()

	queries := []string{"alpha", "beta"}
	texts := make([]string, len(queries))
	errs := make([]error, len(queries))
	var calls sync.WaitGroup
	for index, query := range queries {
		calls.Add(1)
		go func() {
			defer calls.Done()
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: searchToolName, Arguments: map[string]any{"query": query, "project_path": project}})
			if err == nil && len(result.Content) == 1 {
				if text, ok := result.Content[0].(*mcp.TextContent); ok {
					texts[index] = text.Text
				}
			}
			errs[index] = err
		}()
	}
	waitArrived := make(chan struct{})
	go func() { arrived.Wait(); close(waitArrived) }()
	select {
	case <-waitArrived:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("tool calls were not handled concurrently")
	}
	close(release)
	calls.Wait()
	for index, query := range queries {
		if errs[index] != nil || !strings.Contains(texts[index], query+".go") {
			t.Fatalf("call %q: err=%v text=%q", query, errs[index], texts[index])
		}
	}
}
