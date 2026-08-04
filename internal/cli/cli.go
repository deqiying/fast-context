package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/deqiying/fast-context/internal/commandcontract"
	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/credentials"
	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/output"
	"github.com/deqiying/fast-context/internal/search"
	"github.com/deqiying/fast-context/internal/version"
	"github.com/deqiying/fast-context/internal/windsurf"
)

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return executeWithRegistryAndHandlers(ctx, defaultCommandRegistry, defaultCommandHandlers, args, stdout, stderr)
}

func executeWithRegistryAndHandlers(ctx context.Context, registry *commandcontract.Registry, handlers map[string]commandHandler, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRegistryRootHelp(stdout, registry)
		return 0
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printRegistryRootHelp(stdout, registry)
		return 0
	}
	if args[0] == "--version" || args[0] == "-v" {
		printVersion(stdout)
		return 0
	}

	route, ok := resolveCommandRoute(registry, args)
	if !ok {
		if namespace, namespaceOK := namespaceForInvocation(registry, args); namespaceOK {
			tail := args[len(namespace.Path):]
			if len(tail) == 0 || tail[0] == "help" || tail[0] == "-h" || tail[0] == "--help" {
				printNamespaceHelp(stdout, registry, namespace)
				return 0
			}
			return usageError(stderr, fmt.Sprintf("unknown %s command: %s", strings.Join(namespace.Path, " "), tail[0]))
		}
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printRegistryRootHelp(stderr, registry)
		return 2
	}

	tail := args[route.Consumed:]
	if hasCommandHelpToken(route.Definition, tail) {
		printCommandHelp(stdout, route.Definition)
		return 0
	}
	parsed, err := parseCommand(route.Definition, tail)
	if err != nil {
		if route.Definition.ID == "schema" {
			return writeSchemaArgumentError(registry, stdout, stderr, err.Error())
		}
		return usageError(stderr, err.Error())
	}
	handler := handlers[route.Definition.ID]
	if handler == nil {
		fmt.Fprintf(stderr, "Error: command handler is unavailable: %s\n", route.Definition.ID)
		return 1
	}
	return handler(ctx, registry, parsed, stdout, stderr)
}

func runParsedSearch(ctx context.Context, parsed ParsedCommand, stdout, stderr io.Writer) int {
	opts := buildSearchOptions(parsed, config.ReadRuntime(), cwd())
	if opts.Verbose {
		opts.Progress = func(message string) {
			fmt.Fprintln(stderr, message)
		}
	}

	client := windsurf.NewClient(nil)
	result, err := search.RunPipeline(ctx, opts, client)
	if opts.Format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err != nil {
			_ = encoder.Encode(output.ErrorJSON(err, result))
			return 1
		}
		_ = encoder.Encode(result)
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, output.FormatError(err, opts, result))
		return 1
	}
	fmt.Fprint(stdout, output.FormatText(result, opts))
	return 0
}

func buildSearchOptions(parsed ParsedCommand, runtimeDefaults config.Runtime, workingDirectory string) search.Options {
	opts := search.Options{
		Query:        parsed.String("query"),
		ProjectRoot:  workingDirectory,
		TreeDepth:    parsed.Int("tree_depth"),
		MaxTurns:     runtimeDefaults.MaxTurns,
		MaxCommands:  runtimeDefaults.MaxCommands,
		MaxResults:   parsed.Int("max_results"),
		Timeout:      runtimeDefaults.Timeout,
		ExcludePaths: parsed.Strings("exclude"),
		Format:       parsed.String("format"),

		RepoMapMode:          runtimeDefaults.RepoMapMode,
		BootstrapEnabled:     runtimeDefaults.BootstrapEnabled,
		BootstrapTreeDepth:   runtimeDefaults.BootstrapTreeDepth,
		BootstrapMaxTurns:    runtimeDefaults.BootstrapMaxTurns,
		BootstrapMaxCommands: runtimeDefaults.BootstrapMaxCommands,
		HotspotTopK:          runtimeDefaults.HotspotTopK,
		HotspotTreeDepth:     runtimeDefaults.HotspotTreeDepth,
		HotspotMaxBytes:      runtimeDefaults.HotspotMaxBytes,
		IncludeSnippets:      runtimeDefaults.IncludeSnippets,
		Verbose:              runtimeDefaults.Debug,
	}
	if parsed.Present["project"] {
		opts.ProjectRoot = parsed.String("project")
	}
	if parsed.Present["max_turns"] {
		opts.MaxTurns = parsed.Int("max_turns")
	}
	if parsed.Present["max_commands"] {
		opts.MaxCommands = parsed.Int("max_commands")
	}
	if parsed.Present["timeout_ms"] {
		opts.Timeout = parsed.Duration("timeout_ms")
	}
	if parsed.Present["repo_map_mode"] {
		opts.RepoMapMode = parsed.String("repo_map_mode")
	}
	if parsed.Present["include_snippets"] {
		opts.IncludeSnippets = parsed.Bool("include_snippets")
	}
	if parsed.Present["verbose"] {
		opts.Verbose = parsed.Bool("verbose")
	}
	if parsed.Present["no_bootstrap"] && parsed.Bool("no_bootstrap") {
		opts.BootstrapEnabled = false
	}
	return opts
}

func runParsedKeyExtract(parsed ParsedCommand, stdout, stderr io.Writer) int {
	explicitPath := parsed.String("path")
	format := parsed.String("format")
	info, err := credentials.Extract(explicitPath)
	if format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err != nil {
			_ = encoder.Encode(map[string]any{
				"ok":          false,
				"error":       err.Error(),
				"tried_paths": info.TriedPaths,
			})
			return 1
		}
		_ = encoder.Encode(map[string]any{
			"ok":          true,
			"source":      info.SourcePath,
			"source_type": info.SourceType,
			"key":         credentials.Redact(info.APIKey),
			"length":      len(info.APIKey),
		})
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "Error: %s\n", err)
		if len(info.TriedPaths) > 0 {
			fmt.Fprintln(stderr, "\nTried paths:")
			for _, path := range info.TriedPaths {
				fmt.Fprintf(stderr, " - %s\n", path)
			}
		}
		return 1
	}
	fmt.Fprintln(stdout, "Windsurf API key found")
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "Key: %s\n", credentials.Redact(info.APIKey))
	fmt.Fprintf(stdout, "Length: %d\n", len(info.APIKey))
	fmt.Fprintf(stdout, "Source: %s\n", info.SourcePath)
	return 0
}

func runParsedDoctor(parsed ParsedCommand, stdout, _ io.Writer) int {
	project := cwd()
	if parsed.Present["project"] {
		project = parsed.String("project")
	}
	format := parsed.String("format")
	absProject, projectErr := filepath.Abs(project)
	rgPath, rgSource, rgErr := executor.FindRipgrepWithSource()
	keyInfo, keyErr := credentials.FindAPIKey()
	projectOK := projectErr == nil && dirExists(absProject)
	rgOK := rgErr == nil
	credentialsOK := keyErr == nil
	report := map[string]any{
		"ok": projectOK && rgOK && credentialsOK,
		"project": map[string]any{
			"path":   absProject,
			"exists": projectOK,
		},
		"ripgrep": map[string]any{
			"ok":     rgOK,
			"path":   rgPath,
			"source": rgSource,
			"error":  errorString(rgErr),
		},
		"credentials": map[string]any{
			"ok":          keyErr == nil,
			"source":      keyInfo.SourcePath,
			"source_type": keyInfo.SourceType,
			"key":         credentials.Redact(keyInfo.APIKey),
			"error":       errorString(keyErr),
			"candidates":  credentials.SourceCandidates(),
		},
		"version": map[string]string{
			"version": version.Version,
			"commit":  version.Commit,
			"date":    version.Date,
		},
	}
	if format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(report)
		return 0
	}

	fmt.Fprintln(stdout, "fast-context doctor")
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "project: %s\n", absProject)
	fmt.Fprintf(stdout, "project_exists: %t\n", projectOK)
	if rgErr == nil {
		fmt.Fprintf(stdout, "ripgrep: ok (%s)\n", rgPath)
	} else {
		fmt.Fprintf(stdout, "ripgrep: missing (%s)\n", rgErr)
	}
	if keyErr == nil {
		fmt.Fprintf(stdout, "credentials: ok (%s, %s)\n", keyInfo.SourceType, keyInfo.SourcePath)
		fmt.Fprintf(stdout, "key: %s\n", credentials.Redact(keyInfo.APIKey))
	} else {
		fmt.Fprintf(stdout, "credentials: missing (%s)\n", keyErr)
	}
	fmt.Fprintf(stdout, "insecure_tls: %t\n", os.Getenv("FC_INSECURE_TLS") == "1")
	return 0
}

func splitFlag(argument string) (name, value string, hasValue bool) {
	if index := strings.Index(argument, "="); index >= 0 {
		return argument[:index], argument[index+1:], true
	}
	return argument, "", false
}

func parseDuration(raw string) (time.Duration, error) {
	if milliseconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(milliseconds) * time.Millisecond, nil
	}
	return time.ParseDuration(raw)
}

func cwd() string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workingDirectory
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func usageError(stderr io.Writer, message string) int {
	fmt.Fprintf(stderr, "Error: %s\n", message)
	return 2
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "fast-context %s (%s, %s)\n", version.Version, version.Commit, version.Date)
}
