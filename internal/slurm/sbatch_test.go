package slurm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseParsableOutput(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantID      int64
		wantCluster string
		wantErr     bool
	}{
		{"jobid only", "65541\n", 65541, "", false},
		{"jobid with cluster", "65541;gaea\n", 65541, "gaea", false},
		{"leading submit message", "Notice from job_submit\n65541;es\n", 65541, "es", false},
		{"empty", "", 0, "", true},
		{"whitespace only", "  \n\t\n", 0, "", true},
		{"non-numeric", "Submitted batch job 65541\n", 0, "", true},
		{"negative id", "-12\n", 0, "", true},
		{"zero id", "0\n", 0, "", true},
		{"extra semicolon", "65541;es;extra\n", 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, cluster, err := ParseParsableOutput(tt.output)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseParsableOutput(%q) succeeded: id=%d cluster=%q", tt.output, id, cluster)
				}
				return
			}
			if err != nil || id != tt.wantID || cluster != tt.wantCluster {
				t.Fatalf("ParseParsableOutput(%q) = (%d, %q, %v), want (%d, %q, nil)", tt.output, id, cluster, err, tt.wantID, tt.wantCluster)
			}
		})
	}
}

func TestSubmitParsableAndTestOnlyAndErrors(t *testing.T) {
	t.Run("submit without cluster", func(t *testing.T) {
		runner := newFakeRunner()
		key := "sbatch --parsable /work/job.sh"
		runner.responses[key] = []byte("65541\n")

		res, err := Submit(context.Background(), runner, "/work", []string{"sbatch", "--parsable", "/work/job.sh"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.JobID != 65541 || res.Cluster != "" {
			t.Fatalf("result = %+v", res)
		}
		if len(runner.dirs) != 1 || runner.dirs[0] != "/work" {
			t.Fatalf("dirs = %v, want [/work]", runner.dirs)
		}
	})

	t.Run("submit with cluster", func(t *testing.T) {
		runner := newFakeRunner()
		key := "sbatch --parsable --account=ml /work/job.sh arg1"
		runner.responses[key] = []byte("77889;c5\n")

		res, err := Submit(context.Background(), runner, "/work", []string{"--parsable", "--account=ml", "/work/job.sh", "arg1"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.JobID != 77889 || res.Cluster != "c5" {
			t.Fatalf("result = %+v", res)
		}
	})

	t.Run("test-only reads stderr on exit 0", func(t *testing.T) {
		runner := newFakeRunner()
		key := "sbatch --parsable --test-only /work/job.sh"
		stderrLine := "sbatch: Job 65542 to start at 2026-11-23T01:00:00 using 128 processors on nodes nid00[10-11] in partition batch\n"
		runner.stderrs[key] = []byte(stderrLine)

		res, err := Submit(context.Background(), runner, "/work", []string{"sbatch", "--parsable", "--test-only", "/work/job.sh"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if res.JobID != 65542 || res.Cluster != "" || res.Message != strings.TrimSpace(stderrLine) {
			t.Fatalf("test-only result = %+v", res)
		}
	})

	t.Run("test-only with cluster on stderr", func(t *testing.T) {
		runner := newFakeRunner()
		key := "sbatch --test-only --parsable /work/job.sh"
		stderrLine := "sbatch: Job 65543 to start at 2026-11-23T01:00:00 on cluster c6 a using 64 processors on nodes c6n01 in partition batch\n"
		runner.stderrs[key] = []byte(stderrLine)

		res, err := Submit(context.Background(), runner, "/work", []string{"--parsable", "/work/job.sh"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if res.JobID != 65543 || res.Cluster != "c6" || res.Message != strings.TrimSpace(stderrLine) {
			t.Fatalf("test-only cluster result = %+v", res)
		}
	})

	t.Run("sbatch error surfaced verbatim", func(t *testing.T) {
		runner := newFakeRunner()
		key := "sbatch --parsable /work/job.sh"
		wantErr := errors.New("sbatch: error: Batch job submission failed: Invalid account or account/partition combination specified")
		runner.errors[key] = wantErr
		runner.stderrs[key] = []byte("sbatch: error: Batch job submission failed: Invalid account or account/partition combination specified\n")

		res, err := Submit(context.Background(), runner, "/work", []string{"--parsable", "/work/job.sh"}, false)
		if err == nil || !strings.Contains(err.Error(), "Invalid account or account/partition combination specified") {
			t.Fatalf("err = %v, want verbatim sbatch error", err)
		}
		if !strings.Contains(res.Stderr, "Invalid account or account/partition combination specified") {
			t.Fatalf("stderr = %q", res.Stderr)
		}
	})
}

func TestExecRunnerRunInWorkingDirectoryAndStderr(t *testing.T) {
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	runner := ExecRunner{}
	stdout, stderr, err := runner.RunIn(context.Background(), realDir, "sh", "-c", "pwd && echo 'sbatch: Job 901 to start at 2026-11-23T00:00:00 using 1 processors on nodes n1 in partition p1' >&2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdout)) != realDir {
		t.Fatalf("RunIn working directory = %q, want %q", strings.TrimSpace(string(stdout)), realDir)
	}
	if !strings.Contains(string(stderr), "sbatch: Job 901 to start at") {
		t.Fatalf("RunIn stderr = %q", string(stderr))
	}

	// Run drops stderr on success.
	runOut, err := runner.Run(context.Background(), "sh", "-c", "echo out-only && echo err-dropped >&2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(runOut)) != "out-only" {
		t.Fatalf("Run output = %q, want out-only", string(runOut))
	}

	// Real Submit with a fake sbatch executable in PATH verifying working directory and stderr capture.
	binDir := t.TempDir()
	sbatchScript := filepath.Join(binDir, "sbatch")
	scriptBody := `#!/bin/sh
for arg in "$@"; do
	if [ "$arg" = "--test-only" ]; then
		echo "sbatch: Job 4242 to start at 2026-11-23T02:00:00 using 4 processors on nodes n01 in partition debug (cwd=$(pwd))" >&2
		exit 0
	fi
done
echo "4242;es"
`
	if err := os.WriteFile(sbatchScript, []byte(scriptBody), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dryRes, err := Submit(context.Background(), runner, realDir, []string{"sbatch", "--parsable", "--test-only", filepath.Join(realDir, "job.sh")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if dryRes.JobID != 4242 || !strings.Contains(dryRes.Message, "cwd="+realDir) {
		t.Fatalf("dry run result = %+v", dryRes)
	}

	subRes, err := Submit(context.Background(), runner, realDir, []string{"sbatch", "--parsable", filepath.Join(realDir, "job.sh")}, false)
	if err != nil {
		t.Fatal(err)
	}
	if subRes.JobID != 4242 || subRes.Cluster != "es" {
		t.Fatalf("submit result = %+v", subRes)
	}
}
