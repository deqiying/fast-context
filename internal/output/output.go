package output

import (
	"errors"
	"fmt"
	"strings"

	"github.com/deqiying/fast-context/internal/search"
)

func FormatText(result search.Result, opts search.Options) string {
	var b strings.Builder
	files := result.Files
	if len(files) == 0 && len(result.RGPatterns) == 0 {
		if result.Raw != "" {
			return "No relevant files found.\n\nRaw response:\n" + result.Raw + "\n"
		}
		return "No relevant files found.\n"
	}

	fmt.Fprintf(&b, "Found %d relevant files.\n\n", len(files))
	for i, file := range files {
		fmt.Fprintf(&b, "  [%d/%d] %s", i+1, len(files), displayPath(file))
		if len(file.Ranges) > 0 {
			ranges := make([]string, 0, len(file.Ranges))
			for _, r := range file.Ranges {
				ranges = append(ranges, fmt.Sprintf("L%d-%d", r.Start, r.End))
			}
			fmt.Fprintf(&b, " (%s)", strings.Join(ranges, ", "))
		}
		b.WriteByte('\n')
	}
	if len(result.RGPatterns) > 0 {
		fmt.Fprintf(&b, "\ngrep keywords: %s\n", strings.Join(result.RGPatterns, ", "))
	}
	meta := result.Meta
	if meta.MaxTurns == 0 {
		meta.MaxTurns = opts.MaxTurns
		meta.MaxResults = opts.MaxResults
		meta.MaxCommands = opts.MaxCommands
		meta.TimeoutMS = opts.Timeout.Milliseconds()
	}
	fmt.Fprintf(&b, "\n[config] tree_depth=%d, tree_size=%.1fKB, max_turns=%d, max_results=%d, max_commands=%d, timeout_ms=%d",
		meta.TreeDepth, meta.TreeSizeKB, meta.MaxTurns, meta.MaxResults, meta.MaxCommands, meta.TimeoutMS)
	if meta.FellBack {
		b.WriteString(" (fell back from requested depth)")
	}
	if len(opts.ExcludePaths) > 0 {
		fmt.Fprintf(&b, ", exclude_paths=[%s]", strings.Join(opts.ExcludePaths, ", "))
	}
	b.WriteByte('\n')
	return b.String()
}

func FormatError(err error, opts search.Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Error: %s\n", err)
	code := "UNKNOWN"
	type coded interface{ Code() string }
	var c coded
	if errors.As(err, &c) {
		code = c.Code()
	}
	fmt.Fprintf(&b, "\n[diagnostic] error_type=%s\n", code)
	fmt.Fprintf(&b, "[config] tree_depth=%d, max_turns=%d, max_results=%d, max_commands=%d, timeout_ms=%d\n",
		opts.TreeDepth, opts.MaxTurns, opts.MaxResults, opts.MaxCommands, opts.Timeout.Milliseconds())
	switch code {
	case "PAYLOAD_TOO_LARGE", "TIMEOUT":
		b.WriteString("[hint] Try reducing --tree-depth or --max-turns, adding --exclude, or narrowing --project.\n")
	case "AUTH_ERROR":
		b.WriteString("[hint] The API key may be expired or revoked. Run `fast-context key extract` or set WINDSURF_API_KEY.\n")
	case "RATE_LIMITED":
		b.WriteString("[hint] Rate limited. Wait a moment and retry.\n")
	default:
		b.WriteString("[hint] Run `fast-context doctor` to check credentials, ripgrep, and project path.\n")
	}
	return b.String()
}

func ErrorJSON(err error) map[string]any {
	code := "UNKNOWN"
	type coded interface{ Code() string }
	var c coded
	if errors.As(err, &c) {
		code = c.Code()
	}
	return map[string]any{
		"error": map[string]any{
			"message": err.Error(),
			"code":    code,
		},
	}
}

func displayPath(file search.ResultFile) string {
	if file.FullPath != "" {
		return file.FullPath
	}
	return file.Path
}
