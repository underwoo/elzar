package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"
	"github.com/underwoo/elzar/internal/files"
	"github.com/underwoo/elzar/internal/plugin"
	"github.com/underwoo/elzar/internal/registry"
)

func filesApp(t *testing.T) *App {
	t.Helper()
	a := NewWithOptions(nil, Options{})
	a.tab = filesTab
	a.width = 120
	a.height = 14
	a.resetFiles(t.TempDir())
	t.Cleanup(func() { a.files.cancel() })
	a.files.children[a.files.dir] = files.Listing{Entries: []files.Entry{{Name: "dir", Kind: files.Directory}, {Name: "job.sh", Kind: files.Regular}, {Name: "notes", Kind: files.Regular}}}
	delete(a.files.loading, a.files.dir)
	a.rebuildFiles()
	return a
}
func TestFilesNavigationAndIsolation(t *testing.T) {
	a := filesApp(t)
	a.plugins = []plugin.Plugin{{Manifest: plugin.Manifest{Name: "queue-action", Type: "command", Key: "z", Scopes: []string{"queue"}, Command: "true"}}}
	if a.pluginScope() != "" {
		t.Fatal("Files has plugin scope")
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if cmd != nil {
		t.Fatal("queue plugin dispatched on Files")
	}
	pressRune(a, '2')
	pressRune(a, 'j')
	if a.files.cursor != 2 {
		t.Fatal(a.files.cursor)
	}
	pressRune(a, 'g')
	pressRune(a, 'g')
	if a.files.cursor != 0 {
		t.Fatal(a.files.cursor)
	}
	pressRune(a, 'G')
	if a.files.cursor != 2 {
		t.Fatal(a.files.cursor)
	}
	pressRune(a, '/')
	for _, r := range "job" {
		pressRune(a, r)
	}
	if len(a.files.rows) != 1 {
		t.Fatal(a.files.rows)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.files.filtering {
		t.Fatal("still filtering")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(a.files.rows) != 3 {
		t.Fatal("filter not cleared")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if a.tab != registry.QueueTab {
		t.Fatal(a.tab)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if a.tab != filesTab {
		t.Fatal(a.tab)
	}
	a.tab = registry.QueueTab
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	pressRune(a, '4')
	if a.tab != filesTab {
		t.Fatal(a.tab)
	}
}
func TestFilesSlowListAndDebounce(t *testing.T) {
	a := filesApp(t)
	release := make(chan struct{})
	started := make(chan struct{})
	a.files.list = func(string, bool) (files.Listing, error) { close(started); <-release; return files.Listing{}, nil }
	cmd := a.resetFiles(a.files.dir)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started
	if !strings.Contains(a.View(), "loading…") {
		t.Fatal(a.View())
	}
	pressRune(a, '?')
	if !a.showHelp {
		t.Fatal("UI blocked")
	}
	pressRune(a, '?')
	close(release)
	a.Update(<-done)
	a.files.children[a.files.dir] = files.Listing{Entries: []files.Entry{{Name: "a", Kind: files.Regular}, {Name: "b", Kind: files.Regular}}}
	a.rebuildFiles()
	reads := 0
	a.files.read = func(string) (files.FilePreview, error) { reads++; return files.FilePreview{Text: "ok"}, nil }
	first := a.debounceFilePreview()
	a.files.cursor = 1
	second := a.debounceFilePreview()
	_, read := a.Update(first())
	if read != nil || reads != 0 {
		t.Fatal("stale debounce read")
	}
	_, read = a.Update(second())
	if read == nil || reads != 0 {
		t.Fatal("read was not deferred")
	}
	a.Update(read())
	if reads != 1 || a.files.preview.Text != "ok" {
		t.Fatal("preview missing")
	}
	old := filesPreviewed{id: a.files.previewID - 1, path: "old", preview: files.FilePreview{Text: "stale"}}
	a.Update(old)
	if a.files.preview.Text != "ok" {
		t.Fatal("stale result accepted")
	}
}
func TestFilesFIFOAndTree(t *testing.T) {
	a := filesApp(t)
	fifo := filepath.Join(a.files.dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	a.files.rows = []fileRow{{path: fifo, entry: files.Entry{Name: "pipe", Kind: files.Other}}}
	a.files.cursor = 0
	due := a.debounceFilePreview()
	_, read := a.Update(due())
	a.Update(read())
	if !strings.Contains(a.files.previewError, "not a regular file") {
		t.Fatal(a.files.previewError)
	}
	if cmd := a.openFileExternal("PAGER", "less"); cmd != nil {
		t.Fatal("FIFO sent to pager")
	}
	dir := filepath.Join(a.files.dir, "dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child"), []byte("hi"), 0600); err != nil {
		t.Fatal(err)
	}
	a.files.children[a.files.dir] = files.Listing{Entries: []files.Entry{{Name: "dir", Kind: files.Directory}}}
	a.rebuildFiles()
	cmd := a.handleFilesKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	a.Update(cmd())
	if len(a.files.rows) != 2 || a.files.rows[1].depth != 1 {
		t.Fatal(a.files.rows)
	}
	a.files.cursor = 1
	pressRune(a, 'h')
	if len(a.files.rows) != 1 || a.files.cursor != 0 {
		t.Fatal("collapse did not select parent")
	}
}
func TestFilesPreviewOverridesScan(t *testing.T) {
	a := filesApp(t)
	path := a.selectedFile()
	a.Update(filesPreviewed{id: a.files.previewID, path: path, preview: files.FilePreview{IsScript: true}})
	a.files.previewPath = "another"
	stream := make(chan files.ScriptResult)
	close(stream)
	a.Update(filesScanned{generation: a.files.generation, dir: a.files.dir, result: files.ScriptResult{Name: filepath.Base(path), IsScript: false}, stream: stream, ok: true})
	if !a.files.scripts[path] {
		t.Fatal("scan replaced authoritative preview")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.files.cancel()
	a.files.ctx = ctx
	a.files.cancel = cancel
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if ctx.Err() == nil {
		t.Fatal("scan not cancelled on leaving Files")
	}
}
func TestFilesGoldens(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	for _, width := range []int{60, 120} {
		for _, help := range []bool{false, true} {
			t.Run(fmt.Sprintf("width_%d_help_%t", width, help), func(t *testing.T) {
				a := filesApp(t)
				a.files.dir = "/work"
				a.files.rows = []fileRow{{path: "/work/job.sh", entry: files.Entry{Name: "job.sh", Kind: files.Regular}}, {path: "/work/notes", entry: files.Entry{Name: "notes", Kind: files.Regular}}}
				a.files.scripts["/work/job.sh"] = true
				a.width = width
				a.height = 24
				a.showHelp = help
				a.files.previewPath = "/work/job.sh"
				a.files.preview = files.FilePreview{Text: "#!/bin/sh\n#SBATCH --job-name=sample\necho hello", Size: 49, ModTime: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), IsScript: true}
				teatest.RequireEqualOutput(t, []byte(a.View()))
			})
		}
	}
}

func TestFilesEditorReturnsAndRefreshes(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprint(deleted), func(t *testing.T) {
			a := filesApp(t)
			path := filepath.Join(a.files.dir, "job.sh")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n#SBATCH -J test\n"), 0600); err != nil {
				t.Fatal(err)
			}
			sibling := filepath.Join(a.files.dir, "next")
			if err := os.WriteFile(sibling, []byte("sibling preview"), 0600); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nprintf '\n# edited\n' >> \"$1\"\n"
			expected := "# edited"
			if deleted {
				script = "#!/bin/sh\nrm -- \"$1\"\n"
				expected = "sibling preview"
			}
			tool := filepath.Join(t.TempDir(), "editor")
			if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("EDITOR", tool)
			a.files.rows = []fileRow{{path: path, entry: files.Entry{Name: "job.sh", Kind: files.Regular}}}
			a.files.rows = append(a.files.rows, fileRow{path: sibling, entry: files.Entry{Name: "next", Kind: files.Regular}})
			a.files.previewPath = path
			a.files.preview = files.FilePreview{Text: "before"}
			tm := teatest.NewTestModel(t, a, teatest.WithInitialTermSize(120, 20))
			t.Cleanup(func() { _ = tm.Quit() })
			tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
			teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte(expected)) }, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
			tm.Type("q")
			final := tm.FinalModel(t, teatest.WithFinalTimeout(time.Second)).(*App)
			if final.tab != filesTab || !strings.Contains(final.files.preview.Text, expected) {
				t.Fatal("editor failed to refresh Files")
			}
			want := path
			if deleted {
				want = sibling
			}
			if final.selectedFile() != want {
				t.Fatalf("selection=%s", final.selectedFile())
			}
		})
	}
}

func TestFilesEnterAndParentSelection(t *testing.T) {
	a := filesApp(t)
	root := a.files.dir
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := a.handleFilesKey(tea.KeyMsg{Type: tea.KeyEnter})
	a.Update(cmd())
	if a.files.dir != dir {
		t.Fatalf("root=%s", a.files.dir)
	}
	cmd = a.handleFilesKey(tea.KeyMsg{Type: tea.KeyBackspace})
	a.Update(cmd())
	if a.files.dir != root || a.selectedFile() != dir {
		t.Fatalf("root=%s selected=%s", a.files.dir, a.selectedFile())
	}
}
func TestFilesExternalRefreshRetainsTree(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprint(deleted), func(t *testing.T) {
			a := filesApp(t)
			root := a.files.dir
			dir := filepath.Join(root, "dir")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a", "b", "c"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			listing, err := files.List(root, false)
			if err != nil {
				t.Fatal(err)
			}
			a.files.children[root] = listing
			listing, err = files.List(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			a.files.children[dir] = listing
			a.files.expanded[dir] = true
			a.rebuildFiles()
			a.selectFilePaths([]string{filepath.Join(dir, "b")})
			a.height = 9
			a.keepFilesCursorVisible()
			scroll := a.files.scroll
			if deleted {
				if err := os.Remove(filepath.Join(dir, "b")); err != nil {
					t.Fatal(err)
				}
			}
			_, cmd := a.Update(filesExternalDone{})
			a.Update(cmd())
			want := filepath.Join(dir, "b")
			if deleted {
				want = filepath.Join(dir, "c")
			}
			if a.selectedFile() != want || !a.files.expanded[dir] || a.files.scroll != scroll {
				t.Fatalf("selected=%s expanded=%v scroll=%d want=%d", a.selectedFile(), a.files.expanded[dir], a.files.scroll, scroll)
			}
		})
	}
}
