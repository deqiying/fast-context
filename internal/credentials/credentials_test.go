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

func TestLooksTruncated(t *testing.T) {
	cases := map[string]bool{
		"devin-session-token":            true,  // $ eaten entirely
		"devin-session-token$":           true,  // bare $, JWT gone
		"devin-session-token$abc":        true,  // $ kept but no JWT
		"devin-session-token$eyJhbGciOi": false, // intact
		"sk-regular-key":                 false, // different format
	}
	for key, want := range cases {
		if got := LooksTruncated(key); got != want {
			t.Fatalf("LooksTruncated(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestRedact(t *testing.T) {
	got := Redact("sk-abcdefghijklmnopqrstuvwxyz")
	if got != "sk-abc...wxyz" {
		t.Fatalf("got %q", got)
	}
}
