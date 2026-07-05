package search

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/deqiying/fast-context/internal/config"
)

var retryQueryStopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(
		"where what which when who why how does with from into that this those these there their " +
			"implemented implementation handled handling used using code file files logic find search") {
		retryQueryStopwords[w] = true
	}
}

var retryFallbackDirHints = []string{
	"src", "app", "lib", "packages", "server", "backend",
	"services", "internal", "frontend", "web", "client",
}

func retryQueryTokens(query string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range regexp.MustCompile(`[^a-z0-9_\-]+`).Split(strings.ToLower(query), -1) {
		t = strings.TrimSpace(t)
		if len(t) < 3 || retryQueryStopwords[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func safeExistingDir(root, dirName string) string {
	if dirName == "" || strings.Contains(dirName, "..") ||
		strings.ContainsAny(dirName, `/\`) || filepath.IsAbs(dirName) {
		return ""
	}
	abs := filepath.Join(root, dirName)
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return ""
	}
	return abs
}

func probeRetryDirScore(rgPath, absDir string, tokens []string) int {
	if len(tokens) == 0 || rgPath == "" {
		return 0
	}
	escaped := make([]string, len(tokens))
	for i, t := range tokens {
		escaped[i] = regexp.QuoteMeta(t)
	}
	args := []string{"-l", "--max-count", "20", "-S"}
	for _, ex := range config.MergeExcludePaths(nil) {
		for _, expanded := range expandExcludeGlobsForRg(ex) {
			args = append(args, "--glob", "!"+expanded)
		}
	}
	args = append(args, "--", strings.Join(escaped, "|"), absDir)
	out, err := runRGWithTimeout(rgPath, args, 1500*time.Millisecond)
	if err != nil && len(out) == 0 {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			count++
		}
	}
	return count
}

type retryCandidate struct {
	dirName    string
	abs        string
	score      int
	probeScore int
	hotScore   int
}

func scoreRetryCandidate(rgPath, root, dirName string, tokens []string, hotDirSet map[string]bool) *retryCandidate {
	abs := safeExistingDir(root, dirName)
	if abs == "" {
		return nil
	}
	lowerName := strings.ToLower(dirName)
	nameScore := 0
	for _, token := range tokens {
		switch {
		case lowerName == token:
			nameScore += 4
		case strings.Contains(lowerName, token) || strings.Contains(token, lowerName):
			nameScore += 2
		}
	}
	hintScore := 0
	for _, hint := range retryFallbackDirHints {
		if hint == lowerName {
			hintScore = 1
			break
		}
	}
	hotScore := 0
	if hotDirSet[dirName] {
		hotScore = 2
	}
	probeScore := probeRetryDirScore(rgPath, abs, tokens)
	return &retryCandidate{
		dirName:    dirName,
		abs:        abs,
		score:      probeScore*10 + nameScore + hotScore + hintScore,
		probeScore: probeScore,
		hotScore:   hotScore,
	}
}

// selectNoResultRetryProjectRoots picks up to maxRetries narrower project
// roots to retry when the model returned no parseable files: hot dirs from
// the first pass, real top-level dirs, and common source-root hints, ranked
// by local probe hits + name/hint/hot-dir affinity.
func selectNoResultRetryProjectRoots(rgPath, projectRoot string, hotDirs []string, maxRetries int, query string, listTopLevelDirs func(string, []string) []string) []string {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil
	}
	hotDirSet := map[string]bool{}
	for _, d := range hotDirs {
		hotDirSet[d] = true
	}
	tokens := retryQueryTokens(query)

	var candidateNames []string
	candidateNames = append(candidateNames, hotDirs...)
	candidateNames = append(candidateNames, listTopLevelDirs(root, config.MergeExcludePaths(nil))...)
	candidateNames = append(candidateNames, retryFallbackDirHints...)
	candidateNames = lastUnique(candidateNames, len(candidateNames))

	var candidates []*retryCandidate
	for _, dirName := range candidateNames {
		if c := scoreRetryCandidate(rgPath, root, dirName, tokens, hotDirSet); c != nil {
			candidates = append(candidates, c)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.probeScore != b.probeScore {
			return a.probeScore > b.probeScore
		}
		if a.hotScore != b.hotScore {
			return a.hotScore > b.hotScore
		}
		return a.dirName < b.dirName
	})
	if len(candidates) > maxRetries {
		candidates = candidates[:maxRetries]
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.abs)
	}
	return out
}
