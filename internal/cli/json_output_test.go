package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestJSONEncoderDefaultsCompactAndPrettyIsOptIn(t *testing.T) {
	value := map[string]any{
		"html":   "<tag>",
		"nested": map[string]any{"value": "line one\nline two"},
		"ok":     true,
	}
	var compact bytes.Buffer
	if err := newJSONEncoder(&compact, false).Encode(value); err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := newJSONEncoder(&pretty, true).Encode(value); err != nil {
		t.Fatal(err)
	}

	assertCompactJSONText(t, compact.String())
	assertPrettyJSONText(t, pretty.String())
	assertJSONSemanticallyEqual(t, compact.String(), pretty.String())
	if !strings.Contains(compact.String(), `\u003c`) || !strings.Contains(pretty.String(), `\u003c`) {
		t.Fatalf("JSON presentation changed default HTML escaping: compact=%q pretty=%q", compact.String(), pretty.String())
	}
}

func assertCompactJSONText(t *testing.T, value string) {
	t.Helper()
	if strings.HasPrefix(value, "\uFEFF") || !strings.HasSuffix(value, "\n") || strings.Count(value, "\n") != 1 {
		t.Fatalf("JSON is not one compact line with one trailing LF: %q", value)
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		t.Fatalf("compact output is not valid JSON: %v", err)
	}
}

func assertPrettyJSONText(t *testing.T, value string) {
	t.Helper()
	if strings.HasPrefix(value, "\uFEFF") || !strings.HasSuffix(value, "\n") || strings.HasSuffix(value, "\n\n") || strings.Count(value, "\n") <= 1 || !strings.Contains(value, "\n  \"") {
		t.Fatalf("JSON is not two-space pretty output with one trailing LF: %q", value)
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		t.Fatalf("pretty output is not valid JSON: %v", err)
	}
}

func assertJSONSemanticallyEqual(t *testing.T, left, right string) {
	t.Helper()
	var leftValue any
	if err := json.Unmarshal([]byte(left), &leftValue); err != nil {
		t.Fatal(err)
	}
	var rightValue any
	if err := json.Unmarshal([]byte(right), &rightValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leftValue, rightValue) {
		t.Fatalf("JSON values differ:\nleft=%#v\nright=%#v", leftValue, rightValue)
	}
}
