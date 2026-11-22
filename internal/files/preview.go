// Package files provides bounded, read-only filesystem browsing.
package files

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const PreviewLimit = 64 * 1024

var ErrNotRegular = errors.New("not a regular file")

type FilePreview struct {
	Text      string
	Size      int64
	ModTime   time.Time
	Binary    bool
	Truncated bool
	IsScript  bool
}

// Preview reads at most PreviewLimit bytes. Metadata comes from the same
// descriptor as the content, so a renamed path cannot mix two files' data.
func Preview(path string) (FilePreview, error) {
	f, info, err := openRegular(path)
	if err != nil {
		return FilePreview{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, PreviewLimit))
	if err != nil {
		return FilePreview{}, fmt.Errorf("preview %q: %w", path, err)
	}
	p := FilePreview{Size: info.Size(), ModTime: info.ModTime(), Truncated: info.Size() > int64(len(data))}
	p.Binary = bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0
	if p.Binary {
		p.Text = fmt.Sprintf("binary file, %d bytes", p.Size)
	} else {
		p.Text = string(data)
		p.IsScript = script(data)
	}
	return p, nil
}

// IsScript applies the same bounded regular-file read as Preview.
func IsScript(path string) (bool, error) {
	p, err := Preview(path)
	return p.IsScript, err
}

func script(data []byte) bool {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "#!") {
		return false
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#SBATCH") && (len(line) == 7 || line[7] == ' ' || line[7] == '\t') {
			return true
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			return false
		}
	}
	return false
}
