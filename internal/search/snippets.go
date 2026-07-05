package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CodeBudget caps snippet output at ~45KB code + ~5KB metadata ≈ 50KB total.
const CodeBudget = 45000

var extLangMap = map[string]string{
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".jsx": "javascript",
	".py": "python", ".go": "go", ".rs": "rust", ".java": "java",
	".rb": "ruby", ".vue": "vue", ".c": "c", ".h": "c",
	".cpp": "cpp", ".hpp": "cpp", ".cs": "csharp", ".php": "php",
	".swift": "swift", ".kt": "kotlin", ".sh": "bash",
	".yaml": "yaml", ".yml": "yaml", ".json": "json", ".toml": "toml",
	".sql": "sql", ".html": "html", ".css": "css", ".scss": "scss",
}

// readCodeSnippets reads line ranges as fenced, line-numbered blocks within
// the remaining character budget. Files without ranges (grep-expanded) get
// their first 20 lines. Returns the snippets and characters consumed.
func readCodeSnippets(filePath string, ranges []LineRange, budget int) ([]string, int) {
	lang := extLangMap[strings.ToLower(filepath.Ext(filePath))]
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, 0
	}
	lines := strings.Split(string(data), "\n")

	effectiveRanges := ranges
	if len(effectiveRanges) == 0 {
		end := len(lines)
		if end > 20 {
			end = 20
		}
		effectiveRanges = []LineRange{{Start: 1, End: end}}
	}

	var snippets []string
	used := 0
	for _, r := range effectiveRanges {
		start := r.Start
		if start < 1 {
			start = 1
		}
		end := r.End
		if end > len(lines) {
			end = len(lines)
		}
		if start > end {
			continue
		}
		var b strings.Builder
		b.WriteString("```")
		b.WriteString(lang)
		b.WriteByte('\n')
		for i := start; i <= end; i++ {
			fmt.Fprintf(&b, "%4d | %s", i, lines[i-1])
			b.WriteByte('\n')
		}
		b.WriteString("```")
		block := b.String()

		if used+len(block) > budget {
			remaining := budget - used - 100
			if remaining > 200 {
				truncated := block[:remaining] + "\n... [truncated]```"
				snippets = append(snippets, truncated)
				used += len(truncated)
			}
			return snippets, used
		}
		snippets = append(snippets, block)
		used += len(block)
	}
	return snippets, used
}
