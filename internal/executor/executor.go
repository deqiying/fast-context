package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultResultMaxLines = 50
	defaultLineMaxChars   = 250
)

type Command struct {
	Type       string   `json:"type"`
	Pattern    string   `json:"pattern,omitempty"`
	Path       string   `json:"path,omitempty"`
	File       string   `json:"file,omitempty"`
	Include    []string `json:"include,omitempty"`
	Exclude    []string `json:"exclude,omitempty"`
	StartLine  int      `json:"start_line,omitempty"`
	EndLine    int      `json:"end_line,omitempty"`
	Levels     int      `json:"levels,omitempty"`
	LongFormat bool     `json:"long_format,omitempty"`
	All        bool     `json:"all,omitempty"`
	TypeFilter string   `json:"type_filter,omitempty"`
}

type Executor struct {
	root                string
	rgPath              string
	CollectedRgPatterns []string
	mu                  sync.Mutex
}

func New(projectRoot string) (*Executor, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("project root is not a directory: %s", projectRoot)
	}
	rgPath, _ := FindRipgrep()
	return &Executor{root: root, rgPath: rgPath}, nil
}

func FindRipgrep() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("FC_RG_PATH")); configured != "" {
		if st, err := os.Stat(configured); err == nil && !st.IsDir() {
			return configured, nil
		}
		return "", fmt.Errorf("FC_RG_PATH is not executable: %s", configured)
	}
	path, err := exec.LookPath("rg")
	if err != nil {
		return "", errors.New("rg not found in PATH; install ripgrep or set FC_RG_PATH")
	}
	return path, nil
}

func (e *Executor) ExecToolCall(ctx context.Context, args map[string]Command) string {
	if len(args) == 0 {
		return "Error: missing or invalid tool args"
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		if strings.HasPrefix(key, "command") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "Error: missing commandN entries"
	}

	type item struct {
		key string
		out string
	}
	results := make([]item, len(keys))
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			results[i] = item{key: key, out: e.ExecCommand(ctx, args[key])}
		}(i, key)
	}
	wg.Wait()

	var b strings.Builder
	for _, result := range results {
		fmt.Fprintf(&b, "<%s_result>\n%s\n</%s_result>", result.key, result.out, result.key)
	}
	return b.String()
}

func (e *Executor) ExecCommand(ctx context.Context, cmd Command) string {
	switch cmd.Type {
	case "rg":
		return e.RG(ctx, cmd.Pattern, cmd.Path, cmd.Include, cmd.Exclude)
	case "readfile":
		return e.ReadFile(cmd.File, cmd.StartLine, cmd.EndLine)
	case "tree":
		return e.Tree(cmd.Path, cmd.Levels)
	case "ls":
		return e.LS(cmd.Path, cmd.LongFormat, cmd.All)
	case "glob":
		filter := cmd.TypeFilter
		if filter == "" {
			filter = "all"
		}
		return e.Glob(cmd.Pattern, cmd.Path, filter)
	default:
		return fmt.Sprintf("Error: unknown command type %q", cmd.Type)
	}
}

func (e *Executor) RG(ctx context.Context, pattern, virtualPath string, include, exclude []string) string {
	if pattern == "" {
		return "Error: missing or invalid pattern"
	}
	if virtualPath == "" {
		return "Error: missing or invalid path"
	}
	e.mu.Lock()
	e.CollectedRgPatterns = append(e.CollectedRgPatterns, pattern)
	e.mu.Unlock()

	if e.rgPath == "" {
		return "Error: rg not found in PATH; install ripgrep or set FC_RG_PATH"
	}
	realPath, err := e.realPath(virtualPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	if _, err := os.Stat(realPath); err != nil {
		return fmt.Sprintf("Error: path does not exist: %s", virtualPath)
	}

	args := []string{"--no-heading", "-n", "--max-count", "50", pattern, realPath}
	for _, g := range include {
		args = append(args, "--glob", g)
	}
	for _, g := range exclude {
		args = append(args, "--glob", "!"+g)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(cmdCtx, e.rgPath, args...)
	command.Env = filteredEnv(os.Environ(), "RIPGREP_CONFIG_PATH")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err = command.Run()
	if err == nil {
		out := stdout.String()
		if out == "" {
			return "(no matches)"
		}
		return truncate(e.remap(out))
	}
	if exitErr := new(exec.ExitError); errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "(no matches)"
	}
	if stderr.Len() > 0 {
		return truncate(e.remap(stderr.String()))
	}
	return "Error: " + err.Error()
}

func (e *Executor) ReadFile(virtualFile string, startLine, endLine int) string {
	if virtualFile == "" {
		return "Error: missing or invalid file path"
	}
	realPath, err := e.realPath(virtualFile)
	if err != nil {
		return "Error: " + err.Error()
	}
	st, err := os.Stat(realPath)
	if err != nil || st.IsDir() {
		return fmt.Sprintf("Error: file not found: %s", virtualFile)
	}
	data, err := os.ReadFile(realPath)
	if err != nil {
		return "Error: " + err.Error()
	}

	lines := strings.Split(string(data), "\n")
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		return ""
	}
	var b strings.Builder
	for i := startLine; i <= endLine; i++ {
		fmt.Fprintf(&b, "%d:%s", i, lines[i-1])
		if i != endLine {
			b.WriteByte('\n')
		}
	}
	return truncate(b.String())
}

func (e *Executor) Tree(virtualPath string, levels int) string {
	if virtualPath == "" {
		return "Error: missing or invalid path"
	}
	realPath, err := e.realPath(virtualPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	st, err := os.Stat(realPath)
	if err != nil || !st.IsDir() {
		return fmt.Sprintf("Error: dir not found: %s", virtualPath)
	}
	if levels <= 0 {
		levels = 3
	}
	tree, err := buildTree(realPath, virtualPath, levels, nil)
	if err != nil {
		return fmt.Sprintf("Error: failed to generate tree for %s", virtualPath)
	}
	return truncate(e.remap(tree))
}

func (e *Executor) LS(virtualPath string, longFormat, allFiles bool) string {
	if virtualPath == "" {
		return "Error: missing or invalid path"
	}
	realPath, err := e.realPath(virtualPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	entries, err := os.ReadDir(realPath)
	if err != nil {
		return fmt.Sprintf("Error: dir not found: %s", virtualPath)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var lines []string
	if longFormat {
		lines = append(lines, fmt.Sprintf("total %d", len(entries)))
	}
	for _, entry := range entries {
		name := entry.Name()
		if !allFiles && strings.HasPrefix(name, ".") {
			continue
		}
		if !longFormat {
			lines = append(lines, name)
			continue
		}
		info, err := entry.Info()
		if err != nil {
			lines = append(lines, "?---------        ? "+name)
			continue
		}
		typeChar := "-"
		if info.IsDir() {
			typeChar = "d"
		}
		lines = append(lines, fmt.Sprintf("%srwxr-xr-x %8d %s", typeChar, info.Size(), name))
	}
	return truncate(strings.Join(lines, "\n"))
}

func (e *Executor) Glob(pattern, virtualPath, typeFilter string) string {
	if pattern == "" {
		return "Error: missing or invalid pattern"
	}
	if virtualPath == "" {
		return "Error: missing or invalid path"
	}
	realPath, err := e.realPath(virtualPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	if _, err := os.Stat(realPath); err != nil {
		return fmt.Sprintf("Error: path does not exist: %s", virtualPath)
	}

	var matches []string
	_ = filepath.WalkDir(realPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || len(matches) >= 100 {
			return nil
		}
		if path == realPath {
			return nil
		}
		name := d.Name()
		if d.IsDir() && strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(realPath, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if !globMatch(relSlash, pattern) && !globMatch(name, pattern) {
			return nil
		}
		if typeFilter == "file" && d.IsDir() {
			return nil
		}
		if typeFilter == "directory" && !d.IsDir() {
			return nil
		}
		matches = append(matches, e.remap(path))
		return nil
	})
	sort.Strings(matches)
	if len(matches) == 0 {
		return "(no matches)"
	}
	return truncate(strings.Join(matches, "\n"))
}

func (e *Executor) realPath(virtual string) (string, error) {
	if virtual == "" {
		return "", errors.New("empty path")
	}
	normalized := strings.ReplaceAll(virtual, "\\", "/")
	var rel string
	switch {
	case normalized == "/codebase":
		rel = "."
	case strings.HasPrefix(normalized, "/codebase/"):
		rel = strings.TrimPrefix(normalized, "/codebase/")
	case strings.HasPrefix(normalized, "codebase/"):
		rel = strings.TrimPrefix(normalized, "codebase/")
	case filepath.IsAbs(virtual):
		return "", fmt.Errorf("absolute paths are not allowed: %s", virtual)
	default:
		rel = virtual
	}

	candidate := filepath.Clean(filepath.Join(e.root, rel))
	if err := ensureInside(e.root, candidate); err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		if err := ensureInside(e.root, resolved); err != nil {
			return "", err
		}
		return resolved, nil
	}
	return candidate, nil
}

func (e *Executor) remap(text string) string {
	rootSlash := filepath.ToSlash(e.root)
	text = strings.ReplaceAll(text, e.root, "/codebase")
	text = strings.ReplaceAll(text, rootSlash, "/codebase")
	if runtime.GOOS == "windows" {
		text = strings.ReplaceAll(text, strings.ToLower(e.root), "/codebase")
	}
	text = strings.ReplaceAll(text, "\\", "/")
	return text
}

func ensureInside(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes project root: %s", path)
	}
	return nil
}

func buildTree(root, displayRoot string, maxDepth int, exclude func(name, rel string) bool) (string, error) {
	var b strings.Builder
	b.WriteString(displayRoot)
	err := writeTree(&b, root, "", 1, maxDepth, exclude)
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeTree(b *strings.Builder, dir, prefix string, depth, maxDepth int, exclude func(name, rel string) bool) error {
	if depth > maxDepth {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	filtered := entries[:0]
	for _, entry := range entries {
		rel := entry.Name()
		if exclude != nil && exclude(entry.Name(), rel) {
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
			if err := writeTree(b, filepath.Join(dir, entry.Name()), nextPrefix, depth+1, maxDepth, nil); err != nil {
				continue
			}
		}
	}
	return nil
}

func truncate(text string) string {
	maxLines := envInt("FC_RESULT_MAX_LINES", defaultResultMaxLines, 1, 500)
	maxChars := envInt("FC_LINE_MAX_CHARS", defaultLineMaxChars, 20, 10000)
	lines := strings.Split(text, "\n")
	limit := len(lines)
	if limit > maxLines {
		limit = maxLines
	}
	out := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		line := lines[i]
		if len(line) > maxChars {
			line = line[:maxChars]
		}
		out = append(out, line)
	}
	if len(lines) > maxLines {
		out = append(out, "... (lines truncated) ...")
	}
	return strings.Join(out, "\n")
}

func envInt(name string, def, minValue, maxValue int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < minValue {
		return minValue
	}
	if n > maxValue {
		return maxValue
	}
	return n
}

func filteredEnv(env []string, removedKey string) []string {
	prefix := removedKey + "="
	out := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		out = append(out, item)
	}
	out = append(out, removedKey+"=")
	return out
}

func globMatch(str, pattern string) bool {
	regex := "^"
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				regex += ".*"
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
				}
			} else {
				regex += `[^/]*`
			}
		case '?':
			regex += `[^/]`
		default:
			regex += regexp.QuoteMeta(string(c))
		}
	}
	regex += "$"
	ok, err := regexp.MatchString(regex, str)
	return err == nil && ok
}
