package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLocalFileStrictJSON(t *testing.T) {
	tests := []struct {
		name       string
		contents   string
		wantKey    string
		wantSubstr string
	}{
		{name: "valid and trims key", contents: `{"api_key":"  fixture-key  "}`, wantKey: "fixture-key"},
		{name: "empty key", contents: `{"api_key":"  "}`},
		{name: "empty file", contents: "", wantSubstr: "empty file"},
		{name: "null", contents: "null", wantSubstr: "top-level value must be an object"},
		{name: "array", contents: "[]", wantSubstr: "cannot unmarshal array"},
		{name: "null api key", contents: `{"api_key":null}`, wantSubstr: "api_key must be a string"},
		{name: "numeric api key", contents: `{"api_key":123}`, wantSubstr: "api_key must be a string"},
		{name: "unknown field", contents: `{"api_key":"key","base_url":"https://example.test"}`, wantSubstr: "unknown field"},
		{name: "trailing document", contents: `{"api_key":"key"}{}`, wantSubstr: "trailing JSON document"},
		{name: "invalid JSON", contents: `{"api_key":}`, wantSubstr: "parse fast-context config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadLocalFile(path)
			if tt.wantSubstr == "" {
				if err != nil {
					t.Fatalf("LoadLocalFile() error = %v", err)
				}
				if got.APIKey != tt.wantKey {
					t.Fatalf("APIKey = %q, want %q", got.APIKey, tt.wantKey)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("LoadLocalFile() error = %v, want substring %q", err, tt.wantSubstr)
			}
		})
	}
}

func TestLoadLocalFileMissingIsReadError(t *testing.T) {
	_, err := LoadLocalFile(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !strings.Contains(err.Error(), "read fast-context config") {
		t.Fatalf("LoadLocalFile() error = %v, want read error", err)
	}
}

func TestLoadLocalDoesNotCreateMissingConfig(t *testing.T) {
	path, err := DefaultLocalPath()
	if err != nil {
		t.Skipf("user home unavailable: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Skipf("default config already exists at %s", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat default config: %v", err)
	}

	got, info, err := LoadLocal()
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if info.Path != path || info.Exists || got.APIKey != "" {
		t.Fatalf("LoadLocal() = %#v, %#v; want missing empty config", got, info)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("LoadLocal() created or changed config path: stat error = %v", err)
	}
}

func TestLoadLocalFromDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".config", "fast-context", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"api_key":"fixture-config-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, info, err := LoadLocal()
	if err != nil {
		t.Fatalf("LoadLocal() error = %v", err)
	}
	if got.APIKey != "fixture-config-key" || info.Path != path || !info.Exists {
		t.Fatalf("LoadLocal() = %#v, %#v; want fixture config", got, info)
	}
}
