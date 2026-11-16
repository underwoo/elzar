// Package plugin loads declarative plugins without depending on the UI or Slurm.
package plugin

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type Fields map[string]string

// Manifest describes a command or a JSON protocol executable.
type Manifest struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Type        string   `yaml:"type"`
	Key         string   `yaml:"key"`
	Scopes      []string `yaml:"scopes"`
	Command     string   `yaml:"command"`
	Args        []string `yaml:"args"`
	Mode        string   `yaml:"mode"`
	Confirm     bool     `yaml:"confirm"`
	Provides    []string `yaml:"provides"`
}

// Parse accepts exactly one YAML document and rejects unknown fields.
func Parse(data []byte) (Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("decode manifest: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err != nil {
			return m, fmt.Errorf("decode trailing document: %w", err)
		}
		return m, fmt.Errorf("expected one YAML document")
	}
	return m, m.Validate()
}

func (m Manifest) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"name", m.Name}, {"description", m.Description}, {"type", m.Type},
		{"command", m.Command},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	switch m.Type {
	case "command":
		if err := m.validateCommand(); err != nil {
			return err
		}
	case "exec":
		if m.Key != "" || len(m.Scopes) > 0 || m.Mode != "" || m.Confirm {
			return fmt.Errorf("exec plugins cannot specify key, scopes, mode, or confirm")
		}
		if len(m.Provides) == 0 {
			return fmt.Errorf("provides is required for exec plugins")
		}
		seen := map[string]bool{}
		for _, provided := range m.Provides {
			if provided != "columns" && provided != "detail" {
				return fmt.Errorf("unknown provides value %q", provided)
			}
			if seen[provided] {
				return fmt.Errorf("duplicate provides value %q", provided)
			}
			seen[provided] = true
		}
	default:
		return fmt.Errorf("unknown plugin type %q", m.Type)
	}
	for i, source := range append([]string{m.Command}, m.Args...) {
		if strings.ContainsRune(source, 0) {
			return fmt.Errorf("template %d contains NUL", i)
		}
		if _, err := parseTemplate(source); err != nil {
			return fmt.Errorf("template %d: %w", i, err)
		}
	}
	return nil
}

func (m Manifest) validateCommand() error {
	if m.Key == "" {
		return fmt.Errorf("key is required")
	}
	if m.Mode == "" {
		return fmt.Errorf("mode is required")
	}
	if len(m.Provides) > 0 {
		return fmt.Errorf("command plugins cannot specify provides")
	}
	if !validKey(m.Key) {
		return fmt.Errorf("invalid key %q: expected one key, ctrl+x, or alt+x (no chords)", m.Key)
	}
	if m.Mode != "foreground" && m.Mode != "background" {
		return fmt.Errorf("unknown mode %q", m.Mode)
	}
	if len(m.Scopes) == 0 {
		return fmt.Errorf("scopes is required")
	}
	seen := map[string]bool{}
	for _, scope := range m.Scopes {
		switch scope {
		case "queue", "recent", "accounts", "detail":
		default:
			return fmt.Errorf("unknown scope %q", scope)
		}
		if seen[scope] {
			return fmt.Errorf("duplicate scope %q", scope)
		}
		seen[scope] = true
	}
	return nil
}

func validKey(key string) bool {
	if strings.HasPrefix(key, "ctrl+") {
		suffix := strings.TrimPrefix(key, "ctrl+")
		return len(suffix) == 1 && suffix[0] >= 'a' && suffix[0] <= 'z'
	}
	key = strings.TrimPrefix(key, "alt+")
	if !utf8.ValidString(key) || utf8.RuneCountInString(key) != 1 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(key)
	return unicode.IsPrint(r) && !unicode.IsSpace(r)
}
