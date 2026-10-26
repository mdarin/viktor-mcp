package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/platform"
)

type settingsFile map[string]string

func settingsPath() (string, error) {
	if p := os.Getenv("VIKTOR_SETTINGS"); p != "" {
		return p, nil
	}
	dir, err := platform.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func loadSettings() (settingsFile, error) {
	path, err := settingsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settingsFile{}, nil
		}
		return nil, err
	}
	out := settingsFile{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}
	return out, nil
}

func saveSettings(s settingsFile) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, part := range strings.Split(lower, "_") {
		switch part {
		case "token", "secret", "password", "key":
			return true
		}
	}
	return false
}

var configKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

func validateConfigKey(key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if len(key) > 128 {
		return fmt.Errorf("key is too long (max 128)")
	}
	if !configKeyRe.MatchString(key) {
		return fmt.Errorf("invalid key %q: allowed [a-z0-9_.-], must start with letter or digit", key)
	}
	return nil
}

func handleConfigGet(_ *Registry) mcp.ToolHandlerFor[ConfigGetIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in ConfigGetIn) (*mcp.CallToolResult, any, error) {
		if err := validateConfigKey(in.Key); err != nil {
			return nil, nil, err
		}
		s, err := loadSettings()
		if err != nil {
			return nil, nil, err
		}
		v, ok := s[in.Key]
		if !ok {
			return nil, nil, fmt.Errorf("key %q not found (config_list shows available keys)", in.Key)
		}
		if isSecretKey(in.Key) {
			return nil, map[string]any{"key": in.Key, "token_set": true}, nil
		}
		return nil, map[string]any{"key": in.Key, "value": v}, nil
	}
}

func handleConfigSet(_ *Registry) mcp.ToolHandlerFor[ConfigSetIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in ConfigSetIn) (*mcp.CallToolResult, any, error) {
		if err := validateConfigKey(in.Key); err != nil {
			return nil, nil, err
		}
		s, err := loadSettings()
		if err != nil {
			return nil, nil, err
		}
		_, existed := s[in.Key]
		s[in.Key] = in.Value
		if err := saveSettings(s); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"key": in.Key, "saved": true, "created": !existed}, nil
	}
}

func handleConfigList(_ *Registry) mcp.ToolHandlerFor[ConfigListIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ ConfigListIn) (*mcp.CallToolResult, any, error) {
		s, err := loadSettings()
		if err != nil {
			return nil, nil, err
		}
		keys := make([]string, 0, len(s))
		for k := range s {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		rows := []map[string]any{}
		for _, k := range keys {
			rows = append(rows, map[string]any{"key": k, "token_set": isSecretKey(k)})
		}
		return nil, map[string]any{"keys": rows, "count": len(rows)}, nil
	}
}

func handleConfigUpdate(_ *Registry) mcp.ToolHandlerFor[ConfigUpdateIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in ConfigUpdateIn) (*mcp.CallToolResult, any, error) {
		if err := validateConfigKey(in.Key); err != nil {
			return nil, nil, err
		}
		s, err := loadSettings()
		if err != nil {
			return nil, nil, err
		}
		if _, ok := s[in.Key]; !ok {
			return nil, nil, fmt.Errorf("key %q not found (use config_set to create)", in.Key)
		}
		s[in.Key] = in.Value
		if err := saveSettings(s); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"key": in.Key, "updated": true}, nil
	}
}

func handleConfigDelete(_ *Registry) mcp.ToolHandlerFor[ConfigDeleteIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in ConfigDeleteIn) (*mcp.CallToolResult, any, error) {
		if err := validateConfigKey(in.Key); err != nil {
			return nil, nil, err
		}
		s, err := loadSettings()
		if err != nil {
			return nil, nil, err
		}
		if _, ok := s[in.Key]; !ok {
			return nil, nil, fmt.Errorf("key %q not found", in.Key)
		}
		delete(s, in.Key)
		if err := saveSettings(s); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"key": in.Key, "deleted": true}, nil
	}
}
