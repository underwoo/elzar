package files

import (
	"fmt"
	"os"
	"syscall"
)

// O_PATH obtains a reference without opening a device or FIFO for I/O.
// Reopening that pinned regular inode through procfs avoids a stat/open race.
func openRegular(path string) (*os.File, os.FileInfo, error) {
	const oPath = 0x200000
	fd, err := syscall.Open(path, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, &os.PathError{Op: "preview", Path: path, Err: err}
	}
	pinned := os.NewFile(uintptr(fd), path)
	defer func() { _ = pinned.Close() }()
	info, err := pinned.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("preview %q: %w", path, ErrNotRegular)
	}
	f, err := os.Open(fmt.Sprintf("/proc/self/fd/%d", fd))
	return f, info, err
}
