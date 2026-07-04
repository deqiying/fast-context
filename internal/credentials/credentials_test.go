package credentials

import "testing"

func TestExtractAPIKeyFromToml(t *testing.T) {
	got := ExtractAPIKeyFromToml(`
# comment
windsurf_api_key = "sk-test_123"
`)
	if got != "sk-test_123" {
		t.Fatalf("got %q", got)
	}
}

func TestRedact(t *testing.T) {
	got := Redact("sk-abcdefghijklmnopqrstuvwxyz")
	if got != "sk-abc...wxyz" {
		t.Fatalf("got %q", got)
	}
}
