package credentials

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"
)

type Info struct {
	APIKey     string
	SourcePath string
	SourceType string
	TriedPaths []string
}

type source struct {
	kind string
	path string
}

var tomlFields = []string{
	"api_key",
	"apiKey",
	"devin_api_key",
	"devinApiKey",
	"windsurf_api_key",
	"windsurfApiKey",
	"access_token",
	"accessToken",
	"token",
}

func FindAPIKey() (Info, error) {
	if key := strings.TrimSpace(os.Getenv("WINDSURF_API_KEY")); key != "" {
		return Info{APIKey: key, SourcePath: "WINDSURF_API_KEY", SourceType: "env"}, nil
	}
	return Extract("")
}

func Extract(explicitPath string) (Info, error) {
	sources, err := credentialSources(explicitPath)
	if err != nil {
		return Info{}, err
	}

	var tried []string
	var firstExistingErr error
	var firstInfo Info
	for _, src := range sources {
		tried = append(tried, src.path)
		if _, err := os.Stat(src.path); err != nil {
			continue
		}

		var info Info
		var err error
		if src.kind == "toml" {
			info, err = extractFromToml(src.path)
		} else {
			info, err = extractFromSQLite(src.path)
		}
		info.TriedPaths = append([]string(nil), tried...)
		if err == nil && info.APIKey != "" {
			return info, nil
		}
		if firstExistingErr == nil {
			firstExistingErr = err
			firstInfo = info
		}
	}

	if firstExistingErr != nil {
		firstInfo.TriedPaths = tried
		return firstInfo, firstExistingErr
	}
	return Info{TriedPaths: tried}, errors.New("Windsurf/Devin credential source not found")
}

func SourceCandidates() []string {
	sources, err := credentialSources("")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(sources))
	for _, src := range sources {
		out = append(out, src.path)
	}
	return out
}

func credentialSources(explicitPath string) ([]source, error) {
	if explicitPath != "" {
		kind := "sqlite"
		if strings.EqualFold(filepath.Ext(explicitPath), ".toml") {
			kind = "toml"
		}
		return []source{{kind: kind, path: explicitPath}}, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var out []source
	if runtime.GOOS == "linux" {
		out = append(out, source{
			kind: "toml",
			path: filepath.Join(home, ".local", "share", "devin", "credentials.toml"),
		})
	}

	for _, p := range dbPathCandidates(runtime.GOOS, home, os.Environ()) {
		out = append(out, source{kind: "sqlite", path: p})
	}
	return out, nil
}

func dbPathCandidates(goos, home string, env []string) []string {
	envMap := map[string]string{}
	for _, item := range env {
		if k, v, ok := strings.Cut(item, "="); ok {
			envMap[k] = v
		}
	}

	switch goos {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		return []string{
			filepath.Join(base, "Deviv", "User", "globalStorage", "state.vscdb"),
			filepath.Join(base, "Windsurf", "User", "globalStorage", "state.vscdb"),
		}
	case "windows":
		appData := envMap["APPDATA"]
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return []string{
			filepath.Join(appData, "Deviv", "User", "globalStorage", "state.vscdb"),
			filepath.Join(appData, "Windsurf", "User", "globalStorage", "state.vscdb"),
		}
	default:
		configDir := envMap["XDG_CONFIG_HOME"]
		if configDir == "" {
			configDir = filepath.Join(home, ".config")
		}
		return []string{
			filepath.Join(configDir, "Deviv", "User", "globalStorage", "state.vscdb"),
			filepath.Join(configDir, "Windsurf", "User", "globalStorage", "state.vscdb"),
		}
	}
}

func extractFromToml(path string) (Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{SourcePath: path, SourceType: "devin_cli_credentials"}, fmt.Errorf("failed to read Devin CLI credentials: %w", err)
	}
	key := ExtractAPIKeyFromToml(string(data))
	if key == "" {
		return Info{SourcePath: path, SourceType: "devin_cli_credentials"}, errors.New("Devin CLI credentials did not contain an API key")
	}
	return Info{APIKey: key, SourcePath: path, SourceType: "devin_cli_credentials"}, nil
}

func ExtractAPIKeyFromToml(text string) string {
	for _, field := range tomlFields {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(field) + `\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s#]+))`)
		m := re.FindStringSubmatch(text)
		if len(m) == 0 {
			continue
		}
		for i := 1; i < len(m); i++ {
			if strings.TrimSpace(m[i]) != "" {
				return strings.TrimSpace(m[i])
			}
		}
	}
	fallback := regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+\b`).FindString(text)
	return fallback
}

func extractFromSQLite(path string) (Info, error) {
	tmpPath, cleanup, err := copyToTemp(path)
	if err != nil {
		return Info{SourcePath: path, SourceType: "windsurf_state_db"}, err
	}
	defer cleanup()

	db, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		return Info{SourcePath: path, SourceType: "windsurf_state_db"}, fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	var raw string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'windsurfAuthStatus'").Scan(&raw)
	if err != nil {
		return Info{SourcePath: path, SourceType: "windsurf_state_db"}, fmt.Errorf("windsurfAuthStatus record not found: %w", err)
	}

	var payload struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return Info{SourcePath: path, SourceType: "windsurf_state_db"}, fmt.Errorf("windsurfAuthStatus data parse failed: %w", err)
	}
	if payload.APIKey == "" {
		return Info{SourcePath: path, SourceType: "windsurf_state_db"}, errors.New("apiKey field is empty")
	}
	return Info{APIKey: payload.APIKey, SourcePath: path, SourceType: "windsurf_state_db"}, nil
}

func copyToTemp(path string) (string, func(), error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", func() {}, fmt.Errorf("failed to read database: %w", err)
	}
	tmp, err := os.CreateTemp("", "fast-context-state-*.vscdb")
	if err != nil {
		return "", func() {}, fmt.Errorf("failed to create database snapshot: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", func() {}, fmt.Errorf("failed to write database snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", func() {}, fmt.Errorf("failed to close database snapshot: %w", err)
	}
	return tmpPath, func() { _ = os.Remove(tmpPath) }, nil
}

func Redact(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len(key) <= 12 {
		return strings.Repeat("*", len(key))
	}
	return key[:6] + "..." + key[len(key)-4:]
}
