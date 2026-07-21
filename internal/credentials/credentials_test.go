package credentials

import (
	"errors"
	"testing"

	"github.com/deqiying/fast-context/internal/config"
)

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

func TestFindAPIKeyPriority(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		localKey   string
		extractKey string
		wantKey    string
		wantSource string
	}{
		{
			name:       "fast context env wins",
			env:        map[string]string{"FAST_CONTEXT_KEY": "fast-key", "WINDSURF_API_KEY": "windsurf-key"},
			localKey:   "config-key",
			extractKey: "local-key",
			wantKey:    "fast-key",
			wantSource: "FAST_CONTEXT_KEY",
		},
		{
			name:       "local config wins over compatibility env",
			env:        map[string]string{"WINDSURF_API_KEY": "windsurf-key"},
			localKey:   "config-key",
			extractKey: "local-key",
			wantKey:    "config-key",
			wantSource: "config.json",
		},
		{
			name:       "windsurf env wins over local extraction",
			env:        map[string]string{"WINDSURF_API_KEY": "windsurf-key"},
			extractKey: "local-key",
			wantKey:    "windsurf-key",
			wantSource: "WINDSURF_API_KEY",
		},
		{
			name:       "local extraction is last",
			env:        map[string]string{},
			extractKey: "local-key",
			wantKey:    "local-key",
			wantSource: "fixture.toml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := struct{ load, extract int }{}
			getenv := func(name string) string { return tt.env[name] }
			loadLocal := func() (config.LocalConfig, config.LocalConfigInfo, error) {
				calls.load++
				return config.LocalConfig{APIKey: tt.localKey}, config.LocalConfigInfo{Path: "config.json"}, nil
			}
			extract := func(string) (Info, error) {
				calls.extract++
				return Info{APIKey: tt.extractKey, SourcePath: "fixture.toml", SourceType: "devin_cli_credentials"}, nil
			}

			got, err := findAPIKey(getenv, loadLocal, extract)
			if err != nil {
				t.Fatalf("findAPIKey() error = %v", err)
			}
			if got.APIKey != tt.wantKey || got.SourcePath != tt.wantSource {
				t.Fatalf("findAPIKey() = %#v, want key=%q source=%q", got, tt.wantKey, tt.wantSource)
			}
			switch tt.name {
			case "fast context env wins":
				if calls.load != 0 || calls.extract != 0 {
					t.Fatalf("high-priority env called lower sources: %#v", calls)
				}
			case "local config wins over compatibility env":
				if calls.extract != 0 {
					t.Fatalf("local config called extractor: %#v", calls)
				}
			}
		})
	}
}

func TestFindAPIKeyBlankValuesContinue(t *testing.T) {
	getenv := func(name string) string {
		if name == "FAST_CONTEXT_KEY" {
			return "  "
		}
		if name == "WINDSURF_API_KEY" {
			return "windsurf-key"
		}
		return ""
	}
	got, err := findAPIKey(getenv,
		func() (config.LocalConfig, config.LocalConfigInfo, error) {
			return config.LocalConfig{APIKey: "  "}, config.LocalConfigInfo{Path: "config.json"}, nil
		},
		func(string) (Info, error) {
			t.Fatal("extract should not be called when WINDSURF_API_KEY is usable")
			return Info{}, nil
		})
	if err != nil || got.APIKey != "windsurf-key" || got.SourcePath != "WINDSURF_API_KEY" {
		t.Fatalf("findAPIKey() = %#v, %v", got, err)
	}
}

func TestFindAPIKeyExplicitTruncatedSourcesFailFast(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		load config.LocalConfig
	}{
		{name: "environment", env: map[string]string{"FAST_CONTEXT_KEY": "devin-session-token$abc"}},
		{name: "config", load: config.LocalConfig{APIKey: "devin-session-token$abc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(name string) string { return tc.env[name] }
			called := false
			got, err := findAPIKey(getenv,
				func() (config.LocalConfig, config.LocalConfigInfo, error) {
					return tc.load, config.LocalConfigInfo{Path: "config.json"}, nil
				},
				func(string) (Info, error) {
					called = true
					return Info{}, nil
				})
			if err == nil || called {
				t.Fatalf("findAPIKey() = %#v, %v; want fail-fast without extraction", got, err)
			}
			if tc.name == "environment" && got.SourcePath != "FAST_CONTEXT_KEY" {
				t.Fatalf("source = %q, want FAST_CONTEXT_KEY", got.SourcePath)
			}
			if tc.name == "config" && got.SourceType != "fast_context_config" {
				t.Fatalf("source type = %q, want fast_context_config", got.SourceType)
			}
		})
	}
}

func TestFindAPIKeyInvalidLocalConfigBlocksFallback(t *testing.T) {
	called := false
	got, err := findAPIKey(func(name string) string {
		if name == "WINDSURF_API_KEY" {
			return "windsurf-key"
		}
		return ""
	},
		func() (config.LocalConfig, config.LocalConfigInfo, error) {
			return config.LocalConfig{}, config.LocalConfigInfo{Path: "config.json", Exists: true}, errors.New("parse fast-context config")
		},
		func(string) (Info, error) {
			called = true
			return Info{APIKey: "local-key"}, nil
		})
	if err == nil || called || got.SourcePath != "config.json" || got.SourceType != "fast_context_config" {
		t.Fatalf("findAPIKey() = %#v, %v, extractor called=%v; want config error", got, err, called)
	}
}

func TestFindAPIKeyHomeResolutionFailurePreservesFallback(t *testing.T) {
	got, err := findAPIKey(func(name string) string {
		if name == "WINDSURF_API_KEY" {
			return "windsurf-key"
		}
		return ""
	},
		func() (config.LocalConfig, config.LocalConfigInfo, error) {
			return config.LocalConfig{}, config.LocalConfigInfo{}, errors.New("resolve fast-context config home")
		},
		func(string) (Info, error) {
			t.Fatal("extract should not be called when compatibility env is usable")
			return Info{}, nil
		})
	if err != nil || got.APIKey != "windsurf-key" {
		t.Fatalf("findAPIKey() = %#v, %v", got, err)
	}
}
