package slurm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const DefaultCommandTimeout = 30 * time.Second

// Runner is the boundary for all external Slurm commands.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// DirRunner is an optional Runner extension that runs a command in dir (when
// non-empty) and returns both stdout and stderr. Commands such as
// `sbatch --test-only` write normal informational output to stderr on exit 0.
type DirRunner interface {
	RunIn(ctx context.Context, dir, name string, args ...string) (stdout, stderr []byte, err error)
}

// ExecRunner runs a command with a finite timeout and captures stderr for
// useful command failures.
type ExecRunner struct {
	Timeout time.Duration
}

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	stdout, _, err := r.RunIn(ctx, "", name, args...)
	return stdout, err
}

func (r ExecRunner) RunIn(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	stderrBytes := stderr.Bytes()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if ctx.Err() != nil {
			return output, stderrBytes, fmt.Errorf("%s timed out after %s: %w", name, timeout, ctx.Err())
		}
		if message != "" {
			return output, stderrBytes, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, message)
		}
		return output, stderrBytes, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return output, stderrBytes, nil
}
