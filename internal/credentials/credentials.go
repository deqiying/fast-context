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

	"github.com/deqiying/fast-context/internal/config"

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

var errCredentialSourceNotFound = errors.New("Windsurf/Devin credential source not found")
var errAutomaticCredentialTruncated = errors.New("automatic Devin CLI credential appears truncated; repair the '$' credential value")

const credentialSetupHint = "set FAST_CONTEXT_KEY, set api_key in $HOME/.config/fast-context/config.json, or set WINDSURF_API_KEY"

type localConfigLoader func() (config.LocalConfig, config.LocalConfigInfo, error)
type credentialExtractor func(string) (Info, error)

// FindAPIKey resolves credentials in the documented runtime order:
// FAST_CONTEXT_KEY, fixed local config, WINDSURF_API_KEY, then the Linux/WSL
// Devin CLI TOML source.
func FindAPIKey() (Info, error) {
	return findAPIKey(os.Getenv, config.LoadLocal, Extract)
}

func findAPIKey(getenv func(string) string, loadLocal localConfigLoader, extract credentialExtractor) (Info, error) {
	if key := strings.TrimSpace(getenv("FAST_CONTEXT_KEY")); key != "" {
		if LooksTruncated(key) {
			return Info{APIKey: key, SourcePath: "FAST_CONTEXT_KEY", SourceType: "env"}, errors.New("FAST_CONTEXT_KEY appears truncated; quote or escape the '$' character in the value")
		}
		return Info{APIKey: key, SourcePath: "FAST_CONTEXT_KEY", SourceType: "env"}, nil
	}

	localConfig, localInfo, localErr := loadLocal()
	if localErr != nil {
		// UserHomeDir failure has no path to report and should preserve the
		// historical environment/local-credential fallback. Once a concrete
		// config path exists, read/parse errors are intentionally fail-fast.
		if localInfo.Path != "" {
			return Info{SourcePath: localInfo.Path, SourceType: "fast_context_config"}, localErr
		}
	} else if key := strings.TrimSpace(localConfig.APIKey); key != "" {
		if LooksTruncated(key) {
			return Info{APIKey: key, SourcePath: localInfo.Path, SourceType: "fast_context_config"}, errors.New("fast-context config api_key appears truncated; repair the '$' credential value")
		}
		return Info{APIKey: key, SourcePath: localInfo.Path, SourceType: "fast_context_config"}, nil
	}

	if key := strings.TrimSpace(getenv("WINDSURF_API_KEY")); key != "" {
		// Upstream 1.5.2: keys look like `devin-session-token$<JWT>`; shells and
		// TOML env loaders can eat `$eyJ...` as a variable expansion, leaving a
		// truncated key that always yields HTTP 401. Distrust it and fall back
		// to the remaining automatic TOML source so the user can self-heal
		// without reading a Windsurf/Devin state database implicitly.
		if !LooksTruncated(key) {
			return Info{APIKey: key, SourcePath: "WINDSURF_API_KEY", SourceType: "env"}, nil
		}
		info, err := extract("")
		info, err = validateAutomaticCredential(info, err)
		if err == nil && info.APIKey != "" {
			return info, nil
		}
		info.APIKey = ""
		if info.SourcePath == "" {
			info.SourcePath = "WINDSURF_API_KEY"
			info.SourceType = "env"
		}
		return info, truncatedWindsurfAPIKeyError(err)
	}
	info, err := extract("")
	info, err = validateAutomaticCredential(info, err)
	if err == nil && info.APIKey != "" {
		return info, nil
	}
	if err == nil || errors.Is(err, errCredentialSourceNotFound) {
		return info, errors.New("fast-context credential not configured; " + credentialSetupHint)
	}
	if errors.Is(err, errAutomaticCredentialTruncated) {
		return info, errors.New(err.Error() + "; " + credentialSetupHint)
	}
	return info, err
}

func validateAutomaticCredential(info Info, err error) (Info, error) {
	if err == nil && info.APIKey != "" && LooksTruncated(info.APIKey) {
		info.APIKey = ""
		return info, errAutomaticCredentialTruncated
	}
	return info, err
}

func truncatedWindsurfAPIKeyError(recoveryErr error) error {
	const prefix = "WINDSURF_API_KEY appears truncated; quote or escape the '$' character"
	if recoveryErr == nil || errors.Is(recoveryErr, errCredentialSourceNotFound) {
		return errors.New(prefix + "; " + credentialSetupHint)
	}
	return fmt.Errorf("%s; automatic Devin CLI credential recovery failed: %w; %s", prefix, recoveryErr, credentialSetupHint)
}

// LooksTruncated reports whether a manually supplied key appears to have lost
// its `$<JWT>` tail to shell/TOML variable expansion.
func LooksTruncated(key string) bool {
	k := strings.TrimSpace(key)
	if !strings.HasPrefix(k, "devin-session-token") {
		return false
	}
	dollar := strings.Index(k, "$")
	if dollar == -1 {
		return true
	}
	return !strings.HasPrefix(k[dollar+1:], "eyJ")
}

func Extract(explicitPath string) (Info, error) {
	sources, err := credentialSources(explicitPath)
	if err != nil {
		return Info{}, err
	}

	// Upstream 1.5.2 scans all existing sources and prefers the current
	// devin-session-token$ credential; older keys are kept as fallback.
	var tried []string
	var firstUsable *Info
	var firstExistingErr error
	var firstErrInfo Info
	for _, src := range sources {
		tried = append(tried, src.path)
		if _, statErr := os.Stat(src.path); statErr != nil {
			if !errors.Is(statErr, os.ErrNotExist) && firstExistingErr == nil {
				firstExistingErr = fmt.Errorf("failed to inspect credential source %s: %w", src.path, statErr)
				firstErrInfo = Info{SourcePath: src.path, SourceType: sourceType(src.kind)}
			}
			continue
		}

		var info Info
		var err error
		if src.kind == "toml" {
			info, err = extractFromToml(src.path)
		} else {
			info, err = extractFromSQLite(src.path)
		}
		if err == nil && info.APIKey != "" {
			if isPreferredAPIKey(info.APIKey) {
				info.TriedPaths = append([]string(nil), tried...)
				return info, nil
			}
			if firstUsable == nil {
				snapshot := info
				firstUsable = &snapshot
			}
			continue
		}
		if firstExistingErr == nil {
			firstExistingErr = err
			firstErrInfo = info
		}
	}

	if firstUsable != nil {
		firstUsable.TriedPaths = tried
		return *firstUsable, nil
	}
	if firstExistingErr != nil {
		firstErrInfo.TriedPaths = tried
		return firstErrInfo, firstExistingErr
	}
	return Info{TriedPaths: tried}, errCredentialSourceNotFound
}

func sourceType(kind string) string {
	if kind == "toml" {
		return "devin_cli_credentials"
	}
	return "windsurf_state_db"
}

// isPreferredAPIKey reports whether the key is a current Devin/Windsurf
// session token (devin-session-token$<JWT>).
func isPreferredAPIKey(key string) bool {
	return strings.HasPrefix(key, "devin-session-token$")
}

func SourceCandidates() []string {
	var out []string
	if path, err := config.DefaultLocalPath(); err == nil {
		out = append(out, path)
	}
	sources, err := credentialSources("")
	if err != nil {
		return out
	}
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

	return automaticCredentialSources(runtime.GOOS, home), nil
}

func automaticCredentialSources(goos, home string) []source {
	if goos != "linux" {
		return nil
	}
	return []source{{
		kind: "toml",
		path: filepath.Join(home, ".local", "share", "devin", "credentials.toml"),
	}}
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
