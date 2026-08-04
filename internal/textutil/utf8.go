package textutil

import (
	"strings"
	"unicode/utf8"
)

// NormalizeUTF8 将非法 UTF-8 序列替换为 Unicode replacement character。
func NormalizeUTF8(text string) (normalized string, repaired bool) {
	if utf8.ValidString(text) {
		return text, false
	}
	return strings.ToValidUTF8(text, string(utf8.RuneError)), true
}

// TruncateUTF8Bytes 将文本限制在 byte budget 内，并保证文本与 marker 都不拆分 rune。
func TruncateUTF8Bytes(text string, maxBytes int, marker string) (out string, truncated bool) {
	normalized, _ := NormalizeUTF8(text)
	if len(normalized) == 0 {
		return "", false
	}
	if maxBytes <= 0 {
		return "", true
	}
	if len(normalized) <= maxBytes {
		return normalized, false
	}

	normalizedMarker, _ := NormalizeUTF8(marker)
	if len(normalizedMarker) > maxBytes {
		return utf8Prefix(normalizedMarker, maxBytes), true
	}

	contentBudget := maxBytes - len(normalizedMarker)
	return utf8Prefix(normalized, contentBudget) + normalizedMarker, true
}

func utf8Prefix(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}

	end := maxBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}
