package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const MaxOutputBytes = 1 << 20

var ErrOutputLimit = errors.New("plugin stdout exceeds 1 MiB")

// Executor is the process boundary. Implementations must honor ctx cancellation.
// argv includes the executable as element zero; stdin is one JSON request.
type Executor interface {
	Exec(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, err error)
}

// ProcessExecutor runs each operation in a new process, with no implicit shell.
// Sessions supply the configured timeout; direct callers may supply a deadline.
type ProcessExecutor struct{}

func (ProcessExecutor) Exec(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, error) {
	if len(argv) == 0 {
		return nil, nil, fmt.Errorf("empty plugin argv")
	}
	name, err := CheckExecutable(argv[0])
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	// Bound waiting on inherited pipes when an executable leaves children behind.
	cmd.WaitDelay = 100 * time.Millisecond
	stdout := &boundedOutput{limit: MaxOutputBytes, cancel: cancel}
	stderr := &boundedOutput{limit: MaxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if stdout.exceeded {
		err = ErrOutputLimit
	} else if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stdout.buffer.Bytes(), stderr.buffer.Bytes(), err
}

// stderr is truncated but drained; stdout overflow cancels the process. Each
// instance has one writer (os/exec's copier) and is read only after Run returns.
type boundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (w *boundedOutput) Write(data []byte) (int, error) {
	length := len(data)
	remaining := w.limit - w.buffer.Len()
	if length > remaining {
		w.exceeded = true
		_, _ = w.buffer.Write(data[:remaining])
		if w.cancel != nil {
			w.cancel()
			return remaining, ErrOutputLimit
		}
		return length, nil
	}
	return w.buffer.Write(data)
}
