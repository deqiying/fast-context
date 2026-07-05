package output

import (
	"errors"
	"fmt"
	"strings"

	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/search"
)

func FormatText(result search.Result, opts search.Options) string {
	var b strings.Builder
	files := result.Files

	if len(files) == 0 && len(result.RGPatterns) == 0 {
		return formatNoRelevantFilesFound(result, opts)
	}

	if len(result.RetryNotes) > 0 {
		b.WriteString(strings.Join(result.RetryNotes, "\n"))
		b.WriteString("\n\n")
	}

	n := len(files)
	if n > 0 {
		if result.GrepAdded > 0 {
			fmt.Fprintf(&b, "Found %d relevant files (%d from AI search, %d from grep keyword expansion).\n", n, n-result.GrepAdded, result.GrepAdded)
		} else {
			fmt.Fprintf(&b, "Found %d relevant files.\n", n)
		}
	} else {
		b.WriteString("No files found.\n")
	}

	includeSnippets := opts.IncludeSnippets
	for i, file := range files {
		rangesStr := ""
		if len(file.Ranges) > 0 {
			ranges := make([]string, 0, len(file.Ranges))
			for _, r := range file.Ranges {
				ranges = append(ranges, fmt.Sprintf("L%d-%d", r.Start, r.End))
			}
			rangesStr = strings.Join(ranges, ", ")
		} else if file.FromGrep {
			rangesStr = "grep match"
		}
		label := ""
		if file.FromGrep {
			label = " [grep expanded]"
		}
		if !includeSnippets {
			fmt.Fprintf(&b, "  [%d/%d] %s (%s)%s\n", i+1, n, displayPath(file), rangesStr, label)
			continue
		}
		fmt.Fprintf(&b, "\n--- [%d/%d] %s (%s)%s ---\n", i+1, n, displayPath(file), rangesStr, label)
		if len(file.Snippets) > 0 {
			for _, s := range file.Snippets {
				b.WriteString(s)
				b.WriteByte('\n')
			}
		} else {
			b.WriteString("(code snippet omitted — output budget reached)\n")
		}
	}

	if len(result.RGPatterns) > 0 {
		fmt.Fprintf(&b, "\ngrep keywords: %s\n", strings.Join(result.RGPatterns, ", "))
	}

	b.WriteByte('\n')
	b.WriteString(configLine(result, opts))
	b.WriteByte('\n')
	return b.String()
}

func configLine(result search.Result, opts search.Options) string {
	meta := result.Meta
	if meta.MaxTurns == 0 {
		meta.MaxTurns = opts.MaxTurns
		meta.MaxResults = opts.MaxResults
		meta.MaxCommands = opts.MaxCommands
		meta.TimeoutMS = opts.Timeout.Milliseconds()
	}
	var b strings.Builder
	projectPath := result.ProjectRoot
	if projectPath == "" {
		projectPath = meta.ProjectRoot
	}
	if projectPath != "" {
		fmt.Fprintf(&b, "[config] project_path=%s, tree_depth=%d", projectPath, meta.TreeDepth)
	} else {
		fmt.Fprintf(&b, "[config] tree_depth=%d", meta.TreeDepth)
	}
	if meta.FellBack {
		b.WriteString(" (fell back from requested depth)")
	}
	if meta.AutoDepth {
		b.WriteString(" (auto)")
	}
	if meta.HotspotDepth > 0 {
		fmt.Fprintf(&b, ", hotspot_depth=%d", meta.HotspotDepth)
	}
	fmt.Fprintf(&b, ", tree_size=%.1fKB, max_turns=%d, max_results=%d, max_commands=%d, timeout_ms=%d",
		meta.TreeSizeKB, meta.MaxTurns, meta.MaxResults, meta.MaxCommands, meta.TimeoutMS)
	if meta.Strategy != "" {
		fmt.Fprintf(&b, ", strategy=%s", meta.Strategy)
	}
	if len(opts.ExcludePaths) > 0 {
		fmt.Fprintf(&b, ", exclude_paths=[%s]", strings.Join(opts.ExcludePaths, ", "))
	}
	if result.GrepAdded > 0 {
		fmt.Fprintf(&b, ", grep_expanded=%d", result.GrepAdded)
	}
	return b.String()
}

func formatNoRelevantFilesFound(result search.Result, opts search.Options) string {
	parts := []string{"No relevant files found."}
	raw := result.Raw
	if raw != "" {
		const maxRaw = 500
		truncated := raw
		if len(raw) > maxRaw {
			truncated = raw[:maxRaw] + "\n...[raw_response truncated]..."
		}
		parts = append(parts, "", "Raw response:\n"+truncated)
		if len(raw) > maxRaw {
			parts = append(parts, fmt.Sprintf("[diagnostic] raw_response_truncated=true, raw_response_chars=%d", len(raw)))
		}
	} else {
		parts = append(parts, "[diagnostic] raw_response_empty=true")
	}

	meta := result.Meta
	diag := fmt.Sprintf("[diagnostic] tree_depth_used=%d", meta.TreeDepth)
	if meta.FellBack {
		diag += " (fell back from requested depth)"
	}
	if meta.HotspotDepth > 0 {
		diag += fmt.Sprintf(", hotspot_depth=%d", meta.HotspotDepth)
	}
	diag += fmt.Sprintf(", tree_size=%.1fKB", meta.TreeSizeKB)
	if meta.Strategy != "" {
		diag += ", strategy=" + meta.Strategy
	}
	parts = append(parts, diag)
	if len(meta.HotDirs) > 0 {
		parts = append(parts, fmt.Sprintf("[diagnostic] hot_dirs=[%s]", strings.Join(meta.HotDirs, ", ")))
	}

	parts = append(parts, configLine(result, opts))
	parts = append(parts, result.RetryNotes...)
	parts = append(parts,
		"[hint] The remote model returned no parseable file paths. For large or noisy repos, narrow --project to a likely source subtree such as backend, server, src, app, or packages/<name>.",
		fmt.Sprintf("[hint] Also add --exclude for generated/noisy directories when present, e.g. [%s].", strings.Join(config.LargeRepoHintExcludes, ", ")),
		"[hint] After fast-context returns candidates, verify them with rg/readfile and concrete path:line evidence.",
	)
	return strings.Join(parts, "\n") + "\n"
}

func FormatError(err error, opts search.Options, result search.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Error: %s\n", err)
	code := "UNKNOWN"
	type coded interface{ Code() string }
	var c coded
	if errors.As(err, &c) {
		code = c.Code()
	} else if result.Meta.ErrorCode != "" {
		code = result.Meta.ErrorCode
	}
	meta := result.Meta
	fmt.Fprintf(&b, "\n[diagnostic] error_type=%s", code)
	if meta.TreeDepth > 0 || meta.TreeSizeKB > 0 {
		fmt.Fprintf(&b, ", tree_depth_used=%d, tree_size=%.1fKB", meta.TreeDepth, meta.TreeSizeKB)
		if meta.FellBack {
			b.WriteString(" (auto fell back from requested depth)")
		}
		if meta.ContextTrimmed {
			b.WriteString(", context_trimmed=true")
		}
	}
	b.WriteByte('\n')
	if meta.ProjectRoot != "" {
		fmt.Fprintf(&b, "[diagnostic] project_path=%s\n", meta.ProjectRoot)
	}
	fmt.Fprintf(&b, "[config] tree_depth=%d, max_turns=%d, max_results=%d, max_commands=%d, timeout_ms=%d\n",
		opts.TreeDepth, opts.MaxTurns, opts.MaxResults, opts.MaxCommands, opts.Timeout.Milliseconds())
	switch code {
	case "PAYLOAD_TOO_LARGE", "TIMEOUT":
		b.WriteString("[hint] Payload/timeout error. Try: reduce --tree-depth, reduce --max-turns, add --exclude, or narrow --project to a subdirectory.\n")
	case "AUTH_ERROR":
		b.WriteString("[hint] Authentication error. The API key may be expired or revoked. Run `fast-context key extract` or set a fresh WINDSURF_API_KEY.\n")
	case "RATE_LIMITED":
		b.WriteString("[hint] Rate limited. Wait a moment and retry.\n")
	default:
		b.WriteString("[hint] Run `fast-context doctor` to check credentials, ripgrep, and project path.\n")
	}
	for _, note := range result.RetryNotes {
		b.WriteString(note)
		b.WriteByte('\n')
	}
	return b.String()
}

func ErrorJSON(err error, result search.Result) map[string]any {
	code := "UNKNOWN"
	type coded interface{ Code() string }
	var c coded
	if errors.As(err, &c) {
		code = c.Code()
	} else if result.Meta.ErrorCode != "" {
		code = result.Meta.ErrorCode
	}
	out := map[string]any{
		"error": map[string]any{
			"message": err.Error(),
			"code":    code,
		},
	}
	if result.Meta.TreeDepth > 0 || result.Meta.TreeSizeKB > 0 {
		out["meta"] = result.Meta
	}
	if len(result.RetryNotes) > 0 {
		out["retry_notes"] = result.RetryNotes
	}
	return out
}

func displayPath(file search.ResultFile) string {
	if file.FullPath != "" {
		return file.FullPath
	}
	return file.Path
}
