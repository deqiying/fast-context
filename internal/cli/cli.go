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

	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/credentials"
	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/output"
	"github.com/deqiying/fast-context/internal/search"
	"github.com/deqiying/fast-context/internal/version"
	"github.com/deqiying/fast-context/internal/windsurf"
)

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRootHelp(stdout)
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		printRootHelp(stdout)
		return 0
	case "--version", "-v":
		printVersion(stdout)
		return 0
	case "search":
		return runSearch(ctx, args[1:], stdout, stderr)
	case "key":
		return runKey(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "skills":
		return runSkills(args[1:], stdout, stderr)
	case "version":
		printVersion(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printRootHelp(stderr)
		return 2
	}
}

func runSearch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	rt := config.ReadRuntime()
	opts := search.Options{
		ProjectRoot: cwd(),
		TreeDepth:   config.DefaultTreeDepth,
		MaxTurns:    rt.MaxTurns,
		MaxCommands: rt.MaxCommands,
		MaxResults:  config.DefaultMaxResults,
		Timeout:     rt.Timeout,
		Format:      "text",

		RepoMapMode:          rt.RepoMapMode,
		BootstrapEnabled:     rt.BootstrapEnabled,
		BootstrapTreeDepth:   rt.BootstrapTreeDepth,
		BootstrapMaxTurns:    rt.BootstrapMaxTurns,
		BootstrapMaxCommands: rt.BootstrapMaxCommands,
		HotspotTopK:          rt.HotspotTopK,
		HotspotTreeDepth:     rt.HotspotTreeDepth,
		HotspotMaxBytes:      rt.HotspotMaxBytes,
		IncludeSnippets:      rt.IncludeSnippets,
		Verbose:              rt.Debug,
	}

	var queryParts []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-h" || arg == "--help" {
			printSearchHelp(stdout)
			return 0
		}
		if !strings.HasPrefix(arg, "-") {
			queryParts = append(queryParts, arg)
			continue
		}
		name, value, hasValue := splitFlag(arg)
		readValue := func() (string, bool) {
			if hasValue {
				return value, true
			}
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}

		switch name {
		case "--project", "-p":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --project")
			}
			opts.ProjectRoot = v
		case "--tree-depth":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --tree-depth")
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return usageError(stderr, "invalid --tree-depth")
			}
			opts.TreeDepth = config.ClampInt(n, 0, 6)
		case "--max-turns":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --max-turns")
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return usageError(stderr, "invalid --max-turns")
			}
			opts.MaxTurns = config.ClampInt(n, 1, 5)
		case "--max-commands":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --max-commands")
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return usageError(stderr, "invalid --max-commands")
			}
			opts.MaxCommands = config.ClampInt(n, 1, 20)
		case "--max-results":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --max-results")
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return usageError(stderr, "invalid --max-results")
			}
			opts.MaxResults = config.ClampInt(n, 1, 30)
		case "--timeout":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --timeout")
			}
			d, err := parseDuration(v)
			if err != nil {
				return usageError(stderr, "invalid --timeout")
			}
			opts.Timeout = d
		case "--exclude":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --exclude")
			}
			opts.ExcludePaths = append(opts.ExcludePaths, v)
		case "--format":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --format")
			}
			if v != "text" && v != "json" {
				return usageError(stderr, "--format must be text or json")
			}
			opts.Format = v
		case "--include-snippets":
			opts.IncludeSnippets = true
		case "--repo-map-mode":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --repo-map-mode")
			}
			if v != "classic" && v != "bootstrap_hotspot" {
				return usageError(stderr, "--repo-map-mode must be classic or bootstrap_hotspot")
			}
			opts.RepoMapMode = v
		case "--no-bootstrap":
			opts.BootstrapEnabled = false
		case "--verbose":
			opts.Verbose = true
		default:
			return usageError(stderr, "unknown flag: "+name)
		}
	}

	opts.Query = strings.TrimSpace(strings.Join(queryParts, " "))
	if opts.Query == "" {
		return usageError(stderr, "missing search query")
	}
	if opts.Verbose {
		opts.Progress = func(msg string) {
			fmt.Fprintln(stderr, msg)
		}
	}

	client := windsurf.NewClient(nil)
	result, err := search.RunPipeline(ctx, opts, client)
	if opts.Format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err != nil {
			_ = enc.Encode(output.ErrorJSON(err, result))
			return 1
		}
		_ = enc.Encode(result)
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, output.FormatError(err, opts, result))
		return 1
	}
	fmt.Fprint(stdout, output.FormatText(result, opts))
	return 0
}

func runKey(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printKeyHelp(stdout)
		return 0
	}
	if args[0] != "extract" {
		return usageError(stderr, "unknown key command: "+args[0])
	}

	var explicitPath string
	format := "text"
	for i := 1; i < len(args); i++ {
		name, value, hasValue := splitFlag(args[i])
		readValue := func() (string, bool) {
			if hasValue {
				return value, true
			}
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch name {
		case "-h", "--help":
			printKeyHelp(stdout)
			return 0
		case "--path":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --path")
			}
			explicitPath = v
		case "--format":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --format")
			}
			if v != "text" && v != "json" {
				return usageError(stderr, "--format must be text or json")
			}
			format = v
		default:
			return usageError(stderr, "unknown flag: "+name)
		}
	}

	info, err := credentials.Extract(explicitPath)
	if format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err != nil {
			_ = enc.Encode(map[string]any{
				"ok":          false,
				"error":       err.Error(),
				"tried_paths": info.TriedPaths,
			})
			return 1
		}
		_ = enc.Encode(map[string]any{
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
	fmt.Fprintf(stdout, "Windsurf API key found\n\n")
	fmt.Fprintf(stdout, "Key: %s\n", credentials.Redact(info.APIKey))
	fmt.Fprintf(stdout, "Length: %d\n", len(info.APIKey))
	fmt.Fprintf(stdout, "Source: %s\n", info.SourcePath)
	return 0
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	project := cwd()
	format := "text"
	for i := 0; i < len(args); i++ {
		name, value, hasValue := splitFlag(args[i])
		readValue := func() (string, bool) {
			if hasValue {
				return value, true
			}
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch name {
		case "-h", "--help":
			printDoctorHelp(stdout)
			return 0
		case "--project", "-p":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --project")
			}
			project = v
		case "--format":
			v, ok := readValue()
			if !ok {
				return usageError(stderr, "missing value for --format")
			}
			if v != "text" && v != "json" {
				return usageError(stderr, "--format must be text or json")
			}
			format = v
		default:
			return usageError(stderr, "unknown flag: "+name)
		}
	}

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
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return 0
	}

	fmt.Fprintf(stdout, "fast-context doctor\n\n")
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

func splitFlag(arg string) (name, value string, hasValue bool) {
	if i := strings.Index(arg, "="); i >= 0 {
		return arg[:i], arg[i+1:], true
	}
	return arg, "", false
}

func parseDuration(raw string) (time.Duration, error) {
	if n, err := strconv.Atoi(raw); err == nil {
		return time.Duration(n) * time.Millisecond, nil
	}
	return time.ParseDuration(raw)
}

func cwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func usageError(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "Error: %s\n", msg)
	return 2
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "fast-context %s (%s, %s)\n", version.Version, version.Commit, version.Date)
}

func printRootHelp(w io.Writer) {
	fmt.Fprint(w, `fast-context is a CLI version of the fast-context MCP code search tool.

Usage:
  fast-context search <query> [flags]
  fast-context key extract [flags]
  fast-context doctor [flags]
  fast-context skills list [flags]
  fast-context skills show <skill> [flags]
  fast-context version
  fast-context --version
  fast-context -v

`)
}

func printSearchHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  fast-context search <query> [flags]

Flags:
  --project, -p <path>       Project root (default: current directory)
  --tree-depth <0..6>        Repo map depth; 0 = auto by project size (default: 3)
  --max-turns <1..5>         Search rounds (default: 3)
  --max-commands <1..20>     Local commands per round (default: 8)
  --max-results <1..30>      Maximum result files (default: 10)
  --timeout <duration>       Stream timeout, e.g. 30s or 30000 (default: 30s)
  --exclude <pattern>        Exclude path pattern; repeatable (defaults always applied)
  --include-snippets         Attach code snippets to results (45KB budget)
  --repo-map-mode <mode>     classic | bootstrap_hotspot (default: bootstrap_hotspot)
  --no-bootstrap             Skip the bootstrap keyword/hotspot pre-pass
  --format <text|json>       Output format (default: text)
  --verbose                  Print progress to stderr

Environment:
  FC_MAX_TURNS, FC_MAX_COMMANDS, FC_TIMEOUT_MS, FC_REPO_MAP_MODE,
  FC_BOOTSTRAP_ENABLED, FC_BOOTSTRAP_TREE_DEPTH, FC_BOOTSTRAP_MAX_TURNS,
  FC_BOOTSTRAP_MAX_COMMANDS, FC_HOTSPOT_TOP_K, FC_HOTSPOT_TREE_DEPTH,
  FC_HOTSPOT_MAX_BYTES, FC_INCLUDE_SNIPPETS, FAST_CONTEXT_DEBUG

`)
}

func printKeyHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  fast-context key extract [flags]

Flags:
  --path <path>              Read a specific .toml or state.vscdb source
  --format <text|json>       Output format (default: text)

`)
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  fast-context doctor [flags]

Flags:
  --project, -p <path>       Project root to inspect
  --format <text|json>       Output format (default: text)

`)
}
