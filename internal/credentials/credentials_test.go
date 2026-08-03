package credentials

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deqiying/fast-context/internal/config"
)

func writeStateDBFixture(t *testing.T, rawValue string, insertRecord bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if insertRecord {
		if _, err := db.Exec("INSERT INTO ItemTable (key, value) VALUES (?, ?)", "windsurfAuthStatus", rawValue); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func snapshotFiles(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "fast-context-state-*.vscdb"))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		out[path] = struct{}{}
	}
	return out
}

func assertNoNewSnapshots(t *testing.T, dir string, before map[string]struct{}) {
	t.Helper()
	for path := range snapshotFiles(t, dir) {
		if _, ok := before[path]; !ok {
			t.Fatalf("temporary state database snapshot was not removed: %s", path)
		}
	}
}

func isolatedTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	dir := os.TempDir()
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		t.Fatalf("os.TempDir() = %q, want path inside %q", dir, root)
	}
	return dir
}

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

func TestAutomaticCredentialSources(t *testing.T) {
	home := filepath.Join("fixture", "home")
	tests := []struct {
		goos string
		want []source
	}{
		{goos: "windows"},
		{goos: "darwin"},
		{
			goos: "linux",
			want: []source{{
				kind: "toml",
				path: filepath.Join(home, ".local", "share", "devin", "credentials.toml"),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			got := automaticCredentialSources(tt.goos, home)
			if len(got) != len(tt.want) {
				t.Fatalf("automaticCredentialSources(%q) = %#v, want %#v", tt.goos, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("automaticCredentialSources(%q)[%d] = %#v, want %#v", tt.goos, i, got[i], tt.want[i])
				}
				if strings.EqualFold(filepath.Base(got[i].path), "state.vscdb") {
					t.Fatalf("automatic source contains state.vscdb: %s", got[i].path)
				}
			}
		})
	}
}

func TestExtractExplicitSources(t *testing.T) {
	tomlPath := filepath.Join(t.TempDir(), "credentials.toml")
	if err := os.WriteFile(tomlPath, []byte(`api_key = "toml-key"`), 0o600); err != nil {
		t.Fatal(err)
	}
	tomlInfo, err := Extract(tomlPath)
	if err != nil || tomlInfo.APIKey != "toml-key" || tomlInfo.SourceType != "devin_cli_credentials" {
		t.Fatalf("Extract(TOML) = %#v, %v", tomlInfo, err)
	}

	dbPath := writeStateDBFixture(t, `{"apiKey":"devin-session-token$eyJfixture"}`, true)
	dbInfo, err := Extract(dbPath)
	if err != nil {
		t.Fatalf("Extract(SQLite) error = %v", err)
	}
	if dbInfo.APIKey != "devin-session-token$eyJfixture" || dbInfo.SourcePath != dbPath || dbInfo.SourceType != "windsurf_state_db" {
		t.Fatalf("Extract(SQLite) = %#v", dbInfo)
	}
}

func TestExtractSQLiteErrors(t *testing.T) {
	tests := []struct {
		name    string
		path    func(*testing.T) string
		wantErr string
	}{
		{
			name: "missing record",
			path: func(t *testing.T) string {
				return writeStateDBFixture(t, "", false)
			},
			wantErr: "windsurfAuthStatus record not found",
		},
		{
			name: "malformed payload",
			path: func(t *testing.T) string {
				return writeStateDBFixture(t, "not-json", true)
			},
			wantErr: "windsurfAuthStatus data parse failed",
		},
		{
			name: "missing file",
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing.state.vscdb")
			},
			wantErr: "Windsurf/Devin credential source not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := Extract(tt.path(t))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Extract() = %#v, %v; want error containing %q", info, err, tt.wantErr)
			}
			if info.APIKey != "" {
				t.Fatalf("Extract() returned key on failure: %#v", info)
			}
		})
	}
}

func TestExtractPreservesCredentialSourceStatError(t *testing.T) {
	path := "invalid\x00credentials.toml"
	info, err := Extract(path)
	if err == nil || !strings.Contains(err.Error(), "failed to inspect credential source") {
		t.Fatalf("Extract() = %#v, %v; want source inspection error", info, err)
	}
	if errors.Is(err, errCredentialSourceNotFound) {
		t.Fatalf("Extract() disguised source inspection error as not found: %v", err)
	}
	if info.SourcePath != path || info.SourceType != "devin_cli_credentials" || info.APIKey != "" {
		t.Fatalf("Extract() returned unexpected source info: %#v", info)
	}
}

func TestExtractSQLiteCleansTemporarySnapshot(t *testing.T) {
	tests := []struct {
		name      string
		rawValue  string
		wantError bool
	}{
		{name: "success", rawValue: `{"apiKey":"fixture-key"}`},
		{name: "parse failure", rawValue: "not-json", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := isolatedTempDir(t)
			before := snapshotFiles(t, tempDir)
			_, err := Extract(writeStateDBFixture(t, tt.rawValue, true))
			if (err != nil) != tt.wantError {
				t.Fatalf("Extract() error = %v, wantError=%t", err, tt.wantError)
			}
			assertNoNewSnapshots(t, tempDir, before)
		})
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

func TestFindAPIKeyTruncatedWindsurfRecovery(t *testing.T) {
	t.Run("uses automatic TOML recovery", func(t *testing.T) {
		calls := 0
		got, err := findAPIKey(func(name string) string {
			if name == "WINDSURF_API_KEY" {
				return "devin-session-token$abc"
			}
			return ""
		}, func() (config.LocalConfig, config.LocalConfigInfo, error) {
			return config.LocalConfig{}, config.LocalConfigInfo{Path: "config.json"}, nil
		}, func(string) (Info, error) {
			calls++
			return Info{APIKey: "devin-session-token$eyJfixture", SourcePath: "fixture.toml", SourceType: "devin_cli_credentials"}, nil
		})
		if err != nil || calls != 1 || got.SourcePath != "fixture.toml" || got.APIKey == "" {
			t.Fatalf("findAPIKey() = %#v, %v; calls=%d", got, err, calls)
		}
	})

	t.Run("fails without automatic recovery", func(t *testing.T) {
		calls := 0
		got, err := findAPIKey(func(name string) string {
			if name == "WINDSURF_API_KEY" {
				return "devin-session-token$abc"
			}
			return ""
		}, func() (config.LocalConfig, config.LocalConfigInfo, error) {
			return config.LocalConfig{}, config.LocalConfigInfo{Path: "config.json"}, nil
		}, func(string) (Info, error) {
			calls++
			return Info{TriedPaths: []string{"fixture.toml"}}, errCredentialSourceNotFound
		})
		if err == nil || calls != 1 {
			t.Fatalf("findAPIKey() = %#v, %v; calls=%d", got, err, calls)
		}
		if got.APIKey != "" || strings.Contains(err.Error(), "devin-session-token$abc") {
			t.Fatalf("truncated key leaked from findAPIKey(): %#v, %v", got, err)
		}
		for _, want := range []string{"FAST_CONTEXT_KEY", "$HOME/.config/fast-context/config.json", "WINDSURF_API_KEY"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("findAPIKey() error missing %q: %v", want, err)
			}
		}
	})

	t.Run("rejects truncated automatic recovery", func(t *testing.T) {
		got, err := findAPIKey(func(name string) string {
			if name == "WINDSURF_API_KEY" {
				return "devin-session-token$abc"
			}
			return ""
		}, func() (config.LocalConfig, config.LocalConfigInfo, error) {
			return config.LocalConfig{}, config.LocalConfigInfo{Path: "config.json"}, nil
		}, func(string) (Info, error) {
			return Info{APIKey: "devin-session-token$also-truncated", SourcePath: "fixture.toml", SourceType: "devin_cli_credentials"}, nil
		})
		if err == nil || got.APIKey != "" || got.SourcePath != "fixture.toml" {
			t.Fatalf("findAPIKey() = %#v, %v; want truncated automatic credential failure", got, err)
		}
		if strings.Contains(err.Error(), "devin-session-token$also-truncated") {
			t.Fatalf("findAPIKey() leaked truncated automatic credential: %v", err)
		}
	})
}

func TestFindAPIKeyRejectsTruncatedAutomaticCredential(t *testing.T) {
	got, err := findAPIKey(func(string) string { return "" }, func() (config.LocalConfig, config.LocalConfigInfo, error) {
		return config.LocalConfig{}, config.LocalConfigInfo{Path: "config.json"}, nil
	}, func(string) (Info, error) {
		return Info{APIKey: "devin-session-token$abc", SourcePath: "fixture.toml", SourceType: "devin_cli_credentials"}, nil
	})
	if err == nil || got.APIKey != "" || got.SourcePath != "fixture.toml" {
		t.Fatalf("findAPIKey() = %#v, %v; want truncated automatic credential failure", got, err)
	}
	for _, want := range []string{"automatic Devin CLI credential appears truncated", "FAST_CONTEXT_KEY", "$HOME/.config/fast-context/config.json", "WINDSURF_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("findAPIKey() error missing %q: %v", want, err)
		}
	}
}

func TestFindAPIKeyMissingProvidesSetupHint(t *testing.T) {
	got, err := findAPIKey(func(string) string { return "" }, func() (config.LocalConfig, config.LocalConfigInfo, error) {
		return config.LocalConfig{}, config.LocalConfigInfo{Path: "config.json"}, nil
	}, func(string) (Info, error) {
		return Info{}, errCredentialSourceNotFound
	})
	if err == nil || got.APIKey != "" {
		t.Fatalf("findAPIKey() = %#v, %v", got, err)
	}
	for _, want := range []string{"FAST_CONTEXT_KEY", "$HOME/.config/fast-context/config.json", "WINDSURF_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("findAPIKey() error missing %q: %v", want, err)
		}
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
