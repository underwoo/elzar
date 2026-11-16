package plugin

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed builtins/*.yaml
var builtins embed.FS

type Options struct {
	Enabled  bool
	UserDir  string
	SiteDir  string
	Disabled []string
	Timeout  time.Duration
}

func DefaultOptions() Options {
	return Options{Enabled: true, SiteDir: "/etc/elzar/plugins", Timeout: 5 * time.Second}
}

// Plugin retains origin information for diagnostics.
type Plugin struct {
	Manifest
	Layer string
	Path  string
}

// Discover loads built-in, site, then user manifests. Higher layers replace by
// name. Duplicate names invalidate that name within their entire layer.
// Missing directories are normal. Errors affect only the offending entries.
func Discover(options Options) ([]Plugin, []error) {
	if !options.Enabled {
		return nil, nil
	}
	var errs []error
	userDir := options.UserDir
	if userDir == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			var err error
			base, err = os.UserConfigDir()
			if err != nil {
				errs = append(errs, fmt.Errorf("user plugins directory: %w", err))
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
			errs = append(errs, fmt.Errorf("%s plugins: %w", layer.name, err))
			continue
		}
		current := map[string]Plugin{}
		seen := map[string]bool{}
		for _, entry := range entries {
			ext := filepath.Ext(entry.Name())
			if entry.IsDir() || (ext != ".yaml" && ext != ".yml") {
				continue
			}
			path := filepath.Join(layer.dir, entry.Name())
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
				errs = append(errs, fmt.Errorf("%s: %w", path, err))
				continue
			}
			m, parseErr := Parse(data)
			if parseErr != nil {
				errs = append(errs, fmt.Errorf("%s: %w", path, parseErr))
				continue
			}
			if seen[m.Name] {
				delete(current, m.Name)
				errs = append(errs, fmt.Errorf("%s: duplicate name %q in %s layer", path, m.Name, layer.name))
				continue
			}
			seen[m.Name] = true
			current[m.Name] = Plugin{Manifest: m, Layer: layer.name, Path: path}
		}
		for name, p := range current {
			selected[name] = p
		}
	}
	for _, name := range options.Disabled {
		delete(selected, name)
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	plugins := make([]Plugin, 0, len(names))
	for _, name := range names {
		p := selected[name]
		// Field-dependent executable templates are checked by the caller after
		// Render. Static and environment-only commands can be checked at discovery.
		tmpl, err := parseTemplate(p.Command)
		if err == nil {
			var command strings.Builder
			if err = tmpl.Execute(&command, Fields{}); err == nil {
				if _, err = CheckExecutable(command.String()); err != nil {
					errs = append(errs, fmt.Errorf("plugin %s: %w", name, err))
					continue
				}
			}
		}
		plugins = append(plugins, p)
	}
	return plugins, errs
}

// CheckExecutable resolves PATH and follows symlinks, rejecting executables
// writable by group or others. Call it again immediately before execution:
// discovery cannot validate a command that depends on the selected row.
func CheckExecutable(command string) (string, error) {
	path, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("find executable %q: %w", command, err)
	}
	if err = checkFile(path); err != nil {
		return "", err
	}
	return path, nil
}

func checkFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by group or others", path)
	}
	return nil
}
