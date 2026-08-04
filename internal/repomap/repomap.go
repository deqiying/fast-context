package repomap

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deqiying/fast-context/internal/pathfilter"
)

const MaxTreeBytes = 250 * 1024

type Map struct {
	Tree      string
	Depth     int
	SizeBytes int
	FellBack  bool
	AutoDepth bool
}

// SuggestTreeDepth mirrors upstream _suggestTreeDepth: pick a depth from the
// number of top-level entries (<500 → 4, ≤5000 → 3, >5000 → 2).
func SuggestTreeDepth(projectRoot string, policy *pathfilter.Policy) int {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return 3
	}
	count := 0
	for _, entry := range entries {
		if policy == nil || !policy.Excluded(filepath.Join(projectRoot, entry.Name())) {
			count++
		}
	}
	switch {
	case count < 500:
		return 4
	case count <= 5000:
		return 3
	default:
		return 2
	}
}

// Build renders the project tree with adaptive depth fallback. targetDepth 0
// means auto depth based on project size.
func Build(projectRoot string, targetDepth int, policy *pathfilter.Policy) Map {
	autoDepth := targetDepth == 0
	if autoDepth {
		targetDepth = SuggestTreeDepth(projectRoot, policy)
	}
	if targetDepth < 1 {
		targetDepth = 1
	}
	if targetDepth > 6 {
		targetDepth = 6
	}
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		m := fallback(projectRoot, policy)
		m.AutoDepth = autoDepth
		return m
	}
	for depth := targetDepth; depth >= 1; depth-- {
		tree, err := renderTree(root, "/codebase", depth, policy)
		if err != nil {
			continue
		}
		size := len(tree)
		if size <= MaxTreeBytes {
			return Map{Tree: tree, Depth: depth, SizeBytes: size, FellBack: depth < targetDepth, AutoDepth: autoDepth}
		}
	}
	m := fallback(root, policy)
	m.AutoDepth = autoDepth
	return m
}

// BuildSubtree renders a hotspot subtree rooted at /codebase/<dir>.
func BuildSubtree(projectRoot, dir string, levels int, policy *pathfilter.Policy) string {
	abs := filepath.Join(projectRoot, dir)
	vroot := "/codebase/" + filepath.ToSlash(dir)
	if policy != nil && policy.Excluded(abs) {
		return ""
	}
	tree, err := renderTree(abs, vroot, levels, policy)
	if err != nil {
		return vroot + "\n  (failed to generate subtree)"
	}
	return tree
}

// ListTopLevelDirs returns non-excluded top-level directory names, sorted.
func ListTopLevelDirs(projectRoot string, policy *pathfilter.Policy) []string {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if policy != nil && policy.Excluded(filepath.Join(projectRoot, entry.Name())) {
			continue
		}
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}

func fallback(projectRoot string, policy *pathfilter.Policy) Map {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		tree := "/codebase\n(empty or inaccessible)"
		return Map{Tree: tree, Depth: 0, SizeBytes: len(tree), FellBack: true}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if policy != nil && policy.Excluded(filepath.Join(projectRoot, entry.Name())) {
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
	return Map{Tree: tree, Depth: 0, SizeBytes: len(tree), FellBack: true}
}

func renderTree(root, displayRoot string, maxDepth int, policy *pathfilter.Policy) (string, error) {
	var b strings.Builder
	b.WriteString(displayRoot)
	err := writeTree(&b, root, "", 1, maxDepth, policy)
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeTree(b *strings.Builder, dir, prefix string, depth, maxDepth int, policy *pathfilter.Policy) error {
	if depth > maxDepth {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	// Upstream tree.mjs sorts directories first, then by name.
	sort.Slice(entries, func(i, j int) bool {
		iDir, jDir := entries[i].IsDir(), entries[j].IsDir()
		if iDir != jDir {
			return iDir
		}
		return entries[i].Name() < entries[j].Name()
	})
	filtered := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		if policy != nil && policy.Excluded(fullPath) {
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
			_ = writeTree(b, filepath.Join(dir, entry.Name()), nextPrefix, depth+1, maxDepth, policy)
		}
	}
	return nil
}
