// Package config loads the small set of user preferences used by the UI.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/underwoo/elzar/internal/plugin"
	"github.com/underwoo/elzar/internal/slurm"
)

const (
	DefaultRefreshInterval = 15 * time.Second
	DefaultRecentWindow    = time.Hour
)

type Settings struct {
	Plugins                plugin.Options
	RefreshInterval        time.Duration
	RecentWindow           time.Duration
	DefaultScope           slurm.Scope
	QueueColumns           []string
	RecentColumns          []string
	AccountColumns         []string
	AccountClusters        string
	AccountRefreshInterval time.Duration
	TabPrefix              string
	Path                   string
	LegacyPath             string
	Notice                 string
}

func Defaults() Settings {
	return Settings{Plugins: plugin.DefaultOptions(), RefreshInterval: DefaultRefreshInterval, RecentWindow: DefaultRecentWindow, DefaultScope: slurm.ScopeMe, AccountRefreshInterval: 5 * time.Minute, TabPrefix: "ctrl+t"}
}

// Load reads XDG_CONFIG_HOME/elzar/config.toml, falling back to the legacy
// slurmtui path and then the platform user config directory. A missing file
// keeps the built-in defaults.
func Load() (Settings, error) {
	path, err := ConfigPath()
	if err != nil {
		return Settings{}, err
	}
	settings, err := LoadFile(path)
	settings.Path = path
	return settings, err
}

func ConfigPath() (string, error) {
	base, err := configBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "elzar", "config.toml"), nil
}

func configBaseDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("find user config directory: %w", err)
		}
	}
	return base, nil
}

func LegacyPathFor(path string) (string, bool) {
	if filepath.Base(path) != "config.toml" {
		return "", false
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) != "elzar" {
		return "", false
	}
	return filepath.Join(filepath.Dir(dir), "slurmtui", "config.toml"), true
}

func LegacyNotice(legacyPath, newPath string) string {
	return fmt.Sprintf("using legacy config %s; move it to %s/", legacyPath, filepath.Dir(newPath))
}

func LoadFile(path string) (Settings, error) {
	settings := Defaults()
	readPath := path
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if legacyPath, ok := LegacyPathFor(path); ok {
			legacyData, legacyErr := os.ReadFile(legacyPath)
			if legacyErr == nil {
				data = legacyData
				readPath = legacyPath
				settings.LegacyPath = legacyPath
				settings.Notice = LegacyNotice(legacyPath, path)
			} else if !errors.Is(legacyErr, os.ErrNotExist) {
				return Settings{}, fmt.Errorf("open config %s: %w", legacyPath, legacyErr)
			} else {
				return settings, nil
			}
		} else {
			return settings, nil
		}
	} else if err != nil {
		return Settings{}, fmt.Errorf("open config %s: %w", path, err)
	}
	var document map[string]any
	_, err = toml.Decode(string(data), &document)
	if err != nil {
		return Settings{}, fmt.Errorf("decode config %s: %w", readPath, err)
	}
	for key, target := range map[string]*time.Duration{"refresh_interval": &settings.RefreshInterval, "recent_window": &settings.RecentWindow} {
		value, exists := document[key]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return Settings{}, fmt.Errorf("config %s:%d: expected quoted string", readPath, keyLine(data, key))
		}
		duration, parseErr := time.ParseDuration(text)
		if parseErr != nil || duration <= 0 {
			return Settings{}, fmt.Errorf("config %s:%d: %s must be a positive duration", readPath, keyLine(data, key), key)
		}
		*target = duration
	}
	if value, exists := document["default_scope"]; exists {
		scope, ok := value.(string)
		if !ok {
			return Settings{}, fmt.Errorf("config %s:%d: expected quoted string", readPath, keyLine(data, "default_scope"))
		}
		switch slurm.Scope(scope) {
		case slurm.ScopeMe, slurm.ScopeAll:
			settings.DefaultScope = slurm.Scope(scope)
		default:
			return Settings{}, fmt.Errorf("config %s:%d: default_scope must be %q or %q", readPath, keyLine(data, "default_scope"), slurm.ScopeMe, slurm.ScopeAll)
		}
	}
	if rawColumns, ok := document["columns"].(map[string]any); ok {
		if values, ok := rawColumns["queue"].([]any); ok {
			settings.QueueColumns = stringValues(values)
		}
		if values, ok := rawColumns["recent"].([]any); ok {
			settings.RecentColumns = stringValues(values)
		}
	}
	if raw, exists := document["accounts"]; exists {
		table, ok := raw.(map[string]any)
		if !ok {
			return Settings{}, fmt.Errorf("accounts must be a table")
		}
		if value, exists := table["clusters"]; exists {
			text, ok := value.(string)
			if !ok {
				return Settings{}, fmt.Errorf("accounts.clusters must be a string")
			}
			settings.AccountClusters = text
		}
		if value, exists := table["refresh_interval"]; exists {
			text, ok := value.(string)
			if !ok {
				return Settings{}, fmt.Errorf("accounts.refresh_interval must be a positive duration")
			}
			duration, err := time.ParseDuration(text)
			if err != nil || duration <= 0 {
				return Settings{}, fmt.Errorf("accounts.refresh_interval must be a positive duration")
			}
			settings.AccountRefreshInterval = duration
		}
	}
	if rawColumns, ok := document["columns"].(map[string]any); ok {
		if values, ok := rawColumns["accounts"].([]any); ok {
			settings.AccountColumns = stringValues(values)
		}
	}
	if rawKeys, ok := document["keys"].(map[string]any); ok {
		if value, exists := rawKeys["tab_prefix"]; exists {
			prefix, ok := value.(string)
			if !ok || !validKeyString(prefix) {
				return Settings{}, fmt.Errorf("config %s:%d: keys.tab_prefix must be a valid Bubble Tea key string", readPath, keyLine(data, "tab_prefix"))
			}
			if prefix == "space" {
				prefix = " "
			}
			settings.TabPrefix = prefix
		}
	}
	if raw, exists := document["plugins"]; exists {
		if err := loadPlugins(raw, &settings.Plugins); err != nil {
			return Settings{}, fmt.Errorf("config %s: %w", readPath, err)
		}
	}
	return settings, nil
}

func validKeyString(value string) bool {
	if value == " " {
		return true
	}
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	return validBaseKeyString(value)
}

func validBaseKeyString(value string) bool {
	if len([]rune(value)) == 1 {
		return true
	}
	known := map[string]bool{
		"tab": true, "enter": true, "esc": true, "space": true, "backspace": true, "delete": true, "insert": true,
		"up": true, "down": true, "left": true, "right": true, "home": true, "end": true, "pgup": true, "pgdown": true,
		"shift+tab": true, "ctrl+home": true, "ctrl+end": true, "shift+home": true, "shift+end": true,
		"ctrl+shift+home": true, "ctrl+shift+end": true, "ctrl+pgup": true, "ctrl+pgdown": true,
		"ctrl+up": true, "ctrl+down": true, "ctrl+left": true, "ctrl+right": true,
		"shift+up": true, "shift+down": true, "shift+left": true, "shift+right": true,
		"ctrl+shift+up": true, "ctrl+shift+down": true, "ctrl+shift+left": true, "ctrl+shift+right": true,
		"ctrl+@": true, "ctrl+\\": true, "ctrl+]": true, "ctrl+^": true, "ctrl+_": true,
	}
	for i := 1; i <= 20; i++ {
		known[fmt.Sprintf("f%d", i)] = true
	}
	if known[value] {
		return true
	}
	if strings.HasPrefix(value, "ctrl+") {
		key := strings.TrimPrefix(value, "ctrl+")
		return len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' || known[value]
	}
	if strings.HasPrefix(value, "alt+") {
		return validBaseKeyString(strings.TrimPrefix(value, "alt+"))
	}
	return false
}

func stringValues(values []any) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func keyLine(data []byte, key string) int {
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+" =") {
			return i + 1
		}
	}
	return 1
}

var tableHeader = regexp.MustCompile(`^\s*\[[^\[][^]]*\]\s*(?:#.*)?$`)

// SaveColumns replaces just the top-level [columns] table, preserving all
// other bytes. The document is parsed before writing, and the replacement is
// installed with a same-directory atomic rename.
func SaveColumns(path string, queue, recent []string, accountColumns ...[]string) error {
	var accounts []string
	original, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		original = nil
		if legacyPath, ok := LegacyPathFor(path); ok {
			if legacyBytes, legacyErr := os.ReadFile(legacyPath); legacyErr == nil {
				var legacyDoc map[string]any
				if _, err := toml.Decode(string(legacyBytes), &legacyDoc); err != nil {
					return fmt.Errorf("decode config %s: %w", legacyPath, err)
				}
				if err := copyFileExact(legacyPath, path, legacyBytes); err != nil {
					return err
				}
				original = legacyBytes
			} else if !errors.Is(legacyErr, os.ErrNotExist) {
				return fmt.Errorf("read config %s: %w", legacyPath, legacyErr)
			}
		}
	} else if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if len(original) != 0 {
		var document map[string]any
		if _, err := toml.Decode(string(original), &document); err != nil {
			return fmt.Errorf("decode config %s: %w", path, err)
		}
		if cols, ok := document["columns"].(map[string]any); ok {
			if values, ok := cols["accounts"].([]any); ok {
				accounts = stringValues(values)
			}
		}
	}
	if len(accountColumns) > 0 {
		accounts = accountColumns[0]
	}
	replacement := formatColumns(queue, recent, accounts)
	updated, found, err := replaceColumns(original, replacement)
	if err != nil {
		return err
	}
	if !found {
		if len(updated) > 0 && updated[len(updated)-1] != '\n' {
			updated = append(updated, '\n')
		}
		updated = append(updated, '\n')
		updated = append(updated, replacement...)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tempPath := tmp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set config permissions: %w", err)
	}
	if _, err := tmp.Write(updated); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func copyFileExact(src, dst string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(src); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return fmt.Errorf("copy legacy config: %w", err)
	}
	return nil
}

func formatColumns(queue, recent []string, accounts ...[]string) []byte {
	var out bytes.Buffer
	fmt.Fprintln(&out, "[columns]")
	writeArray := func(name string, values []string) {
		fmt.Fprintf(&out, "%s = [", name)
		for i, value := range values {
			if i > 0 {
				fmt.Fprint(&out, ", ")
			}
			fmt.Fprintf(&out, "%q", value)
		}
		fmt.Fprintln(&out, "]")
	}
	writeArray("queue", queue)
	writeArray("recent", recent)
	if len(accounts) > 0 && accounts[0] != nil {
		writeArray("accounts", accounts[0])
	}
	return out.Bytes()
}

func replaceColumns(original, replacement []byte) ([]byte, bool, error) {
	lines := bytes.SplitAfter(original, []byte("\n"))
	start, end, offset := -1, -1, 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(string(line), "\n"))
		if start < 0 && isColumnsHeader(trimmed) {
			start = offset
			end = i + 1
			offset += len(line)
			continue
		}
		if start >= 0 {
			if tableHeader.MatchString(trimmed) {
				end = i
				break
			}
			end = i + 1
		}
		offset += len(line)
	}
	if start < 0 {
		return append([]byte(nil), original...), false, nil
	}
	for end > 0 && end <= len(lines) && strings.TrimSpace(string(lines[end-1])) == "" {
		end--
	}
	endOffset := 0
	for i := 0; i < end; i++ {
		endOffset += len(lines[i])
	}
	updated := make([]byte, 0, len(original)-(endOffset-start)+len(replacement))
	updated = append(updated, original[:start]...)
	updated = append(updated, replacement...)
	updated = append(updated, original[endOffset:]...)
	return updated, true, nil
}

func isColumnsHeader(line string) bool {
	withoutComment := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
	return withoutComment == "[columns]"
}

// Override applies explicitly supplied command-line values to file settings.
func Override(settings Settings, refresh, window string, all bool) (Settings, error) {
	if refresh != "" {
		duration, err := time.ParseDuration(refresh)
		if err != nil || duration <= 0 {
			return Settings{}, fmt.Errorf("invalid --refresh value %q: expected a positive duration", refresh)
		}
		settings.RefreshInterval = duration
	}
	if window != "" {
		duration, err := time.ParseDuration(window)
		if err != nil || duration <= 0 {
			return Settings{}, fmt.Errorf("invalid --window value %q: expected a positive duration", window)
		}
		settings.RecentWindow = duration
	}
	if all {
		settings.DefaultScope = slurm.ScopeAll
	}
	return settings, nil
}
