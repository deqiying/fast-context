package repomap

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const MaxTreeBytes = 250 * 1024

type Map struct {
	Tree      string
	Depth     int
	SizeBytes int
	FellBack  bool
}

func Build(projectRoot string, targetDepth int, excludePaths []string) Map {
	if targetDepth < 1 {
		targetDepth = 1
	}
	if targetDepth > 6 {
		targetDepth = 6
	}
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return fallback(projectRoot, excludePaths)
	}
	excludes := compileExcludes(excludePaths)
	for depth := targetDepth; depth >= 1; depth-- {
		tree, err := renderTree(root, "/codebase", depth, excludes)
		if err != nil {
			continue
		}
		size := len([]byte(tree))
		if size <= MaxTreeBytes {
			return Map{Tree: tree, Depth: depth, SizeBytes: size, FellBack: depth < targetDepth}
		}
	}
	return fallback(root, excludePaths)
}

func fallback(projectRoot string, excludePaths []string) Map {
	excludes := compileExcludes(excludePaths)
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		tree := "/codebase\n(empty or inaccessible)"
		return Map{Tree: tree, Depth: 0, SizeBytes: len([]byte(tree)), FellBack: true}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if matchesExclude(excludes, entry.Name(), entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("/codebase")
	for _, name := range names {
		b.WriteString("\n")
		b.WriteString("├── ")
		b.WriteString(name)
	}
	tree := b.String()
	return Map{Tree: tree, Depth: 0, SizeBytes: len([]byte(tree)), FellBack: true}
}

func renderTree(root, displayRoot string, maxDepth int, excludes []excludePattern) (string, error) {
	var b strings.Builder
	b.WriteString(displayRoot)
	err := writeTree(&b, root, root, "", 1, maxDepth, excludes)
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeTree(b *strings.Builder, root, dir, prefix string, depth, maxDepth int, excludes []excludePattern) error {
	if depth > maxDepth {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	filtered := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		rel, err := filepath.Rel(root, filepath.Join(dir, entry.Name()))
		if err != nil {
			rel = entry.Name()
		}
		rel = filepath.ToSlash(rel)
		if matchesExclude(excludes, entry.Name(), rel) {
			continue
		}
		filtered = append(filtered, entry)
	}
	for i, entry := range filtered {
		last := i == len(filtered)-1
		connector := "├── "
		nextPrefix := prefix + "│   "
		if last {
			connector = "└── "
			nextPrefix = prefix + "    "
		}
		b.WriteByte('\n')
		b.WriteString(prefix)
		b.WriteString(connector)
		b.WriteString(entry.Name())
		if entry.IsDir() {
			_ = writeTree(b, root, filepath.Join(dir, entry.Name()), nextPrefix, depth+1, maxDepth, excludes)
		}
	}
	return nil
}

type excludePattern struct {
	simple string
	re     *regexp.Regexp
}

func compileExcludes(patterns []string) []excludePattern {
	out := make([]excludePattern, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(filepath.ToSlash(pattern))
		if pattern == "" {
			continue
		}
		if !strings.ContainsAny(pattern, "*?") {
			out = append(out, excludePattern{simple: pattern})
			continue
		}
		out = append(out, excludePattern{re: regexp.MustCompile(globToRegex(pattern))})
	}
	return out
}

func matchesExclude(patterns []excludePattern, name, rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, pattern := range patterns {
		if pattern.simple != "" {
			if name == pattern.simple || rel == pattern.simple || strings.HasPrefix(rel, pattern.simple+"/") {
				return true
			}
			continue
		}
		if pattern.re != nil && (pattern.re.MatchString(name) || pattern.re.MatchString(rel)) {
			return true
		}
	}
	return false
}

func globToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString(".*")
			}
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	return b.String()
}
