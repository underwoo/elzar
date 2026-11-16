package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPluginSettings(t *testing.T) {
	defaults := Defaults().Plugins
	if !defaults.Enabled || defaults.SiteDir != "/etc/elzar/plugins" || defaults.Timeout != 5*time.Second {
		t.Fatalf("defaults=%+v", defaults)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	text := "[plugins]\nenabled = false\nsite_dir = \"/site/plugins\"\ndisabled = [\"stdout\", \"stderr\"]\ntimeout = \"2s\"\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := settings.Plugins
	if p.Enabled || p.SiteDir != "/site/plugins" || len(p.Disabled) != 2 || p.Timeout != 2*time.Second {
		t.Fatalf("plugins=%+v", p)
	}
	if err := SaveColumns(path, []string{"name"}, nil); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(after), text) {
		t.Fatalf("plugin settings not preserved: %s %v", after, err)
	}
}

func TestInvalidPluginSettings(t *testing.T) {
	for _, content := range []string{
		"plugins = true",
		"[plugins]\nenabled = \"yes\"",
		"[plugins]\nsite_dir = 42",
		"[plugins]\ndisabled = \"stdout\"",
		"[plugins]\ndisabled = [1]",
		"[plugins]\ntimeout = 5",
		"[plugins]\ntimeout = \"0s\"",
		"[plugins]\ntimeout = \"-1s\"",
		"[plugins]\ntimeout = \"forever\"",
		"[plugins]\nunknown = true",
	} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "plugins") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
