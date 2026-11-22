package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func put(t *testing.T, dir, name, data string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestList(t *testing.T) {
	d := t.TempDir()
	put(t, d, "a", "x")
	put(t, d, ".hidden", "")
	if err := os.Mkdir(filepath.Join(d, "z"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(d, filepath.Join(d, "loop")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(d, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := List(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 4 || l.Entries[0].Name != "z" || l.Entries[1].Name != "a" || l.Entries[2].Kind != Other || l.Entries[3].Target != d {
		t.Fatalf("%+v", l)
	}
	l, err = List(filepath.Join(d, "loop"), true)
	if err != nil || len(l.Entries) != 5 {
		t.Fatalf("%+v %v", l, err)
	}
	// No content read: an unreadable file and FIFO still list successfully.
	if err := os.Chmod(filepath.Join(d, "a"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := List(d, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < EntryLimit; i++ {
		put(t, d, fmt.Sprintf("entry%04d", i), "")
	}
	l, err = List(d, false)
	if err != nil || len(l.Entries) != EntryLimit || l.Marker() != "4 more not shown" {
		t.Fatalf("%d %s %v", len(l.Entries), l.Marker(), err)
	}
	if _, err := List(filepath.Join(d, "missing"), false); err == nil {
		t.Fatal("missing directory accepted")
	}
}
func TestPreview(t *testing.T) {
	d := t.TempDir()
	for _, tc := range []struct {
		data string
		want bool
	}{{"#!/bin/sh\n#SBATCH -J test\necho hi\n", true}, {"#!/bin/sh\n# comment\n\n#SBATCH --time=1\n", true}, {"#SBATCH -J x\n", false}, {"#!/bin/sh\necho hi\n#SBATCH -J x\n", false}, {"#!/bin/sh\n#SBATCHish\n", false}} {
		p := put(t, d, "job", tc.data)
		got, err := IsScript(p)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %v %v", tc.data, got, err)
		}
	}
	p := put(t, d, "big", strings.Repeat("a", PreviewLimit+10))
	before, _ := os.Stat(p)
	v, err := Preview(p)
	if err != nil || len(v.Text) != PreviewLimit || !v.Truncated || v.Size != PreviewLimit+10 || !v.ModTime.Equal(before.ModTime()) {
		t.Fatalf("preview %v", err)
	}
	p = put(t, d, "binary", "a\x00b")
	v, err = Preview(p)
	if err != nil || !v.Binary || v.Text != "binary file, 3 bytes" {
		t.Fatalf("%+v %v", v, err)
	}
	p = put(t, d, "late-nul", strings.Repeat("a", 8192)+"\x00")
	v, err = Preview(p)
	if err != nil || v.Binary {
		t.Fatal("NUL beyond detection window")
	}
	fifo := filepath.Join(d, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{fifo, d} {
		if _, err := Preview(p); !errors.Is(err, ErrNotRegular) {
			t.Fatalf("%s: %v", p, err)
		}
		if _, err := IsScript(p); !errors.Is(err, ErrNotRegular) {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(fifo, filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Preview(filepath.Join(d, "link")); !errors.Is(err, ErrNotRegular) {
		t.Fatal(err)
	}
}
func TestScan(t *testing.T) {
	d := t.TempDir()
	put(t, d, "job", "#!/bin/sh\n#SBATCH -J x\n")
	names := make([]string, ScanLimit+10)
	for i := range names {
		names[i] = "job"
	}
	n := 0
	for r := range ScanScripts(context.Background(), d, names) {
		if !r.IsScript || r.Err != nil {
			t.Fatalf("%+v", r)
		}
		n++
	}
	if n != ScanLimit {
		t.Fatal(n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range ScanScripts(ctx, d, names) {
		t.Fatal("result after cancellation")
	}
	ctx, cancel = context.WithCancel(context.Background())
	ch := ScanScripts(ctx, d, names)
	<-ch
	cancel()
	for range ch {
	} // producer exits even if receiver leaves.
	put(t, d, "late", "#!/bin/sh\n"+strings.Repeat("#\n", 4096)+"#SBATCH -J x\n")
	for r := range ScanScripts(context.Background(), d, []string{"late"}) {
		if r.IsScript {
			t.Fatal("scan read beyond 8 KiB")
		}
	}
	if ok, err := IsScript(filepath.Join(d, "late")); err != nil || !ok {
		t.Fatalf("preview override %v %v", ok, err)
	}
}

func TestPermissionDenied(t *testing.T) {
	if os.Getenv("FILES_PERMISSION_CHILD") == "1" {
		path := os.Getenv("FILES_DENIED_PATH")
		if _, err := List(path, false); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("List: %v", err)
		}
		if _, err := Preview(path + "/file"); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("Preview: %v", err)
		}
		return
	}
	d, err := os.MkdirTemp("", "elzar-permission-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(d) }()
	put(t, d, "file", "data")
	if err := os.Chmod(d, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(d, 0700) }()
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.CreateTemp("", "elzar-permission-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(exe.Name()) }()
	if _, err := exe.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := exe.Chmod(0755); err != nil {
		t.Fatal(err)
	}
	if err := exe.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe.Name(), "-test.run=^TestPermissionDenied$")
	cmd.Env = append(os.Environ(), "FILES_PERMISSION_CHILD=1", "FILES_DENIED_PATH="+d)
	if os.Geteuid() == 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("permission subprocess: %s %v", out, err)
	}
}
