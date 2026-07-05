package search

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Noise globs excluded from grep keyword expansion (upstream 1.5.2: bundler
// output, lock files, generated artifacts, binaries, media, AI config files).
var grepNoiseGlobs = []string{
	// bundler output & sourcemaps
	"chunk-*", "*.chunk.*", "*.bundle.*",
	"*.min.js", "*.min.css", "*.map",
	"app.*.js", "app.*.css",
	// dependency & lock files
	"*.lock", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"go.sum", "go.mod",
	"Cargo.lock",
	// generated type declarations
	"*.d.ts",
	// binaries
	"*.exe", "*.dll", "*.so", "*.dylib",
	"*.pyc", "*.pyo", "*.class",
	"*.wasm", "*.o", "*.a",
	// static assets & media
	"*.svg", "*.png", "*.jpg", "*.jpeg", "*.gif", "*.ico", "*.webp",
	"*.woff*", "*.ttf", "*.eot", "*.otf",
	"*.mp4", "*.mp3", "*.wav", "*.avi", "*.mov",
	"*.pdf", "*.doc", "*.docx", "*.xls", "*.xlsx",
	"*.zip", "*.tar", "*.gz", "*.rar", "*.7z",
	// AI config files
	"CLAUDE.md", "AGENTS.md", ".cursorrules", ".cursorignore",
}

var grepNoiseDirGlobs = []string{
	// build output
	"dist/**", "build/**", "out/**", "target/**",
	"resource/page/**",
	// framework build caches
	".nuxt/**", ".next/**", ".output/**",
	// caches
	"__pycache__/**", ".cache/**", ".pytest_cache/**",
	// version control
	".svn/**", ".hg/**",
	// dependency dirs
	"vendor/**", ".venv/**", "venv/**",
}

// autoGrepFiles runs collected rg patterns locally to supplement files the
// remote model missed. Zero API calls. Results are sorted by how many
// patterns hit each file.
func autoGrepFiles(rgPath string, patterns []string, projectRoot string, excludePaths []string, existingPaths []string, maxPerPattern, maxTotal int) []ResultFile {
	if rgPath == "" || maxTotal <= 0 {
		return nil
	}
	hitCount := map[string]int{}
	fileInfo := map[string]ResultFile{}
	order := []string{}
	seen := map[string]bool{}
	for _, p := range existingPaths {
		abs, err := filepath.Abs(filepath.Join(projectRoot, p))
		if err == nil {
			seen[abs] = true
		}
	}

	noiseArgs := make([]string, 0, 2*(len(grepNoiseGlobs)+len(grepNoiseDirGlobs)))
	for _, noise := range grepNoiseGlobs {
		noiseArgs = append(noiseArgs, "--glob", "!"+noise)
	}
	for _, noise := range grepNoiseDirGlobs {
		noiseArgs = append(noiseArgs, "--glob", "!"+noise)
	}

	limited := patterns
	if len(limited) > 8 {
		limited = limited[:8]
	}
	for _, pattern := range limited {
		if len(fileInfo) >= maxTotal {
			break
		}
		args := []string{"-l", "--max-count", "10", "-S"}
		for _, ex := range excludePaths {
			for _, expanded := range expandExcludeGlobsForRg(ex) {
				args = append(args, "--glob", "!"+expanded)
			}
		}
		args = append(args, noiseArgs...)
		args = append(args, "--", pattern, projectRoot)

		out, err := runRGWithTimeout(rgPath, args, 5*time.Second)
		if err != nil && len(out) == 0 {
			continue
		}
		added := 0
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f == "" || added >= maxPerPattern {
				continue
			}
			full, err := filepath.Abs(f)
			if err != nil {
				continue
			}
			if seen[full] {
				continue
			}
			rel, err := filepath.Rel(projectRoot, full)
			if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				continue
			}
			hitCount[full]++
			if _, ok := fileInfo[full]; !ok {
				fileInfo[full] = ResultFile{
					Path:     filepath.ToSlash(rel),
					FullPath: full,
					Ranges:   []LineRange{},
					FromGrep: true,
				}
				order = append(order, full)
				added++
				if len(fileInfo) >= maxTotal {
					break
				}
			}
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		return hitCount[order[i]] > hitCount[order[j]]
	})
	if len(order) > maxTotal {
		order = order[:maxTotal]
	}
	found := make([]ResultFile, 0, len(order))
	for _, full := range order {
		found = append(found, fileInfo[full])
	}
	return found
}

func expandExcludeGlobsForRg(pattern string) []string {
	normalized := strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if normalized == "" {
		return nil
	}
	expanded := []string{normalized}
	if !strings.HasPrefix(normalized, "**/") && !strings.HasPrefix(normalized, "/") {
		expanded = append(expanded, "**/"+normalized)
	}
	return expanded
}

func runRGWithTimeout(rgPath string, args []string, timeout time.Duration) ([]byte, error) {
	cmd := exec.Command(rgPath, args...)
	cmd.Env = append(os.Environ(), "RIPGREP_CONFIG_PATH=")
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return out, err
	}
}
