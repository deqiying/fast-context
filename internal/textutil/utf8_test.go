package textutil

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeUTF8(t *testing.T) {
	valid := "hello，世界"
	if got, repaired := NormalizeUTF8(valid); got != valid || repaired {
		t.Fatalf("NormalizeUTF8(valid) = %q, %t", got, repaired)
	}

	invalid := string([]byte{'a', 0xff, 'b', 0xc3})
	got, repaired := NormalizeUTF8(invalid)
	if !repaired {
		t.Fatal("NormalizeUTF8(invalid) did not report repair")
	}
	if want := "a\uFFFDb\uFFFD"; got != want {
		t.Fatalf("NormalizeUTF8(invalid) = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("NormalizeUTF8(invalid) returned invalid UTF-8: %q", got)
	}
}

func TestTruncateUTF8BytesPreservesRuneBoundaries(t *testing.T) {
	text := strings.Repeat("a", 249) + "汉"
	got, truncated := TruncateUTF8Bytes(text, 250, "")
	if !truncated {
		t.Fatal("TruncateUTF8Bytes did not report truncation")
	}
	if len(got) > 250 || !utf8.ValidString(got) {
		t.Fatalf("result bytes=%d valid=%t", len(got), utf8.ValidString(got))
	}
	if want := strings.Repeat("a", 249); got != want {
		t.Fatalf("TruncateUTF8Bytes = %q, want %q", got, want)
	}

	for _, budget := range []int{4000, 8000, 20000} {
		t.Run(strconv.Itoa(budget), func(t *testing.T) {
			text := strings.Repeat("b", budget-1) + "😀"
			got, truncated := TruncateUTF8Bytes(text, budget, "")
			if !truncated || len(got) != budget-1 || !utf8.ValidString(got) {
				t.Fatalf("budget=%d bytes=%d truncated=%t valid=%t", budget, len(got), truncated, utf8.ValidString(got))
			}
		})
	}
}

func TestTruncateUTF8BytesCountsMarkerInBudget(t *testing.T) {
	unchanged, truncated := TruncateUTF8Bytes("abc", 3, "...")
	if unchanged != "abc" || truncated {
		t.Fatalf("TruncateUTF8Bytes unchanged = %q, %t", unchanged, truncated)
	}

	got, truncated := TruncateUTF8Bytes("abcdef", 5, "...")
	if got != "ab..." || !truncated {
		t.Fatalf("TruncateUTF8Bytes = %q, %t; want %q, true", got, truncated, "ab...")
	}
	if len(got) != 5 || !utf8.ValidString(got) {
		t.Fatalf("result bytes=%d valid=%t", len(got), utf8.ValidString(got))
	}

	invalidMarker := string([]byte{0xff})
	got, truncated = TruncateUTF8Bytes("abcdef", 5, invalidMarker)
	if want := "ab\uFFFD"; got != want || !truncated {
		t.Fatalf("TruncateUTF8Bytes invalid marker = %q, %t; want %q, true", got, truncated, want)
	}
}

func TestTruncateUTF8BytesHandlesSmallBudgets(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		maxBytes  int
		marker    string
		want      string
		truncated bool
	}{
		{name: "zero", text: "abc", maxBytes: 0, marker: "...", want: "", truncated: true},
		{name: "negative", text: "abc", maxBytes: -1, marker: "...", want: "", truncated: true},
		{name: "empty input", text: "", maxBytes: 0, marker: "...", want: "", truncated: false},
		{name: "budget smaller than multibyte marker", text: "abc", maxBytes: 2, marker: "…", want: "", truncated: true},
		{name: "marker prefix", text: "abcdef", maxBytes: 1, marker: "XY", want: "X", truncated: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := TruncateUTF8Bytes(tt.text, tt.maxBytes, tt.marker)
			if got != tt.want || truncated != tt.truncated {
				t.Fatalf("TruncateUTF8Bytes = %q, %t; want %q, %t", got, truncated, tt.want, tt.truncated)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("result is invalid UTF-8: %q", got)
			}
			if tt.maxBytes >= 0 && len(got) > tt.maxBytes {
				t.Fatalf("result bytes=%d exceeds budget=%d", len(got), tt.maxBytes)
			}
		})
	}
}

func TestTruncateUTF8BytesRepairsInvalidInput(t *testing.T) {
	invalid := string([]byte{'a', 0xff, 'b'})
	got, truncated := TruncateUTF8Bytes(invalid, 10, "...")
	if want := "a\uFFFDb"; got != want || truncated {
		t.Fatalf("TruncateUTF8Bytes = %q, %t; want %q, false", got, truncated, want)
	}
	if !utf8.ValidString(got) || len(got) > 10 {
		t.Fatalf("result bytes=%d valid=%t", len(got), utf8.ValidString(got))
	}
}
