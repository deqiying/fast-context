package search

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func ParseToolCall(text string) (*ToolCall, string, error) {
	text = strings.ReplaceAll(text, "</s>", "")
	re := regexp.MustCompile(`(?s)\[TOOL_CALLS\](\w+)\[ARGS\](\{.+)`)
	m := re.FindStringSubmatchIndex(text)
	if len(m) == 0 {
		return nil, text, nil
	}
	name := text[m[2]:m[3]]
	raw := strings.TrimSpace(text[m[4]:m[5]])
	end := findJSONObjectEnd(raw)
	if end <= 0 {
		return nil, text[:m[0]], nil
	}
	raw = raw[:end]
	args := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, text[:m[0]], err
	}
	return &ToolCall{Name: name, Args: args}, strings.TrimSpace(text[:m[0]]), nil
}

func findJSONObjectEnd(raw string) int {
	depth := 0
	inString := false
	escaped := false
	for i, r := range raw {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

func ParseAnswer(xmlText, projectRoot string) Result {
	var files []ResultFile
	root, _ := filepath.Abs(projectRoot)
	fileRe := regexp.MustCompile(`(?s)<file\s+path=["']([^"']+)["']>(.*?)</file>`)
	rangeRe := regexp.MustCompile(`<range>(\d+)-(\d+)</range>`)
	for _, fm := range fileRe.FindAllStringSubmatch(xmlText, -1) {
		vpath := fm[1]
		rel := strings.TrimPrefix(filepath.ToSlash(vpath), "/codebase")
		rel = strings.TrimLeft(rel, `/\`)
		fullPath := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
		if !inside(root, fullPath) {
			continue
		}
		ranges := make([]LineRange, 0)
		for _, rm := range rangeRe.FindAllStringSubmatch(fm[2], -1) {
			start, _ := strconv.Atoi(rm[1])
			end, _ := strconv.Atoi(rm[2])
			if start <= 0 || end <= 0 || end < start {
				continue
			}
			ranges = append(ranges, LineRange{Start: start, End: end})
		}
		files = append(files, ResultFile{Path: filepath.ToSlash(rel), FullPath: fullPath, Ranges: ranges})
	}
	return Result{Files: files}
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}
