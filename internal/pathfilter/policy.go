// Package pathfilter defines the immutable path policy shared by search
// stages. Patterns are anchored to the original project root so a narrowed
// retry root cannot silently reinterpret a user's excludes.
package pathfilter

import (
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Policy is an immutable, original-root anchored exclude policy. The slices
// and regular expressions are private and never changed after New returns.
type Policy struct {
	originalRoot string
	resolvedRoot string
	patterns     []compiledPattern
	caseFold     bool
}

type compiledPattern struct {
	raw         string
	basename    bool
	outsideRoot bool
	hasGlob     bool
	fixedPrefix string
	re          *regexp.Regexp
	ancestorRe  *regexp.Regexp
}

// New creates a policy anchored to root. effective is copied and normalized;
// callers should pass the already merged default and user excludes.
func New(root string, effective []string) (*Policy, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve policy root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	st, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat policy root: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("policy root is not a directory: %s", root)
	}

	resolvedRoot := absRoot
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		resolvedRoot = filepath.Clean(resolved)
	}

	caseFold := runtime.GOOS == "windows"
	patterns := make([]compiledPattern, 0, len(effective))
	seen := map[string]bool{}
	for _, raw := range effective {
		pattern, ok := compilePattern(absRoot, raw, caseFold)
		if !ok || seen[pattern.raw+"\x00"+fmt.Sprint(pattern.outsideRoot)] {
			continue
		}
		seen[pattern.raw+"\x00"+fmt.Sprint(pattern.outsideRoot)] = true
		patterns = append(patterns, pattern)
	}
	return &Policy{
		originalRoot: absRoot,
		resolvedRoot: resolvedRoot,
		patterns:     patterns,
		caseFold:     caseFold,
	}, nil
}

// OriginalRoot returns the lexical absolute root used to anchor this policy.
func (p *Policy) OriginalRoot() string {
	if p == nil {
		return ""
	}
	return p.originalRoot
}

// CaseInsensitive reports whether policy matching follows case-insensitive
// filesystem semantics. Ripgrep callers use this to select --iglob.
func (p *Policy) CaseInsensitive() bool {
	return p != nil && p.caseFold
}

// ExcludedVirtual resolves a restricted-tool /codebase path against the
// current active root while preserving the policy's original-root anchor.
func (p *Policy) ExcludedVirtual(activeRoot, virtual string) bool {
	if p == nil {
		return false
	}
	active, ok := p.absolutePath(activeRoot)
	if !ok {
		return true
	}
	if _, inside := relativeInside(p.originalRoot, active); !inside {
		return true
	}
	normalized := strings.ReplaceAll(virtual, "\\", "/")
	var rel string
	switch {
	case normalized == "/codebase" || normalized == "codebase":
		rel = "."
	case strings.HasPrefix(normalized, "/codebase/"):
		rel = strings.TrimPrefix(normalized, "/codebase/")
	case strings.HasPrefix(normalized, "codebase/"):
		rel = strings.TrimPrefix(normalized, "codebase/")
	case filepath.IsAbs(virtual) || filepath.VolumeName(virtual) != "":
		return true
	default:
		rel = virtual
	}
	candidate := filepath.Clean(filepath.Join(active, filepath.FromSlash(rel)))
	if _, inside := relativeInside(active, candidate); !inside {
		return true
	}
	return p.Excluded(candidate)
}

// Excluded reports whether path is excluded. Relative paths are interpreted
// relative to the original root. Both the lexical path and its resolved
// symlink target are checked; an unresolved leaf still receives lexical
// matching.
func (p *Policy) Excluded(path string) bool {
	if p == nil || p.originalRoot == "" {
		return false
	}
	lexical, ok := p.absolutePath(path)
	if !ok {
		return false
	}
	if p.excludedAt(lexical, p.originalRoot) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return false
	}
	resolved = filepath.Clean(resolved)
	if _, inside := relativeInside(p.resolvedRoot, resolved); !inside {
		return true
	}
	return p.excludedAt(resolved, p.resolvedRoot)
}

// RGGlobs returns positive ripgrep glob patterns. Callers should pass each
// returned value as a negated glob (for example, "!"+pattern). Patterns are
// rebased from the original root to activeRoot for narrow retry searches.
func (p *Policy) RGGlobs(activeRoot string) []string {
	if p == nil || p.originalRoot == "" {
		return nil
	}
	active, ok := p.absolutePath(activeRoot)
	if !ok {
		return nil
	}
	activeRel, inside := relativeInside(p.originalRoot, active)
	if !inside {
		return nil
	}
	activeRel = filepath.ToSlash(activeRel)
	if activeRel == "." {
		activeRel = ""
	} else if p.caseFold {
		activeRel = strings.ToLower(activeRel)
	}

	var out []string
	seen := map[string]bool{}
	appendGlob := func(pattern string) {
		if pattern == "" || seen[pattern] {
			return
		}
		seen[pattern] = true
		out = append(out, pattern)
	}
	for _, pattern := range p.patterns {
		if pattern.outsideRoot {
			continue
		}
		for _, rebased := range rebasePattern(pattern, activeRel) {
			if !pattern.basename {
				rebased = anchorRGGlob(active, rebased)
			}
			appendGlob(rebased)
		}
	}
	return out
}

func anchorRGGlob(activeRoot, pattern string) string {
	if pattern == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(activeRoot, filepath.FromSlash(pattern)))
}

func (p *Policy) absolutePath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return filepath.Clean(path), true
	}
	return filepath.Clean(filepath.Join(p.originalRoot, path)), true
}

func (p *Policy) excludedAt(path, root string) bool {
	rel, inside := relativeInside(root, path)
	if !inside {
		return false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return false
	}
	base := filepath.Base(filepath.FromSlash(rel))
	if p.caseFold {
		rel = strings.ToLower(rel)
		base = strings.ToLower(base)
	}
	for _, pattern := range p.patterns {
		if !pattern.outsideRoot && pattern.matches(rel, base) {
			return true
		}
	}
	return false
}

func relativeInside(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func compilePattern(root, raw string, caseFold bool) (compiledPattern, bool) {
	pattern := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if pattern == "" {
		return compiledPattern{}, false
	}
	pattern = strings.TrimPrefix(pattern, "./")

	hasGlob := strings.ContainsAny(pattern, "*?")
	outside := false
	if isPatternAbsolute(pattern) {
		rootSlash := filepath.ToSlash(root)
		if samePathPrefix(pattern, rootSlash) {
			pattern = strings.TrimPrefix(pattern[len(rootSlash):], "/")
		} else {
			outside = true
		}
	}
	pattern = strings.Trim(pattern, "/")
	if pattern == "" || pattern == "." {
		return compiledPattern{}, false
	}
	pattern = filepath.ToSlash(filepath.Clean(filepath.FromSlash(pattern)))
	if pattern == "." || pattern == "" {
		return compiledPattern{}, false
	}
	if caseFold {
		pattern = strings.ToLower(pattern)
	}
	basename := !strings.Contains(pattern, "/")
	if outside {
		basename = false
	}
	re := regexp.MustCompile(globRegex(pattern))
	var ancestorRe *regexp.Regexp
	if strings.HasSuffix(pattern, "/**") {
		ancestorRe = regexp.MustCompile(globRegex(strings.TrimSuffix(pattern, "/**")))
	}
	fixedPrefix := pattern
	if i := strings.IndexAny(pattern, "*?"); i >= 0 {
		fixedPrefix = strings.TrimSuffix(pattern[:i], "/")
	}
	return compiledPattern{
		raw:         pattern,
		basename:    basename,
		outsideRoot: outside,
		hasGlob:     hasGlob,
		fixedPrefix: fixedPrefix,
		re:          re,
		ancestorRe:  ancestorRe,
	}, true
}

func isPatternAbsolute(pattern string) bool {
	return strings.HasPrefix(pattern, "/") || filepath.IsAbs(pattern) || filepath.VolumeName(pattern) != ""
}

func samePathPrefix(path, root string) bool {
	if path == root {
		return true
	}
	if strings.HasPrefix(path, root+"/") {
		return true
	}
	return filepath.VolumeName(root) != "" && strings.HasPrefix(strings.ToLower(path), strings.ToLower(root)+"/")
}

func (p compiledPattern) matches(rel, base string) bool {
	if p.outsideRoot {
		return false
	}
	if p.basename {
		if p.re.MatchString(base) {
			return true
		}
		for _, part := range strings.Split(rel, "/") {
			if p.re.MatchString(part) {
				return true
			}
		}
		return false
	}
	if p.re.MatchString(rel) {
		return true
	}
	if !p.hasGlob && p.fixedPrefix != "" && (rel == p.fixedPrefix || strings.HasPrefix(rel, p.fixedPrefix+"/")) {
		return true
	}
	if p.ancestorRe != nil && p.ancestorRe.MatchString(rel) {
		return true
	}
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		if p.re.MatchString(strings.Join(parts[:i], "/")) {
			return true
		}
	}
	return false
}

func rebasePattern(pattern compiledPattern, activeRel string) []string {
	if pattern.basename {
		return expandRGBasename(pattern.raw)
	}
	raw := pattern.raw
	if activeRel == "" {
		return expandRGScoped(raw)
	}
	if raw == activeRel || (!pattern.hasGlob && strings.HasPrefix(activeRel, raw+"/")) {
		return []string{"**"}
	}
	if !pattern.hasGlob {
		if strings.HasPrefix(raw, activeRel+"/") {
			return expandRGScoped(strings.TrimPrefix(raw, activeRel+"/"))
		}
		return nil
	}
	return rebaseGlobPattern(raw, activeRel)
}

// rebaseGlobPattern consumes the active-root path against the original-root
// glob, then returns only the unmatched suffix. This avoids turning a crossing
// glob such as src/**/generated/** into the catch-all **.
func rebaseGlobPattern(pattern, activeRel string) []string {
	patternParts := strings.Split(pattern, "/")
	activeParts := strings.Split(activeRel, "/")
	type state struct {
		patternIndex int
		activeIndex  int
	}
	visited := map[state]bool{}
	seen := map[string]bool{}
	var out []string
	appendRemainder := func(index int) {
		remainder := strings.Join(patternParts[index:], "/")
		if remainder == "" {
			remainder = "**"
		}
		for _, expanded := range expandRGScoped(remainder) {
			if !seen[expanded] {
				seen[expanded] = true
				out = append(out, expanded)
			}
		}
	}
	var walk func(int, int)
	walk = func(patternIndex, activeIndex int) {
		current := state{patternIndex: patternIndex, activeIndex: activeIndex}
		if visited[current] {
			return
		}
		visited[current] = true
		if activeIndex == len(activeParts) {
			appendRemainder(patternIndex)
			return
		}
		if patternIndex == len(patternParts) {
			return
		}
		component := patternParts[patternIndex]
		if component == "**" {
			walk(patternIndex+1, activeIndex)
			walk(patternIndex, activeIndex+1)
			return
		}
		matched, err := pathpkg.Match(component, activeParts[activeIndex])
		if err == nil && matched {
			walk(patternIndex+1, activeIndex+1)
		}
	}
	walk(0, 0)
	return out
}

func expandRGBasename(pattern string) []string {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if pattern == "" {
		return nil
	}
	if strings.HasSuffix(pattern, "/**") {
		return []string{pattern, "**/" + pattern}
	}
	return []string{pattern, pattern + "/**", "**/" + pattern, "**/" + pattern + "/**"}
}

func expandRGScoped(pattern string) []string {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if pattern == "" {
		return nil
	}
	if pattern == "**" || strings.HasSuffix(pattern, "/**") {
		return []string{pattern}
	}
	return []string{pattern, pattern + "/**"}
}

func globRegex(pattern string) string {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteByte('$')
	return b.String()
}
