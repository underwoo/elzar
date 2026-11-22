package files

import (
	"context"
	"io"
	"path/filepath"
)

const ScanLimit = 500
const ScanBytes = 8192

type ScriptResult struct {
	Name     string
	IsScript bool
	Err      error
}

// ScanScripts streams bounded hints. Cancel the context when leaving a directory
// or abandoning the stream. Cancellation cannot interrupt a stalled filesystem
// syscall, but is checked before each file and before delivering each result.
func ScanScripts(ctx context.Context, dir string, names []string) <-chan ScriptResult {
	out := make(chan ScriptResult)
	names = append([]string(nil), names...)
	go func() {
		defer close(out)
		for _, name := range names[:min(len(names), ScanLimit)] {
			if ctx.Err() != nil {
				return
			}
			if !filepath.IsLocal(name) || filepath.Base(name) != name {
				continue
			}
			f, _, err := openRegular(filepath.Join(dir, name))
			r := ScriptResult{Name: name, Err: err}
			if err == nil {
				data, readErr := io.ReadAll(io.LimitReader(f, ScanBytes))
				_ = f.Close()
				r.Err = readErr
				r.IsScript = readErr == nil && script(data)
			}
			select {
			case <-ctx.Done():
				return
			case out <- r:
			}
		}
	}()
	return out
}
