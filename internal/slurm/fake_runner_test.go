package slurm

import (
	"context"
	"fmt"
	"strings"
)

// FakeRunner replays recorded command fixtures by executable and argv.
type FakeRunner struct {
	responses map[string][]byte
	stderrs   map[string][]byte
	errors    map[string]error
	calls     []string
	dirs      []string
}

func newFakeRunner() *FakeRunner {
	return &FakeRunner{
		responses: make(map[string][]byte),
		stderrs:   make(map[string][]byte),
		errors:    make(map[string]error),
	}
}

func (r *FakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	stdout, _, err := r.RunIn(ctx, "", name, args...)
	return stdout, err
}

func (r *FakeRunner) RunIn(_ context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	key := commandKey(name, args)
	r.calls = append(r.calls, key)
	r.dirs = append(r.dirs, dir)
	stderr := r.stderrs[key]
	if err := r.errors[key]; err != nil {
		return r.responses[key], stderr, err
	}
	data, ok := r.responses[key]
	if !ok {
		if _, hasStderr := r.stderrs[key]; !hasStderr {
			return nil, nil, fmt.Errorf("no fake response for %s", key)
		}
	}
	return data, stderr, nil
}

func commandKey(name string, args []string) string {
	return name + " " + strings.Join(args, " ")
}
