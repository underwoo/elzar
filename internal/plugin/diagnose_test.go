package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiagnoseAndFormat(t *testing.T) {
	options := discoveryOptions(t)
	options.Disabled = []string{"stderr"}

	// Valid exec plugin in site layer.
	execYAML := "name: metrics\ndescription: Metrics exec plugin\ntype: exec\ncommand: " + testPluginPath + "\nprovides: [columns, detail]\n"
	writeFile(t, filepath.Join(options.SiteDir, "metrics.yaml"), execYAML, 0o600)

	// Valid command plugin in user layer.
	writeFile(t, filepath.Join(options.UserDir, "sample.yaml"), validManifest, 0o600)

	diags, hasErr := Diagnose(context.Background(), options, nil, map[string]bool{"q": true})
	if hasErr {
		t.Fatalf("unexpected error in diagnostics: %+v", diags)
	}
	if len(diags) != 4 {
		t.Fatalf("len(diags)=%d, want 4: %+v", len(diags), diags)
	}

	var buf bytes.Buffer
	if err := FormatDiagnostics(&buf, diags); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"NAME", "LAYER", "PATH", "TYPE", "DETAILS", "STATUS",
		"metrics", "site", "exec", "provides=columns,detail", "ok",
		"sample", "user", "command", "key=z scopes=queue,recent", "ok",
		"stderr", "builtin", "builtins/stderr.yaml", "disabled: in [plugins] disabled",
		"stdout", "builtin", "builtins/stdout.yaml", "key=o scopes=queue,recent,detail",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDiagnoseErrorsAndCollisions(t *testing.T) {
	options := discoveryOptions(t)

	// 1. Invalid YAML manifest.
	writeFile(t, filepath.Join(options.UserDir, "broken.yaml"), "name: broken\ntype: nope\n", 0o600)
	// 2. Group-writable manifest.
	writeFile(t, filepath.Join(options.UserDir, "unsafe.yaml"), validManifest, 0o620)
	// 3. Built-in key collision.
	collideBuiltin := strings.Replace(strings.Replace(validManifest, "name: sample", "name: quit-collision", 1), "key: z", "key: q", 1)
	writeFile(t, filepath.Join(options.UserDir, "collide-builtin.yaml"), collideBuiltin, 0o600)
	// 4. Plugin-to-plugin key collision in overlapping scope.
	firstZ := strings.Replace(validManifest, "name: sample", "name: first-z", 1)
	secondZ := strings.Replace(validManifest, "name: sample", "name: second-z", 1)
	writeFile(t, filepath.Join(options.UserDir, "first-z.yaml"), firstZ, 0o600)
	writeFile(t, filepath.Join(options.UserDir, "second-z.yaml"), secondZ, 0o600)
	// 5. Exec plugin whose describe fails.
	badExec := "name: bad-exec\ndescription: Broken exec\ntype: exec\ncommand: " + testPluginPath + "\nargs: [protocol]\nprovides: [columns, detail]\n"
	writeFile(t, filepath.Join(options.UserDir, "bad-exec.yaml"), badExec, 0o600)

	diags, hasErr := Diagnose(context.Background(), options, nil, map[string]bool{"q": true})
	if !hasErr {
		t.Fatalf("expected hasErr=true, got false: %+v", diags)
	}

	byName := map[string]Diagnostic{}
	for _, d := range diags {
		byName[d.Name] = d
	}
	for name, wantSub := range map[string]string{
		"broken":         "error:",
		"unsafe":         "error:",
		"quit-collision": "conflicts with a built-in binding",
		"first-z":        "ok",
		"second-z":       "conflicts with plugin first-z",
		"bad-exec":       "unsupported protocol",
	} {
		got, ok := byName[name]
		if !ok || !strings.Contains(got.Status, wantSub) {
			t.Fatalf("%s diagnostic = %+v, want status containing %q", name, got, wantSub)
		}
	}
}

func TestExamplePluginsPassDiagnoseAndExecute(t *testing.T) {
	options := discoveryOptions(t)
	examplesDir := filepath.Join("..", "..", "examples", "plugins")
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		src := filepath.Join(examplesDir, entry.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if strings.HasSuffix(entry.Name(), ".sh") {
			mode = 0o700
		}
		writeFile(t, filepath.Join(options.UserDir, entry.Name()), string(data), mode)
	}

	// Ensure ssh, scontrol, sh, less, and jq exist on PATH even in minimal test containers.
	binDir := t.TempDir()
	for _, cmd := range []string{"ssh", "scontrol", "less"} {
		if _, err := CheckExecutable(cmd); err != nil {
			writeFile(t, filepath.Join(binDir, cmd), "#!/bin/sh\nexit 0\n", 0o700)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	diags, hasErr := Diagnose(context.Background(), options, nil, map[string]bool{"q": true})
	if hasErr {
		var buf bytes.Buffer
		_ = FormatDiagnostics(&buf, diags)
		t.Fatalf("example plugins failed Diagnose:\n%s", buf.String())
	}

	plugins, errs := Discover(options)
	if len(errs) != 0 {
		t.Fatalf("Discover errors on examples: %v", errs)
	}
	var jobInfo *Plugin
	for i := range plugins {
		if plugins[i].Name == "job-info" {
			jobInfo = &plugins[i]
			break
		}
	}
	if jobInfo == nil {
		t.Fatalf("job-info example plugin not discovered: %+v", plugins)
	}

	session, err := LoadExec(context.Background(), *jobInfo, nil, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	values, err := session.Columns(context.Background(), "queue", []Fields{
		{"JobID": "101", "WorkDir": "/scratch/alice/run"},
		{"JobID": "102"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if values["101"]["workdir"] != "/scratch/alice/run" || values["102"]["workdir"] != "—" {
		t.Fatalf("unexpected columns values from job-info.sh: %+v", values)
	}

	sections, err := session.Detail(context.Background(), Fields{
		"JobID":   "101",
		"WorkDir": "/scratch/alice/run",
		"StdOut":  "/scratch/alice/run/slurm.out",
		"StdErr":  "/scratch/alice/run/slurm.err",
	}, json.RawMessage(`{"job_id":101}`))
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []string{
		"WorkDir: /scratch/alice/run",
		"StdOut:  /scratch/alice/run/slurm.out",
		"StdErr:  /scratch/alice/run/slurm.err",
	}
	if !reflect.DeepEqual(sections["paths"], wantLines) {
		t.Fatalf("detail sections = %+v, want %+v", sections["paths"], wantLines)
	}
}
