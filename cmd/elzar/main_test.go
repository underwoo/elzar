package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.HasPrefix(stdout.String(), "elzar ") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestPluginsSubcommandOkDisabledAndError(t *testing.T) {
	xdg := t.TempDir()
	siteDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("PAGER", "echo")

	configPath := filepath.Join(xdg, "elzar", "config.toml")
	configTOML := "[plugins]\nsite_dir = \"" + siteDir + "\"\ndisabled = [\"stderr\"]\n"
	writeTestFile(t, configPath, configTOML, 0o600)

	userPlugin := filepath.Join(xdg, "elzar", "plugins", "custom.yaml")
	writeTestFile(t, userPlugin, "name: custom\ndescription: Custom command\ntype: command\nkey: z\nscopes: [queue]\ncommand: echo\nargs: ['{{.JobID}}']\nmode: background\n", 0o600)

	var stdout, stderr bytes.Buffer
	code := run([]string{"plugins"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("expected exit 0, got code=%d stderr=%q stdout=\n%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"custom", "user", "command", "key=z scopes=queue", "ok",
		"stderr", "builtin", "disabled: in [plugins] disabled",
		"stdout", "builtin", "ok",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}

	// Add a broken manifest and confirm non-zero exit code (1).
	brokenPlugin := filepath.Join(xdg, "elzar", "plugins", "broken.yaml")
	writeTestFile(t, brokenPlugin, "name: broken\ndescription: Broken\ntype: command\nkey: q\nscopes: [queue]\ncommand: echo\nmode: background\n", 0o600)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"plugins"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 on plugin error, got %d:\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "error: key \"q\" conflicts with a built-in binding") {
		t.Fatalf("missing key conflict error in output:\n%s", stdout.String())
	}
}
