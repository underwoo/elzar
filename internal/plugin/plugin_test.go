package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const validManifest = `name: sample
description: Sample command
type: command
key: z
scopes: [queue, recent]
command: echo
args: ['{{.Name}}']
mode: background
`

func TestParseValidation(t *testing.T) {
	tests := []struct{ name, text, want string }{
		{"valid", validManifest, ""},
		{"empty", "", "decode"},
		{"unknown", validManifest + "typo: true\n", "field typo"},
		{"duplicate yaml key", validManifest + "name: another\n", "already defined"},
		{"multiple documents", validManifest + "---\nname: other\n", "one YAML document"},
		{"type", strings.Replace(validManifest, "type: command", "type: nope", 1), "unknown plugin type"},
		{"scope", strings.Replace(validManifest, "[queue, recent]", "[jobs]", 1), "unknown scope"},
		{"duplicate scope", strings.Replace(validManifest, "[queue, recent]", "[queue, queue]", 1), "duplicate scope"},
		{"empty scopes", strings.Replace(validManifest, "[queue, recent]", "[]", 1), "scopes is required"},
		{"mode", strings.Replace(validManifest, "background", "shell", 1), "unknown mode"},
		{"key chord", strings.Replace(validManifest, "key: z", "key: ctrl+t z", 1), "invalid key"},
		{"key modifier", strings.Replace(validManifest, "key: z", "key: super+x", 1), "invalid key"},
		{"key multiple", strings.Replace(validManifest, "key: z", "key: zz", 1), "invalid key"},
		{"bad template", strings.Replace(validManifest, "{{.Name}}", "{{", 1), "template"},
		{"unknown function", strings.Replace(validManifest, "{{.Name}}", "{{unknown}}", 1), "template"},
		{"bad args", strings.Replace(validManifest, "args: ['{{.Name}}']", "args: hello", 1), "unmarshal"},
		{"bad confirm", validManifest + "confirm: not-bool\n", "unmarshal"},
	}
	for _, field := range []string{"name: sample", "description: Sample command", "type: command", "key: z", "command: echo", "mode: background"} {
		tests = append(tests, struct{ name, text, want string }{field, strings.Replace(validManifest, field, "", 1), "required"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.text))
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want %q", err, tt.want)
			}
		})
	}
	for _, key := range []string{"o", "O", "?", "ctrl+x", "alt+x", "é"} {
		if !validKey(key) {
			t.Errorf("key %q rejected", key)
		}
	}
}

func TestRender(t *testing.T) {
	t.Setenv("PLUGIN_TEST_COMMAND", "echo")
	m, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	m.Command = `{{env "PLUGIN_TEST_COMMAND" "fallback"}}`
	m.Args = []string{`{{.Name}}`, `{{shquote .Name}}`, `{{default "fallback" .Empty}}`, `{{env "PLUGIN_TEST_UNSET" "value"}}`}
	t.Setenv("PLUGIN_TEST_UNSET", "")
	name, args, err := Render(m, Fields{"Name": "a b'c\"d", "Empty": ""})
	want := []string{"a b'c\"d", "'a b'\"'\"'c\"d'", "fallback", "value"}
	if err != nil || name != "echo" || !reflect.DeepEqual(args, want) {
		t.Fatalf("Render=%q %#v %v", name, args, err)
	}
	for _, source := range []string{"{{.Absent}}", `{{default "fallback" .Absent}}`} {
		m.Args = []string{source}
		if _, _, err := Render(m, Fields{}); err == nil {
			t.Fatalf("missing field accepted: %s", source)
		}
	}
	m.Args = []string{"{{.Name}}"}
	if _, _, err := Render(m, Fields{"Name": "bad\x00arg"}); err == nil {
		t.Fatal("NUL accepted")
	}
	m.Command = "{{.Empty}}"
	if _, _, err := Render(m, Fields{"Empty": "", "Name": "ok"}); err == nil {
		t.Fatal("empty command accepted")
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func discoveryOptions(t *testing.T) Options {
	t.Helper()
	options := DefaultOptions()
	options.SiteDir = t.TempDir()
	options.UserDir = t.TempDir()
	t.Setenv("PAGER", "echo")
	return options
}

func TestBuiltins(t *testing.T) {
	options := discoveryOptions(t)
	plugins, errs := Discover(options)
	if len(errs) != 0 || len(plugins) != 2 {
		t.Fatalf("plugins=%+v errors=%v", plugins, errs)
	}
	for _, p := range plugins {
		if p.Layer != "builtin" || p.Mode != "foreground" || len(p.Scopes) != 3 {
			t.Fatalf("builtin=%+v", p)
		}
		_, argv, err := Render(p.Manifest, Fields{"StdOut": "/tmp/a b", "StdErr": "/tmp/c d"})
		if err != nil || len(argv) != 2 || argv[0] != "--" {
			t.Fatalf("argv=%v err=%v", argv, err)
		}
		if (p.Name == "stdout" && p.Key != "o") || (p.Name == "stderr" && p.Key != "O") {
			t.Fatalf("key=%q", p.Key)
		}
	}
	options.Enabled = false
	if plugins, errs := Discover(options); len(plugins) != 0 || len(errs) != 0 {
		t.Fatalf("disabled: %v %v", plugins, errs)
	}
}

func TestDiscoveryLayers(t *testing.T) {
	options := discoveryOptions(t)
	options.Disabled = []string{"stderr"}
	site := strings.Replace(validManifest, "name: sample", "name: stdout", 1)
	user := strings.Replace(site, "Sample command", "User override", 1)
	writeFile(t, filepath.Join(options.SiteDir, "a.yaml"), site, 0o600)
	writeFile(t, filepath.Join(options.UserDir, "b.yml"), user, 0o600)
	writeFile(t, filepath.Join(options.UserDir, "ignored.txt"), "bad", 0o600)
	plugins, errs := Discover(options)
	if len(errs) != 0 || len(plugins) != 1 || plugins[0].Description != "User override" || plugins[0].Layer != "user" {
		t.Fatalf("%+v %v", plugins, errs)
	}
	// All duplicates in one layer are rejected, not selected by filename.
	writeFile(t, filepath.Join(options.UserDir, "c.yaml"), user, 0o600)
	writeFile(t, filepath.Join(options.UserDir, "d.yaml"), user, 0o600)
	plugins, errs = Discover(options)
	if len(errs) != 2 || len(plugins) != 1 || plugins[0].Layer != "site" {
		t.Fatalf("%+v %v", plugins, errs)
	}
	options.Disabled = append(options.Disabled, "stdout")
	plugins, _ = Discover(options)
	if len(plugins) != 0 {
		t.Fatalf("disabled override remains: %+v", plugins)
	}
}

func TestDiscoveryIsolationAndPermissions(t *testing.T) {
	options := discoveryOptions(t)
	writeFile(t, filepath.Join(options.UserDir, "good.yaml"), validManifest, 0o600)
	writeFile(t, filepath.Join(options.UserDir, "broken.yaml"), "unknown: [", 0o600)
	for _, mode := range []os.FileMode{0o620, 0o602} {
		writeFile(t, filepath.Join(options.UserDir, "unsafe.yaml"), strings.Replace(validManifest, "sample", "unsafe", 1), mode)
		plugins, errs := Discover(options)
		if len(plugins) != 3 || len(errs) != 2 {
			t.Fatalf("mode %o: %+v %v", mode, plugins, errs)
		}
	}
	target := filepath.Join(t.TempDir(), "target.yaml")
	writeFile(t, target, validManifest, 0o666)
	if err := os.Symlink(target, filepath.Join(options.UserDir, "link.yaml")); err != nil {
		t.Fatal(err)
	}
	_, errs := Discover(options)
	if len(errs) != 3 {
		t.Fatalf("symlink permissions not checked: %v", errs)
	}
}

func TestExecutablePermissions(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "tool")
	writeFile(t, command, "#!/bin/sh\nexit 0\n", 0o700)
	if _, err := CheckExecutable(command); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o720, 0o702} {
		if err := os.Chmod(command, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckExecutable(command); err == nil {
			t.Fatalf("unsafe mode %o accepted", mode)
		}
	}
	options := discoveryOptions(t)
	manifest := strings.Replace(validManifest, "command: echo", "command: "+command, 1)
	writeFile(t, filepath.Join(options.UserDir, "unsafe.yaml"), manifest, 0o600)
	plugins, errs := Discover(options)
	if len(plugins) != 2 || len(errs) != 1 {
		t.Fatalf("unsafe executable: %+v %v", plugins, errs)
	}
	if _, err := CheckExecutable(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing executable accepted")
	}
}

func TestDiscoveryXDGAndMissingDirectories(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("PAGER", "echo")
	dir := filepath.Join(base, "elzar", "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "test.yml"), validManifest, 0o600)
	options := DefaultOptions()
	options.SiteDir = filepath.Join(base, "missing")
	plugins, errs := Discover(options)
	if len(plugins) != 3 || len(errs) != 0 {
		t.Fatalf("%+v %v", plugins, errs)
	}
	options.SiteDir = filepath.Join(dir, "test.yml")
	_, errs = Discover(options)
	if len(errs) != 1 {
		t.Fatalf("directory error=%v", errs)
	}
}

// Exercise the argv contract with a real process: shell metacharacters stay data.
func TestRenderedArgvExecution(t *testing.T) {
	m, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	m.Command = "printf"
	m.Args = []string{"%s", "{{.Name}}"}
	value := "a b'c; $(echo expanded)"
	name, args, err := Render(m, Fields{"Name": value})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := CheckExecutable(name)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable, args...).Output()
	if err != nil || string(output) != value {
		t.Fatalf("output=%q error=%v", output, err)
	}
}

func TestDynamicExecutable(t *testing.T) {
	options := discoveryOptions(t)
	text := strings.Replace(validManifest, "command: echo", "command: '{{.Executable}}'", 1)
	writeFile(t, filepath.Join(options.UserDir, "dynamic.yaml"), text, 0o600)
	plugins, errs := Discover(options)
	if len(plugins) != 3 || len(errs) != 0 {
		t.Fatalf("plugins=%+v errors=%v", plugins, errs)
	}
	m, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "unsafe")
	writeFile(t, executable, "#!/bin/sh\nexit 0\n", 0o702)
	name, _, err := Render(m, Fields{"Executable": executable, "Name": "job"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CheckExecutable(name); err == nil {
		t.Fatal("rendered unsafe executable accepted")
	}
}
