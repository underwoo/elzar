package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
)

// Diagnostic reports a discovered plugin's origin, metadata, and validation status.
type Diagnostic struct {
	Name     string
	Layer    string
	Path     string
	Type     string
	Key      string
	Scopes   []string
	Provides []string
	Status   string
}

func (d Diagnostic) IsError() bool {
	return strings.HasPrefix(d.Status, "error:")
}

// Details formats the plugin's key and scopes (for command plugins) or provides
// list (for exec plugins).
func (d Diagnostic) Details() string {
	switch d.Type {
	case "command":
		switch {
		case d.Key != "" && len(d.Scopes) > 0:
			return fmt.Sprintf("key=%s scopes=%s", d.Key, strings.Join(d.Scopes, ","))
		case d.Key != "":
			return "key=" + d.Key
		case len(d.Scopes) > 0:
			return "scopes=" + strings.Join(d.Scopes, ",")
		}
	case "exec":
		if len(d.Provides) > 0 {
			return "provides=" + strings.Join(d.Provides, ",")
		}
	}
	return "-"
}

// FormatDiagnostics renders a tabular report of plugin diagnostics.
func FormatDiagnostics(w io.Writer, diags []Diagnostic) error {
	if len(diags) == 0 {
		_, err := fmt.Fprintln(w, "no plugins discovered")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tLAYER\tPATH\tTYPE\tDETAILS\tSTATUS")
	for _, d := range diags {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", d.Name, d.Layer, d.Path, d.Type, d.Details(), d.Status)
	}
	return tw.Flush()
}

// Diagnose inspects built-in, site, and user plugin layers and checks each
// selected plugin (including running describe on exec plugins and checking key
// collisions when reservedKeys is provided). It returns one Diagnostic per
// plugin entry or layer failure and true if any entry has an error status.
func Diagnose(ctx context.Context, options Options, executor Executor, reservedKeys map[string]bool) ([]Diagnostic, bool) {
	if !options.Enabled {
		return nil, false
	}

	userDir := options.UserDir
	if userDir == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			var err error
			base, err = os.UserConfigDir()
			if err != nil {
				return []Diagnostic{{
					Name:   "-",
					Layer:  "user",
					Path:   "-",
					Type:   "-",
					Status: "error: user plugins directory: " + err.Error(),
				}}, true
			}
		}
		if base != "" {
			userDir = filepath.Join(base, "elzar", "plugins")
		}
	}

	layers := []struct {
		name, dir string
		files     fs.FS
	}{
		{"builtin", "builtins", builtins},
		{"site", options.SiteDir, nil},
		{"user", userDir, nil},
	}

	var errorDiags []Diagnostic
	selected := map[string]Plugin{}

	for _, layer := range layers {
		if layer.dir == "" {
			continue
		}
		var entries []fs.DirEntry
		var err error
		if layer.files != nil {
			entries, err = fs.ReadDir(layer.files, layer.dir)
		} else {
			entries, err = os.ReadDir(layer.dir)
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errorDiags = append(errorDiags, Diagnostic{
				Name:   "-",
				Layer:  layer.name,
				Path:   layer.dir,
				Type:   "-",
				Status: "error: " + err.Error(),
			})
			continue
		}

		current := map[string]Plugin{}
		firstByName := map[string]Plugin{}
		dupReported := map[string]bool{}

		for _, entry := range entries {
			ext := filepath.Ext(entry.Name())
			if entry.IsDir() || (ext != ".yaml" && ext != ".yml") {
				continue
			}
			path := filepath.Join(layer.dir, entry.Name())
			defaultName := strings.TrimSuffix(entry.Name(), ext)
			var data []byte
			if layer.files != nil {
				data, err = fs.ReadFile(layer.files, path)
			} else {
				err = checkFile(path)
				if err == nil {
					data, err = os.ReadFile(path)
				}
			}
			if err != nil {
				errorDiags = append(errorDiags, Diagnostic{
					Name:   defaultName,
					Layer:  layer.name,
					Path:   path,
					Type:   "-",
					Status: "error: " + err.Error(),
				})
				continue
			}
			m, parseErr := Parse(data)
			if parseErr != nil {
				name := strings.TrimSpace(m.Name)
				if name == "" {
					name = defaultName
				}
				typ := strings.TrimSpace(m.Type)
				if typ == "" {
					typ = "-"
				}
				errorDiags = append(errorDiags, Diagnostic{
					Name:     name,
					Layer:    layer.name,
					Path:     path,
					Type:     typ,
					Key:      m.Key,
					Scopes:   slices.Clone(m.Scopes),
					Provides: slices.Clone(m.Provides),
					Status:   "error: " + parseErr.Error(),
				})
				continue
			}
			if layer.files == nil && !filepath.IsAbs(m.Command) && (strings.ContainsRune(m.Command, '/') || (filepath.Separator != '/' && strings.ContainsRune(m.Command, filepath.Separator))) {
				m.Command = filepath.Join(layer.dir, m.Command)
			}
			p := Plugin{Manifest: m, Layer: layer.name, Path: path}
			if first, seen := firstByName[m.Name]; seen {
				delete(current, m.Name)
				if !dupReported[m.Name] {
					dupReported[m.Name] = true
					errorDiags = append(errorDiags, diagnosticFromPlugin(first, fmt.Sprintf("error: duplicate name %q in %s layer", m.Name, layer.name)))
				}
				errorDiags = append(errorDiags, diagnosticFromPlugin(p, fmt.Sprintf("error: duplicate name %q in %s layer", m.Name, layer.name)))
				continue
			}
			firstByName[m.Name] = p
			current[m.Name] = p
		}
		for name, p := range current {
			selected[name] = p
		}
	}

	disabledSet := make(map[string]bool, len(options.Disabled))
	for _, name := range options.Disabled {
		disabledSet[name] = true
	}

	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)

	var diags []Diagnostic
	var activeCommands []Plugin
	for _, name := range names {
		p := selected[name]
		if disabledSet[name] {
			diags = append(diags, diagnosticFromPlugin(p, "disabled: in [plugins] disabled"))
			continue
		}
		if err := checkStaticCommand(p); err != nil {
			diags = append(diags, diagnosticFromPlugin(p, "error: "+err.Error()))
			continue
		}
		if p.Type == "exec" {
			if _, err := LoadExec(ctx, p, executor, options, nil); err != nil {
				diags = append(diags, diagnosticFromPlugin(p, "error: "+err.Error()))
				continue
			}
			diags = append(diags, diagnosticFromPlugin(p, "ok"))
			continue
		}
		if reservedKeys[p.Key] {
			diags = append(diags, diagnosticFromPlugin(p, fmt.Sprintf("error: key %q conflicts with a built-in binding", p.Key)))
			continue
		}
		conflict := ""
		for _, other := range activeCommands {
			if other.Key != p.Key {
				continue
			}
			for _, scope := range p.Scopes {
				if slices.Contains(other.Scopes, scope) {
					conflict = other.Name
					break
				}
			}
			if conflict != "" {
				break
			}
		}
		if conflict != "" {
			diags = append(diags, diagnosticFromPlugin(p, fmt.Sprintf("error: key %q conflicts with plugin %s", p.Key, conflict)))
			continue
		}
		activeCommands = append(activeCommands, p)
		diags = append(diags, diagnosticFromPlugin(p, "ok"))
	}

	diags = append(diags, errorDiags...)
	hasErrors := false
	for _, d := range diags {
		if d.IsError() {
			hasErrors = true
			break
		}
	}
	return diags, hasErrors
}

func diagnosticFromPlugin(p Plugin, status string) Diagnostic {
	return Diagnostic{
		Name:     p.Name,
		Layer:    p.Layer,
		Path:     p.Path,
		Type:     p.Type,
		Key:      p.Key,
		Scopes:   slices.Clone(p.Scopes),
		Provides: slices.Clone(p.Provides),
		Status:   status,
	}
}

func checkStaticCommand(p Plugin) error {
	tmpl, err := parseTemplate(p.Command)
	if err != nil {
		return err
	}
	var command strings.Builder
	if err = tmpl.Execute(&command, Fields{}); err != nil {
		// Field-dependent executable templates cannot be checked without a row.
		return nil
	}
	_, err = CheckExecutable(command.String())
	return err
}
