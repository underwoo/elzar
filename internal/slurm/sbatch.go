package slurm

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// SubmitResult holds the parsed result of an sbatch submission or dry run.
type SubmitResult struct {
	JobID   int64
	Cluster string
	Message string
	Stderr  string
}

// Submit runs sbatch in dir with args. argv may either include "sbatch" as
// element 0 or hold only the arguments to pass to sbatch. When testOnly is
// true, --test-only is ensured in the arguments and the estimate message on
// stderr (or stdout fallback) is returned without requiring --parsable job ID
// output on stdout.
func Submit(ctx context.Context, runner Runner, dir string, argv []string, testOnly bool) (SubmitResult, error) {
	if runner == nil {
		return SubmitResult{}, fmt.Errorf("slurm runner is unavailable")
	}

	name := "sbatch"
	args := append([]string(nil), argv...)
	if len(args) > 0 && args[0] == "sbatch" {
		args = args[1:]
	}
	if testOnly && !hasArg(args, "--test-only") {
		args = insertFlagBeforeScript(args, "--test-only")
	}

	var stdout, stderr []byte
	var err error
	if dr, ok := runner.(DirRunner); ok {
		stdout, stderr, err = dr.RunIn(ctx, dir, name, args...)
	} else {
		stdout, err = runner.Run(ctx, name, args...)
	}

	stderrText := strings.TrimSpace(string(stderr))
	stdoutText := strings.TrimSpace(string(stdout))

	if err != nil {
		return SubmitResult{Stderr: stderrText}, err
	}

	if testOnly {
		msg := stderrText
		if msg == "" {
			msg = stdoutText
		}
		jobID, cluster, _ := parseTestOnlyEstimate(msg)
		return SubmitResult{
			JobID:   jobID,
			Cluster: cluster,
			Message: msg,
			Stderr:  stderrText,
		}, nil
	}

	jobID, cluster, parseErr := ParseParsableOutput(stdoutText)
	if parseErr != nil {
		return SubmitResult{Stderr: stderrText}, parseErr
	}
	return SubmitResult{
		JobID:   jobID,
		Cluster: cluster,
		Message: stdoutText,
		Stderr:  stderrText,
	}, nil
}

// ParseParsableOutput parses `sbatch --parsable` stdout (`jobid` or `jobid;cluster`).
// Per Slurm 25.11 src/sbatch/sbatch.c:347-352, sbatch prints "%u" followed by
// optional ";%s" and a newline. If job_submit_user_msg was printed beforehand,
// the last non-empty line holds the parsable token.
func ParseParsableOutput(output string) (jobID int64, cluster string, err error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	line := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			line = trimmed
			break
		}
	}
	if line == "" {
		return 0, "", fmt.Errorf("parse sbatch --parsable output: empty output")
	}
	idText, clusterText, hasCluster := strings.Cut(line, ";")
	idText = strings.TrimSpace(idText)
	if idText == "" {
		return 0, "", fmt.Errorf("parse sbatch --parsable output %q: missing job ID", line)
	}
	id, parseErr := strconv.ParseInt(idText, 10, 64)
	if parseErr != nil || id <= 0 {
		return 0, "", fmt.Errorf("parse sbatch --parsable output %q: invalid job ID", line)
	}
	if hasCluster {
		if strings.Contains(clusterText, ";") {
			return 0, "", fmt.Errorf("parse sbatch --parsable output %q: unexpected extra semicolon", line)
		}
		cluster = strings.TrimSpace(clusterText)
	}
	return id, cluster, nil
}

// parseTestOnlyEstimate extracts the job ID and optional cluster from sbatch's
// --test-only stderr line (Slurm 25.11 src/api/allocate.c:641:
// `sbatch: Job <id> to start at <time>[ on cluster <cluster>] using ...`).
func parseTestOnlyEstimate(text string) (int64, string, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		_, after, ok := strings.Cut(line, "Job ")
		if !ok {
			continue
		}
		idField, rest, ok := strings.Cut(after, " ")
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(idField, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		cluster := ""
		if _, afterCluster, hasCluster := strings.Cut(rest, "on cluster "); hasCluster {
			cluster, _, _ = strings.Cut(afterCluster, " ")
		}
		return id, cluster, true
	}
	return 0, "", false
}

func hasArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == target {
			return true
		}
	}
	return false
}

func insertFlagBeforeScript(args []string, flag string) []string {
	out := make([]string, 0, len(args)+1)
	out = append(out, flag)
	out = append(out, args...)
	return out
}
