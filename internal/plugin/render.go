package plugin

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
)

func parseTemplate(source string) (*template.Template, error) {
	return template.New("plugin").Option("missingkey=error").Funcs(template.FuncMap{
		"shquote": func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" },
		"env": func(name, fallback string) string {
			if value := os.Getenv(name); value != "" {
				return value
			}
			return fallback
		},
		"default": func(fallback, value string) string {
			if value == "" {
				return fallback
			}
			return value
		},
	}).Parse(source)
}

// Render returns the executable name and its arguments, never a shell command.
// Each template produces exactly one argv element. Missing fields are errors,
// including fields passed to default. This function does not execute anything;
// callers must use CheckExecutable immediately before starting the command.
func Render(m Manifest, fields Fields) (name string, argv []string, err error) {
	if err = m.Validate(); err != nil {
		return "", nil, err
	}
	rendered := make([]string, 0, len(m.Args)+1)
	for i, source := range append([]string{m.Command}, m.Args...) {
		tmpl, e := parseTemplate(source)
		if e != nil {
			return "", nil, e
		}
		var out bytes.Buffer
		if e = tmpl.Execute(&out, fields); e != nil {
			return "", nil, fmt.Errorf("plugin %s template %d: %w", m.Name, i, e)
		}
		value := out.String()
		if strings.ContainsRune(value, 0) {
			return "", nil, fmt.Errorf("plugin %s template %d contains NUL", m.Name, i)
		}
		rendered = append(rendered, value)
	}
	if strings.TrimSpace(rendered[0]) == "" {
		return "", nil, fmt.Errorf("plugin %s: empty command", m.Name)
	}
	return rendered[0], rendered[1:], nil
}
