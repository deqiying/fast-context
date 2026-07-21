package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalConfig is the user-level configuration read by the CLI. Keep this
// schema deliberately small until additional fields have a defined runtime
// meaning.
type LocalConfig struct {
	APIKey string `json:"api_key"`
}

// LocalConfigInfo describes the default local configuration path without
// exposing its contents.
type LocalConfigInfo struct {
	Path   string
	Exists bool
}

// DefaultLocalPath returns the fixed cross-platform user configuration path.
// It intentionally uses UserHomeDir rather than UserConfigDir so Windows,
// Linux, and macOS share the same $HOME/.config/fast-context location.
func DefaultLocalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve fast-context config home: %w", err)
	}
	return filepath.Join(home, ".config", "fast-context", "config.json"), nil
}

// LoadLocal reads the default user-level configuration. A missing file is an
// unset configuration, while an existing but unreadable or invalid file is a
// hard error so a damaged configuration is never silently bypassed.
func LoadLocal() (LocalConfig, LocalConfigInfo, error) {
	path, err := DefaultLocalPath()
	if err != nil {
		return LocalConfig{}, LocalConfigInfo{}, err
	}
	info := LocalConfigInfo{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LocalConfig{}, info, nil
		}
		info.Exists = true
		return LocalConfig{}, info, fmt.Errorf("read fast-context config %q: %w", path, err)
	}
	info.Exists = true
	config, err := decodeLocalFile(path, data)
	if err != nil {
		return LocalConfig{}, info, err
	}
	return config, info, nil
}

// LoadLocalFile strictly parses one local configuration file. It does not
// create, rewrite, or chmod the target and is intended to be used by tests and
// callers that already resolved an explicit path.
func LoadLocalFile(path string) (LocalConfig, error) {
	if strings.TrimSpace(path) == "" {
		return LocalConfig{}, errors.New("fast-context config path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return LocalConfig{}, fmt.Errorf("read fast-context config %q: %w", path, err)
	}
	return decodeLocalFile(path, data)
}

func decodeLocalFile(path string, data []byte) (LocalConfig, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var wire *struct {
		APIKey json.RawMessage `json:"api_key"`
	}
	if err := decoder.Decode(&wire); err != nil {
		if errors.Is(err, io.EOF) {
			return LocalConfig{}, fmt.Errorf("parse fast-context config %q: empty file", path)
		}
		return LocalConfig{}, fmt.Errorf("parse fast-context config %q: %w", path, err)
	}
	if wire == nil {
		return LocalConfig{}, fmt.Errorf("parse fast-context config %q: top-level value must be an object", path)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return LocalConfig{}, fmt.Errorf("parse fast-context config %q: trailing JSON document", path)
		}
		return LocalConfig{}, fmt.Errorf("parse fast-context config %q: trailing data: %w", path, err)
	}

	config := LocalConfig{}
	if wire.APIKey != nil {
		if bytes.Equal(bytes.TrimSpace(wire.APIKey), []byte("null")) {
			return LocalConfig{}, fmt.Errorf("parse fast-context config %q: api_key must be a string", path)
		}
		if err := json.Unmarshal(wire.APIKey, &config.APIKey); err != nil {
			return LocalConfig{}, fmt.Errorf("parse fast-context config %q: api_key must be a string: %w", path, err)
		}
	}
	config.APIKey = strings.TrimSpace(config.APIKey)
	return config, nil
}
