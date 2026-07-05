// Package dirscore ports upstream fast-context-mcp 1.5.2 directory-scorer.mjs:
// BM25F over directory profiles + probe grep + git RFM + file-level aggregation,
// fused with Reciprocal Rank Fusion and an adaptive top-K cutoff.
package dirscore

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	rrfK   = 60
)

// Field weights for BM25F (path tokens are the main signal).
var fieldWeights = map[string]float64{
	"dir_name":    1.0,
	"path_tokens": 4.0,
	"metadata":    3.0,
	"headers":     2.0,
}

var defaultExcludes = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true,
	"coverage": true, ".venv": true, "venv": true, "target": true,
	"out": true, ".cache": true, "__pycache__": true, "vendor": true,
	"deps": true, "third_party": true, "logs": true, "data": true,
	".next": true, ".nuxt": true, "bundle": true, "bundled": true,
	"fixtures": true,
}

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(
		"the a an is are was were be been being have has had do does did will would could " +
			"should may might must shall can need dare to of in for on with at by from as " +
			"into through during before after above below and but or nor so yet both either neither " +
			"not only own same than too very just also this that these those here there all any " +
			"some no none each every other another such get set use used using make made if then " +
			"else return new like well where which who what when why how it its we you your") {
		stopwords[w] = true
	}
}

type stemRule struct {
	re          *regexp.Regexp
	replacement string
}

var stemRules = []stemRule{
	{regexp.MustCompile(`^(.+)(ies)$`), "${1}y"},
	{regexp.MustCompile(`^(.+)([^aeiou])(es)$`), "${1}${2}"},
	{regexp.MustCompile(`^(.+)([^aeiou])(s)$`), "${1}${2}"},
	{regexp.MustCompile(`^(.+)(ing)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(edly)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(ly)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(ed)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(ation)$`), "${1}ate"},
	{regexp.MustCompile(`^(.+)(tion)$`), "${1}t"},
	{regexp.MustCompile(`^(.+)(ment)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(ness)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(ful)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(less)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(able)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(ible)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(ally)$`), "${1}al"},
	{regexp.MustCompile(`^(.+)(ity)$`), "${1}"},
	{regexp.MustCompile(`^(.+)(ive)$`), "${1}"},
}

// Stem applies the simplified Porter-like rules (first match wins).
func Stem(word string) string {
	if len(word) < 3 {
		return word
	}
	w := strings.ToLower(word)
	for _, rule := range stemRules {
		if rule.re.MatchString(w) {
			return rule.re.ReplaceAllString(w, rule.replacement)
		}
	}
	return w
}

// splitByCase approximates scule's splitByCase: split on separators and
// case/digit transitions ("fooBarBAZQux" -> foo, Bar, BAZ, Qux).
func splitByCase(s string) []string {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, string(cur))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		if r == '_' || r == '-' || r == '.' || r == ' ' || r == '/' || r == '\\' {
			flush()
			continue
		}
		if len(cur) > 0 {
			prev := cur[len(cur)-1]
			switch {
			// lower/digit → upper boundary (parseJSON, Server2Client)
			case unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
				flush()
			// upper run followed by lower starts a new word (HTTPServer)
			case unicode.IsUpper(prev) && unicode.IsUpper(r) && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return parts
}

// Tokenize splits text into stemmed lowercase tokens, dropping stopwords.
func Tokenize(text string) []string {
	if text == "" {
		return nil
	}
	var tokens []string
	for _, seg := range regexp.MustCompile(`[\s\-./\\]+`).Split(text, -1) {
		if len(seg) < 2 {
			continue
		}
		for _, word := range splitByCase(seg) {
			lower := strings.ToLower(word)
			if len(lower) >= 2 && !stopwords[lower] {
				tokens = append(tokens, Stem(lower))
			}
		}
	}
	return tokens
}

// TokenizePath keeps stopwords (path components are all signal).
func TokenizePath(pathStr string) []string {
	if pathStr == "" {
		return nil
	}
	var tokens []string
	normalized := strings.NewReplacer("/", " ", "\\", " ").Replace(pathStr)
	for _, seg := range strings.Fields(normalized) {
		if len(seg) < 2 {
			continue
		}
		for _, word := range splitByCase(seg) {
			lower := strings.ToLower(word)
			if len(lower) >= 2 {
				tokens = append(tokens, Stem(lower))
			}
		}
	}
	return tokens
}

// ─── Directory profiles ────────────────────────────────────────

type Profile struct {
	DirName   string
	PathText  string
	Metadata  string
	Headers   string
	FileCount int
	FilePaths []string

	tokDirName  []string
	tokPath     []string
	tokMetadata []string
	tokHeaders  []string
}

func extractMetadata(dirPath string) string {
	var metadata []string

	if data, err := os.ReadFile(filepath.Join(dirPath, "package.json")); err == nil {
		var pkg struct {
			Name         string         `json:"name"`
			Description  string         `json:"description"`
			Keywords     []string       `json:"keywords"`
			Dependencies map[string]any `json:"dependencies"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			if pkg.Name != "" {
				metadata = append(metadata, pkg.Name)
			}
			metadata = append(metadata, Tokenize(pkg.Description)...)
			for _, k := range pkg.Keywords {
				metadata = append(metadata, Tokenize(k)...)
			}
			for dep := range pkg.Dependencies {
				metadata = append(metadata, Tokenize(dep)...)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(dirPath, "go.mod")); err == nil {
		if m := regexp.MustCompile(`module\s+(\S+)`).FindStringSubmatch(string(data)); m != nil {
			metadata = append(metadata, TokenizePath(m[1])...)
		}
	}
	for _, tomlName := range []string{"Cargo.toml", "pyproject.toml"} {
		if data, err := os.ReadFile(filepath.Join(dirPath, tomlName)); err == nil {
			if m := regexp.MustCompile(`name\s*=\s*"([^"]+)"`).FindStringSubmatch(string(data)); m != nil {
				metadata = append(metadata, TokenizePath(m[1])...)
			}
		}
	}
	return strings.Join(metadata, " ")
}

var headerExts = map[string]bool{
	".md": true, ".mdx": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".py": true, ".go": true,
}

var (
	mdHeaderRe = regexp.MustCompile(`(?m)^#+\s+.+$`)
	commentRe  = regexp.MustCompile(`^\s*(?:(?://|#|;|\*)\s*)(.+)$`)
)

func extractFileHeaders(filePath string) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	if len(data) > 2000 {
		data = data[:2000]
	}
	content := string(data)
	var headers []string
	for _, h := range mdHeaderRe.FindAllString(content, -1) {
		headers = append(headers, strings.TrimLeft(h, "# "))
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 10 {
		lines = lines[:10]
	}
	for _, line := range lines {
		if m := commentRe.FindStringSubmatch(line); m != nil {
			headers = append(headers, m[1])
		}
	}
	return strings.Join(headers, " ")
}

// BuildProfile walks a top-level directory (up to maxDepth levels) and builds
// its scoring profile. The CLI is one-shot, so no TTL cache is needed.
func BuildProfile(projectRoot, dirName string, excludePaths []string, maxDepth int) *Profile {
	if maxDepth <= 0 {
		maxDepth = 3
	}
	profile := &Profile{DirName: dirName}
	excludeSet := map[string]bool{}
	for _, p := range excludePaths {
		excludeSet[p] = true
	}

	var pathTokens []string
	var headers []string
	var walk func(currentPath string, depth int)
	walk = func(currentPath string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(currentPath)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if defaultExcludes[name] || excludeSet[name] {
				continue
			}
			if strings.HasPrefix(name, ".") && name != ".github" {
				continue
			}
			fullPath := filepath.Join(currentPath, name)
			rel, err := filepath.Rel(projectRoot, fullPath)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				pathTokens = append(pathTokens, rel)
				walk(fullPath, depth+1)
				continue
			}
			pathTokens = append(pathTokens, rel)
			profile.FilePaths = append(profile.FilePaths, rel)
			profile.FileCount++
			if headerExts[strings.ToLower(filepath.Ext(name))] {
				if h := extractFileHeaders(fullPath); h != "" {
					headers = append(headers, h)
				}
			}
		}
	}
	walk(filepath.Join(projectRoot, dirName), 1)

	profile.Metadata = extractMetadata(filepath.Join(projectRoot, dirName))
	profile.PathText = strings.Join(pathTokens, " ")
	profile.Headers = strings.Join(headers, " ")

	profile.tokDirName = Tokenize(profile.DirName)
	profile.tokPath = Tokenize(profile.PathText)
	profile.tokMetadata = Tokenize(profile.Metadata)
	profile.tokHeaders = Tokenize(profile.Headers)
	return profile
}

// ─── BM25F ─────────────────────────────────────────────────────

func computeIDF(documents [][]string) map[string]float64 {
	docCount := float64(len(documents))
	termDocCount := map[string]float64{}
	for _, doc := range documents {
		seen := map[string]bool{}
		for _, term := range doc {
			if !seen[term] {
				seen[term] = true
				termDocCount[term]++
			}
		}
	}
	idf := make(map[string]float64, len(termDocCount))
	for term, count := range termDocCount {
		idf[term] = math.Log((docCount-count+0.5)/(count+0.5) + 1)
	}
	return idf
}

func bm25FieldScore(queryTerms, fieldTerms []string, avgLen, fieldLen float64, idf map[string]float64) float64 {
	termFreqs := map[string]float64{}
	for _, t := range fieldTerms {
		termFreqs[t]++
	}
	var score float64
	for _, term := range queryTerms {
		tf := termFreqs[term]
		if tf == 0 {
			continue
		}
		termIDF, ok := idf[term]
		if !ok {
			termIDF = math.Log(2)
		}
		numerator := tf * (bm25K1 + 1)
		denominator := tf + bm25K1*(1-bm25B+bm25B*(fieldLen/avgLen))
		score += termIDF * (numerator / denominator)
	}
	return score
}

func bm25fScore(queryTerms []string, profile *Profile, avgFieldLens map[string]float64, idf map[string]float64) float64 {
	fields := []struct {
		name  string
		terms []string
	}{
		{"dir_name", profile.tokDirName},
		{"path_tokens", profile.tokPath},
		{"metadata", profile.tokMetadata},
		{"headers", profile.tokHeaders},
	}
	var total float64
	for _, field := range fields {
		avgLen := avgFieldLens[field.name]
		if avgLen == 0 {
			avgLen = 50
		}
		fieldLen := float64(len(field.terms))
		if fieldLen == 0 {
			fieldLen = 1
		}
		total += fieldWeights[field.name] * bm25FieldScore(queryTerms, field.terms, avgLen, fieldLen, idf)
	}
	return total
}

// ─── Probe grep signal ─────────────────────────────────────────

func selectProbeTerms(queryTerms []string, idf map[string]float64, maxTerms int) []string {
	type termIDF struct {
		term string
		idf  float64
	}
	sorted := make([]termIDF, 0, len(queryTerms))
	for _, t := range queryTerms {
		sorted = append(sorted, termIDF{t, idf[t]})
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].idf > sorted[j].idf })
	seen := map[string]bool{}
	var out []string
	for _, t := range sorted {
		if seen[t.term] {
			continue
		}
		seen[t.term] = true
		out = append(out, t.term)
		if len(out) >= maxTerms {
			break
		}
	}
	return out
}

func probeGrep(rgPath, projectRoot string, topDirs, probeTerms, excludePaths []string) map[string]int {
	dirHits := map[string]int{}
	for _, d := range topDirs {
		dirHits[d] = 0
	}
	if len(probeTerms) == 0 || rgPath == "" {
		return dirHits
	}
	excludeSet := map[string]bool{}
	var excludeList []string
	appendExclude := func(p string) {
		if p != "" && !excludeSet[p] {
			excludeSet[p] = true
			excludeList = append(excludeList, p)
		}
	}
	for _, p := range excludePaths {
		appendExclude(p)
	}
	for p := range defaultExcludes {
		appendExclude(p)
	}
	sort.Strings(excludeList)

	args := []string{"-l", "--hidden", "-S", "-F"}
	for _, t := range probeTerms {
		args = append(args, "-e", t)
	}
	args = append(args, "-g", "!{"+strings.Join(excludeList, ",")+"}", projectRoot)

	cmd := exec.Command(rgPath, args...)
	cmd.Env = append(os.Environ(), "RIPGREP_CONFIG_PATH=")
	out, err := runWithTimeout(cmd, 8*time.Second)
	if err != nil && len(out) == 0 {
		return dirHits
	}
	for _, file := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if file == "" {
			continue
		}
		rel, err := filepath.Rel(projectRoot, file)
		if err != nil {
			continue
		}
		topDir := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if _, ok := dirHits[topDir]; ok {
			dirHits[topDir]++
		}
	}
	return dirHits
}

func computeProbeScore(hits, fileCount int) float64 {
	if hits == 0 {
		return 0
	}
	return math.Log(1+float64(hits)) / math.Sqrt(1+float64(fileCount))
}

// ─── RRF fusion ────────────────────────────────────────────────

type dirScore struct {
	dir   string
	score float64
}

func rrfFusion(rankings [][]dirScore) []dirScore {
	finalScores := map[string]float64{}
	for _, ranking := range rankings {
		for pos, entry := range ranking {
			finalScores[entry.dir] += 1.0 / float64(rrfK+pos+1)
		}
	}
	out := make([]dirScore, 0, len(finalScores))
	for dir, score := range finalScores {
		out = append(out, dirScore{dir, score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].dir < out[j].dir
	})
	return out
}

// ─── Adaptive top-K ────────────────────────────────────────────

const (
	kMin           = 3
	kMax           = 10
	entropyGamma   = 0.5
	softmaxTemp    = 1.0
	tailScanWindow = 6
)

func adaptiveTopK(fused []dirScore, userTopK, n int) []string {
	if len(fused) <= kMin {
		out := make([]string, len(fused))
		for i, r := range fused {
			out[i] = r.dir
		}
		return out
	}
	scores := make([]float64, len(fused))
	for i, r := range fused {
		scores[i] = r.score
	}

	// Signal 1: N-proportional safety floor.
	kBase := userTopK
	if ceil := int(math.Ceil(float64(n) * 0.15)); ceil < kMax {
		if ceil > kBase {
			kBase = ceil
		}
	} else if kMax > kBase {
		kBase = kMax
	}

	// Signal 2: Kneedle max-gap detection.
	maxGap := 0.0
	kKnee := kBase
	searchEnd := kMax
	if len(scores)-1 < searchEnd {
		searchEnd = len(scores) - 1
	}
	for i := kMin - 1; i < searchEnd; i++ {
		gap := scores[i] - scores[i+1]
		if gap > maxGap {
			maxGap = gap
			kKnee = i + 1
		}
	}

	// Signal 3: softmax-normalized entropy scaling.
	maxScore := scores[0]
	expScores := make([]float64, len(scores))
	var expSum float64
	for i, s := range scores {
		expScores[i] = math.Exp((s - maxScore) / softmaxTemp)
		expSum += expScores[i]
	}
	var entropy float64
	for _, e := range expScores {
		p := e / expSum
		if p > 0 {
			entropy -= p * math.Log(p)
		}
	}
	hNorm := 0.0
	if len(scores) > 1 {
		hNorm = entropy / math.Log(float64(len(scores)))
	}
	kEntropy := int(math.Ceil(float64(kBase) * (1 + entropyGamma*hNorm)))

	primaryK := kBase
	if kKnee > primaryK {
		primaryK = kKnee
	}
	if kEntropy > primaryK {
		primaryK = kEntropy
	}
	if primaryK < kMin {
		primaryK = kMin
	}
	if primaryK > kMax {
		primaryK = kMax
	}
	if primaryK > len(fused) {
		primaryK = len(fused)
	}

	hotDirs := make([]string, 0, primaryK+tailScanWindow)
	for _, r := range fused[:primaryK] {
		hotDirs = append(hotDirs, r.dir)
	}

	// Adaptive tail inclusion based on head decay rate.
	if len(fused) > primaryK {
		cutoffScore := scores[primaryK-1]
		headDecayRate := 0.0
		if primaryK > 1 {
			headDecayRate = (scores[0] - cutoffScore) / float64(primaryK-1)
		}
		tailThreshold := math.Max(cutoffScore-headDecayRate, cutoffScore*0.4)
		for i := primaryK; i < len(fused) && i < primaryK+tailScanWindow; i++ {
			if scores[i] >= tailThreshold {
				hotDirs = append(hotDirs, fused[i].dir)
			} else {
				break
			}
		}
	}
	return hotDirs
}

// ─── Path spines ───────────────────────────────────────────────

var sourcePathPatterns = []string{"/src/", "/core/", "/lib/", "/internal/", "/pkg/", "/cmd/"}
var noisePathPatterns = []string{
	"/migrations/", "/test/", "/__tests__/", "/fixtures/", "/examples/",
	"/vendor/", "/mock/", "/mocks/", "/i18n/", "/locales/", "/versions/",
}

func extractPathSpines(profiles map[string]*Profile, queryTerms, keywords []string, topN int) []string {
	allTerms := uniqueStrings(append(append([]string(nil), queryTerms...), keywords...))
	if len(allTerms) == 0 {
		return nil
	}
	type candidate struct {
		path  string
		score float64
	}
	var candidates []candidate
	for _, profile := range profiles {
		for _, filePath := range profile.FilePaths {
			pathTokens := TokenizePath(filePath)
			pathText := strings.ToLower(filePath)
			parts := strings.Split(filePath, "/")
			fileName := strings.ToLower(parts[len(parts)-1])
			if dot := strings.LastIndex(fileName, "."); dot > 0 {
				fileName = fileName[:dot]
			}
			fileNameTokens := TokenizePath(fileName)

			var score float64
			for _, term := range allTerms {
				switch {
				case strings.Contains(fileName, term) || containsExact(fileNameTokens, term):
					score += 4
				case strings.Contains(pathText, term):
					score += 2
				case containsPartial(pathTokens, term):
					score += 1
				}
			}
			if score <= 0 {
				continue
			}
			lowerPath := "/" + pathText
			if containsAnySubstring(lowerPath, sourcePathPatterns) {
				score *= 1.5
			}
			if containsAnySubstring(lowerPath, noisePathPatterns) {
				score *= 0.3
			}
			candidates = append(candidates, candidate{filePath, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})
	if len(candidates) > topN {
		candidates = candidates[:topN]
	}
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.path
	}
	return out
}

// ─── Git RFM signal ────────────────────────────────────────────

type gitRFMEntry struct {
	dir          string
	score        float64
	recency      float64
	commits      int
	linesChanged int
}

func computeGitRFM(projectRoot string, topDirs []string) []gitRFMEntry {
	const (
		windowDays   = 180
		halfLifeDays = 30
		wr           = 0.4
		wf           = 0.35
		wm           = 0.25
	)
	lambda := math.Ln2 / halfLifeDays
	nowSec := time.Now().Unix()
	sinceDate := time.Now().AddDate(0, 0, -windowDays).Format("2006-01-02")

	type stats struct {
		lastCommitSec int64
		commits       int
		linesChanged  int
	}
	dirStats := map[string]*stats{}
	for _, d := range topDirs {
		dirStats[d] = &stats{}
	}

	cmd := exec.Command("git", "log", "--format=%at", "--numstat", "--since="+sinceDate, "--no-merges")
	cmd.Dir = projectRoot
	out, err := runWithTimeout(cmd, 10*time.Second)
	if err != nil && len(out) == 0 {
		return nil
	}

	var currentTimestamp int64
	seenDirsForCommit := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			seenDirsForCommit = map[string]bool{}
			continue
		}
		if ts, err := strconv.ParseInt(trimmed, 10, 64); err == nil && regexp.MustCompile(`^\d+$`).MatchString(trimmed) {
			currentTimestamp = ts
			seenDirsForCommit = map[string]bool{}
			continue
		}
		parts := strings.Split(trimmed, "\t")
		if len(parts) < 3 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		deleted, _ := strconv.Atoi(parts[1])
		topDir := strings.SplitN(filepath.ToSlash(parts[2]), "/", 2)[0]
		st, ok := dirStats[topDir]
		if !ok {
			continue
		}
		st.linesChanged += added + deleted
		if currentTimestamp > st.lastCommitSec {
			st.lastCommitSec = currentTimestamp
		}
		if !seenDirsForCommit[topDir] {
			seenDirsForCommit[topDir] = true
			st.commits++
		}
	}

	totalCommits := 0
	for _, st := range dirStats {
		totalCommits += st.commits
	}
	if totalCommits == 0 {
		totalCommits = 1
	}

	ranking := make([]gitRFMEntry, 0, len(topDirs))
	for _, dir := range topDirs {
		st := dirStats[dir]
		recency := 0.0
		if st.lastCommitSec > 0 {
			daysSince := float64(nowSec-st.lastCommitSec) / 86400
			recency = math.Exp(-lambda * daysSince)
		}
		frequency := float64(st.commits) / float64(totalCommits)
		modification := math.Log2(1 + float64(st.linesChanged))
		mNorm := modification / (modification + 10)
		ranking = append(ranking, gitRFMEntry{
			dir:          dir,
			score:        wr*recency + wf*frequency + wm*mNorm,
			recency:      recency,
			commits:      st.commits,
			linesChanged: st.linesChanged,
		})
	}
	sort.SliceStable(ranking, func(i, j int) bool { return ranking[i].score > ranking[j].score })
	return ranking
}

// ─── File-level Log-Sum aggregation ────────────────────────────

func fileAggregateScore(queryTerms []string, profile *Profile) float64 {
	const (
		alpha     = 0.5
		threshold = 0.3
		maxFiles  = 200
	)
	if len(profile.FilePaths) == 0 {
		return 0
	}
	files := profile.FilePaths
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	var fileScores []float64
	for _, relPath := range files {
		pathTokens := TokenizePath(relPath)
		var score float64
		for _, qt := range queryTerms {
			switch {
			case containsExact(pathTokens, qt):
				score += 2.0
			case containsPartial(pathTokens, qt):
				score += 1.0
			case strings.Contains(strings.ToLower(relPath), qt):
				score += 0.5
			}
		}
		if score > 0 {
			fileScores = append(fileScores, score)
		}
	}
	if len(fileScores) == 0 {
		return 0
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(fileScores)))
	maxScore := fileScores[0]
	var densitySum float64
	for _, s := range fileScores {
		if s > threshold {
			densitySum += s - threshold
		}
	}
	return maxScore + alpha*math.Log(1+densitySum)
}

// ─── Main API ──────────────────────────────────────────────────

type Options struct {
	TopK      int
	UseProbe  bool
	Keywords  []string // rg patterns from bootstrap phase
	MinReturn int
	RGPath    string // ripgrep binary for probe grep; empty disables probe
}

type Result struct {
	HotDirs    []string
	PathSpines []string
	Signals    map[string][]string
}

// ScoreDirectories ranks top-level directories for a query using all signals.
func ScoreDirectories(query, projectRoot string, topDirs, excludePaths []string, opts Options) Result {
	if opts.TopK <= 0 {
		opts.TopK = 4
	}
	if opts.MinReturn <= 0 {
		opts.MinReturn = 2
	}
	queryTerms := Tokenize(query)

	profiles := make(map[string]*Profile, len(topDirs))
	for _, dir := range topDirs {
		profiles[dir] = BuildProfile(projectRoot, dir, excludePaths, 3)
	}

	allFieldTerms := make([][]string, 0, len(topDirs))
	for _, dir := range topDirs {
		p := profiles[dir]
		doc := make([]string, 0, len(p.tokDirName)+len(p.tokPath)+len(p.tokMetadata)+len(p.tokHeaders))
		doc = append(doc, p.tokDirName...)
		doc = append(doc, p.tokPath...)
		doc = append(doc, p.tokMetadata...)
		doc = append(doc, p.tokHeaders...)
		allFieldTerms = append(allFieldTerms, doc)
	}
	idf := computeIDF(allFieldTerms)

	avgFieldLens := map[string]float64{}
	if len(topDirs) > 0 {
		for _, dir := range topDirs {
			p := profiles[dir]
			avgFieldLens["dir_name"] += float64(len(p.tokDirName))
			avgFieldLens["path_tokens"] += float64(len(p.tokPath))
			avgFieldLens["metadata"] += float64(len(p.tokMetadata))
			avgFieldLens["headers"] += float64(len(p.tokHeaders))
		}
		for field := range avgFieldLens {
			avgFieldLens[field] /= float64(len(topDirs))
			if avgFieldLens[field] == 0 {
				avgFieldLens[field] = 10
			}
		}
	}

	signals := map[string][]string{}

	// Signal 1: BM25F.
	bm25fRanking := make([]dirScore, 0, len(topDirs))
	for _, dir := range topDirs {
		bm25fRanking = append(bm25fRanking, dirScore{dir, bm25fScore(queryTerms, profiles[dir], avgFieldLens, idf)})
	}
	sort.SliceStable(bm25fRanking, func(i, j int) bool { return bm25fRanking[i].score > bm25fRanking[j].score })
	rankings := [][]dirScore{bm25fRanking}
	signals["bm25f"] = dirsOf(bm25fRanking)

	// Signal 2: probe grep.
	if opts.UseProbe && len(queryTerms) > 0 && opts.RGPath != "" {
		var keywordTerms []string
		for _, k := range opts.Keywords {
			keywordTerms = append(keywordTerms, Tokenize(k)...)
		}
		probeCandidates := uniqueStrings(append(append([]string(nil), queryTerms...), keywordTerms...))
		probeTerms := selectProbeTerms(probeCandidates, idf, 6)
		if len(probeTerms) > 0 {
			dirHits := probeGrep(opts.RGPath, projectRoot, topDirs, probeTerms, excludePaths)
			probeRanking := make([]dirScore, 0, len(topDirs))
			probeSignal := make([]string, 0, len(topDirs))
			for _, dir := range topDirs {
				fileCount := profiles[dir].FileCount
				if fileCount == 0 {
					fileCount = 1
				}
				probeRanking = append(probeRanking, dirScore{dir, computeProbeScore(dirHits[dir], fileCount)})
			}
			sort.SliceStable(probeRanking, func(i, j int) bool { return probeRanking[i].score > probeRanking[j].score })
			for _, r := range probeRanking {
				probeSignal = append(probeSignal, r.dir+":"+strconv.Itoa(dirHits[r.dir]))
			}
			rankings = append(rankings, probeRanking)
			signals["probe"] = probeSignal
		}
	}

	// Signal 3: bootstrap keywords vs path text.
	if len(opts.Keywords) > 0 {
		var keywordTerms []string
		for _, k := range opts.Keywords {
			keywordTerms = append(keywordTerms, Tokenize(k)...)
		}
		keywordRanking := make([]dirScore, 0, len(topDirs))
		for _, dir := range topDirs {
			lowerPathText := strings.ToLower(profiles[dir].PathText)
			var score float64
			for _, term := range keywordTerms {
				if strings.Contains(lowerPathText, term) {
					score++
				}
			}
			keywordRanking = append(keywordRanking, dirScore{dir, score})
		}
		sort.SliceStable(keywordRanking, func(i, j int) bool { return keywordRanking[i].score > keywordRanking[j].score })
		rankings = append(rankings, keywordRanking)
		signals["keywords"] = dirsOf(keywordRanking)
	}

	// Signal 4: git history RFM.
	if gitRanking := computeGitRFM(projectRoot, topDirs); len(gitRanking) > 0 {
		hasSignal := false
		for _, r := range gitRanking {
			if r.score > 0 {
				hasSignal = true
				break
			}
		}
		if hasSignal {
			asDirScores := make([]dirScore, len(gitRanking))
			gitSignal := make([]string, 0, 6)
			for i, r := range gitRanking {
				asDirScores[i] = dirScore{r.dir, r.score}
				if i < 6 {
					gitSignal = append(gitSignal, r.dir+":R="+strconv.FormatFloat(r.recency, 'f', 2, 64)+",C="+strconv.Itoa(r.commits))
				}
			}
			rankings = append(rankings, asDirScores)
			signals["gitRFM"] = gitSignal
		}
	}

	// Signal 5: file-level Log-Sum aggregation.
	fileAggRanking := make([]dirScore, 0, len(topDirs))
	for _, dir := range topDirs {
		fileAggRanking = append(fileAggRanking, dirScore{dir, fileAggregateScore(queryTerms, profiles[dir])})
	}
	sort.SliceStable(fileAggRanking, func(i, j int) bool { return fileAggRanking[i].score > fileAggRanking[j].score })
	hasFileAgg := false
	for _, r := range fileAggRanking {
		if r.score > 0 {
			hasFileAgg = true
			break
		}
	}
	if hasFileAgg {
		rankings = append(rankings, fileAggRanking)
		fileAggSignal := make([]string, 0, 6)
		for i, r := range fileAggRanking {
			if i >= 6 {
				break
			}
			fileAggSignal = append(fileAggSignal, r.dir+":"+strconv.FormatFloat(r.score, 'f', 2, 64))
		}
		signals["fileAgg"] = fileAggSignal
	}

	fused := rrfFusion(rankings)
	for len(fused) < opts.MinReturn && len(fused) < len(topDirs) {
		missing := ""
		for _, d := range topDirs {
			found := false
			for _, f := range fused {
				if f.dir == d {
					found = true
					break
				}
			}
			if !found {
				missing = d
				break
			}
		}
		if missing == "" {
			break
		}
		fused = append(fused, dirScore{missing, 0.001})
	}

	pathSpines := extractPathSpines(profiles, queryTerms, opts.Keywords, 30)
	hotDirs := adaptiveTopK(fused, opts.TopK, len(topDirs))

	return Result{HotDirs: hotDirs, PathSpines: pathSpines, Signals: signals}
}

// QuickScore is the lightweight fallback when full scoring fails or when
// profiles are unnecessary: token overlap between query and directory names.
func QuickScore(query string, topDirs []string, topK int) []string {
	queryTerms := Tokenize(query)
	scored := make([]dirScore, 0, len(topDirs))
	for _, dir := range topDirs {
		dirTerms := Tokenize(dir)
		var score float64
		for _, qt := range queryTerms {
			if containsPartial(dirTerms, qt) {
				score++
			}
		}
		scored = append(scored, dirScore{dir, score})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return dirsOf(scored)
}

// ─── helpers ───────────────────────────────────────────────────

func runWithTimeout(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
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

func dirsOf(ranking []dirScore) []string {
	out := make([]string, len(ranking))
	for i, r := range ranking {
		out[i] = r.dir
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func containsExact(tokens []string, term string) bool {
	for _, t := range tokens {
		if t == term {
			return true
		}
	}
	return false
}

func containsPartial(tokens []string, term string) bool {
	for _, t := range tokens {
		if strings.Contains(t, term) || strings.Contains(term, t) {
			return true
		}
	}
	return false
}

func containsAnySubstring(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
