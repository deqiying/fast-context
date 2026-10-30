package search

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// MalformedToolCallError 表示模型已输出文本 tool-call 标记，但其 JSON
// 参数不合法。传输层仍然有效，因此调用方可在不执行本次任何命令的前提下，
// 请求一次有界的纠正重试。
type MalformedToolCallError struct {
	err error
}

func (e *MalformedToolCallError) Error() string { return e.err.Error() }
func (e *MalformedToolCallError) Unwrap() error { return e.err }

func ParseToolCall(text string) (*ToolCall, string, error) {
	text = strings.ReplaceAll(text, "</s>", "")
	re := regexp.MustCompile(`(?s)\[TOOL_CALLS\](\w+)\[ARGS\](\{.+)`)
	m := re.FindStringSubmatchIndex(text)
	if len(m) == 0 {
		return nil, text, nil
	}
	name := text[m[2]:m[3]]
	raw := strings.TrimSpace(text[m[4]:m[5]])
	thinking := strings.TrimSpace(text[:m[0]])

	end := findJSONObjectEnd(raw)
	var parseErr error
	if end > 0 {
		args := map[string]any{}
		if parseErr = json.Unmarshal([]byte(raw[:end]), &args); parseErr == nil {
			return &ToolCall{Name: name, Args: args}, thinking, nil
		}
	}

	// 与 npm 1.5.2 / sammysnake 1.3.2 对齐：先在本地做一次有界的语法修复，
	// 避免为常见的模型 JSON 瑕疵额外消耗一轮远端请求。修复结果仍会经过
	// decodeCommands 的严格字段与预算校验，不会放宽可执行命令的边界。
	if fixed, changed := repairToolArgsJSON(raw); changed {
		if fixedEnd := findJSONObjectEnd(fixed); fixedEnd > 0 {
			args := map[string]any{}
			if json.Unmarshal([]byte(fixed[:fixedEnd]), &args) == nil {
				return &ToolCall{Name: name, Args: args, Repaired: true}, thinking, nil
			}
		}
	}

	if end <= 0 {
		// JSON 对象未闭合：保持历史行为，按无 tool call 处理。
		return nil, text[:m[0]], nil
	}
	return nil, text[:m[0]], &MalformedToolCallError{err: parseErr}
}

// repairToolArgsJSON 只在 JSON 字符串外部做以下修复，绝不改写字符串内容：
//   - 未加引号的对象 key：{type:"rg"} → {"type":"rg"}
//   - 缺少左引号的对象 key：{exclude":[]} → {"exclude":[]}
//   - 对象或数组中的尾随逗号：[1,] / {"a":1,} → 删除逗号
//
// 结构字符均为 ASCII，按字节扫描不会切断 UTF-8 多字节序列。
func repairToolArgsJSON(raw string) (string, bool) {
	var b strings.Builder
	b.Grow(len(raw) + 16)
	inString := false
	escaped := false
	expectKey := false
	changed := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			expectKey = false
			b.WriteByte(c)
		case c == '{':
			expectKey = true
			b.WriteByte(c)
		case c == ',':
			if next := skipJSONSpace(raw, i+1); next < len(raw) && (raw[next] == '}' || raw[next] == ']') {
				changed = true
				continue
			}
			expectKey = true
			b.WriteByte(c)
		case isJSONSpace(c):
			b.WriteByte(c)
		case expectKey && isKeyStart(c):
			keyEnd := i
			for keyEnd < len(raw) && isKeyPart(raw[keyEnd]) {
				keyEnd++
			}
			afterKey := keyEnd
			if afterKey < len(raw) && raw[afterKey] == '"' {
				afterKey++
			}
			colon := skipJSONSpace(raw, afterKey)
			expectKey = false
			if colon < len(raw) && raw[colon] == ':' {
				b.WriteByte('"')
				b.WriteString(raw[i:keyEnd])
				b.WriteByte('"')
				b.WriteString(raw[afterKey:colon])
				changed = true
				i = colon - 1
				continue
			}
			b.WriteByte(c)
		default:
			expectKey = false
			b.WriteByte(c)
		}
	}
	return b.String(), changed
}

func skipJSONSpace(raw string, i int) int {
	for i < len(raw) && isJSONSpace(raw[i]) {
		i++
	}
	return i
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isKeyStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isKeyPart(c byte) bool {
	return isKeyStart(c) || c == '-' || (c >= '0' && c <= '9')
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
