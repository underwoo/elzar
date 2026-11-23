package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/underwoo/elzar/internal/files"
	"github.com/underwoo/elzar/internal/registry"
)

const filesTab registry.Tab = "files"

type fileRow struct {
	path  string
	entry files.Entry
	depth int
}
type filesState struct {
	dir                            string
	hidden                         bool
	children                       map[string]files.Listing
	expanded                       map[string]bool
	loading                        map[string]bool
	rows                           []fileRow
	cursor, scroll                 int
	generation, previewID          uint64
	preview                        files.FilePreview
	previewPath, previewError, err string
	filter                         string
	filtering                      bool
	scripts                        map[string]bool
	cancel                         context.CancelFunc
	ctx                            context.Context
	list                           func(string, bool) (files.Listing, error)
	read                           func(string) (files.FilePreview, error)
}
type filesListed struct {
	dir        string
	generation uint64
	listing    files.Listing
	err        error
}
type filesPreviewDue struct {
	id   uint64
	path string
}
type filesPreviewed struct {
	id      uint64
	path    string
	preview files.FilePreview
	err     error
}
type filesScanned struct {
	generation uint64
	dir        string
	result     files.ScriptResult
	stream     <-chan files.ScriptResult
	ok         bool
}
type filesExternalDone struct{ err error }

func (a *App) resetFiles(dir string) tea.Cmd {
	f := &a.files
	if f.cancel != nil {
		f.cancel()
	}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.dir = dir
	f.generation++
	f.previewID++
	f.children = map[string]files.Listing{}
	f.expanded = map[string]bool{dir: true}
	f.loading = map[string]bool{}
	f.scripts = map[string]bool{}
	f.rows = nil
	f.cursor = 0
	f.scroll = 0
	f.previewPath = ""
	f.previewError = ""
	f.err = ""
	return a.listFiles(dir)
}
func (a *App) listFiles(dir string) tea.Cmd {
	f := &a.files
	f.loading[dir] = true
	list := f.list
	if list == nil {
		list = files.List
	}
	generation, hidden := f.generation, f.hidden
	return func() tea.Msg { l, err := list(dir, hidden); return filesListed{dir, generation, l, err} }
}
func (a *App) rebuildFiles() {
	f := &a.files
	f.rows = nil
	var visit func(string, int)
	visit = func(dir string, depth int) {
		for _, e := range f.children[dir].Entries {
			if f.filter != "" && !strings.Contains(strings.ToLower(e.Name), strings.ToLower(f.filter)) {
				continue
			}
			path := filepath.Join(dir, e.Name)
			f.rows = append(f.rows, fileRow{path, e, depth})
			if f.expanded[path] {
				visit(path, depth+1)
			}
		}
	}
	visit(f.dir, 0)
	a.keepFilesCursorVisible()
}
func (a *App) selectedFile() string {
	if len(a.files.rows) == 0 {
		return ""
	}
	return a.files.rows[a.files.cursor].path
}
func (a *App) keepFilesCursorVisible() {
	f := &a.files
	f.cursor = max(0, min(f.cursor, len(f.rows)-1))
	capacity := a.tableRowCapacity()
	f.scroll = max(0, min(f.scroll, max(0, len(f.rows)-capacity)))
	if f.cursor < f.scroll {
		f.scroll = f.cursor
	}
	if f.cursor >= f.scroll+capacity {
		f.scroll = f.cursor - capacity + 1
	}
}
func (a *App) debounceFilePreview() tea.Cmd {
	f := &a.files
	f.previewID++
	id, path := f.previewID, a.selectedFile()
	f.previewPath = ""
	f.previewError = ""
	if path == "" {
		return nil
	}
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return filesPreviewDue{id, path} })
}
func nextFileScan(generation uint64, dir string, stream <-chan files.ScriptResult) tea.Cmd {
	return func() tea.Msg { r, ok := <-stream; return filesScanned{generation, dir, r, stream, ok} }
}
func (a *App) updateFiles(msg tea.Msg) (tea.Cmd, bool) {
	f := &a.files
	switch m := msg.(type) {
	case filesListed:
		if m.generation != f.generation {
			return nil, true
		}
		delete(f.loading, m.dir)
		if m.err != nil {
			f.err = m.err.Error()
			delete(f.expanded, m.dir)
			return nil, true
		}
		f.children[m.dir] = m.listing
		f.err = ""
		a.rebuildFiles()
		names := []string{}
		for _, e := range m.listing.Entries {
			if e.Kind == files.Regular {
				names = append(names, e.Name)
			}
		}
		return tea.Batch(a.debounceFilePreview(), nextFileScan(f.generation, m.dir, files.ScanScripts(f.ctx, m.dir, names))), true
	case filesScanned:
		if m.generation != f.generation || !m.ok {
			return nil, true
		}
		path := filepath.Join(m.dir, m.result.Name)
		if path != f.previewPath && m.result.Err == nil {
			f.scripts[path] = m.result.IsScript
		}
		return nextFileScan(m.generation, m.dir, m.stream), true
	case filesPreviewDue:
		if m.id != f.previewID || a.tab != filesTab {
			return nil, true
		}
		read := f.read
		if read == nil {
			read = files.Preview
		}
		return func() tea.Msg { p, err := read(m.path); return filesPreviewed{m.id, m.path, p, err} }, true
	case filesPreviewed:
		if m.id != f.previewID || a.tab != filesTab {
			return nil, true
		}
		f.previewPath = m.path
		f.preview = m.preview
		if m.err != nil {
			f.previewError = m.err.Error()
		} else {
			f.previewError = ""
			f.scripts[m.path] = m.preview.IsScript
		}
		return nil, true
	case filesExternalDone:
		cmd := a.resetFiles(f.dir)
		if m.err != nil {
			f.err = fmt.Sprintf("external command: %v", m.err)
		}
		return cmd, true
	}
	return nil, false
}
func (a *App) filesAfterKey(oldTab registry.Tab, oldPath string, cmd tea.Cmd) tea.Cmd {
	if oldTab == filesTab && a.tab != filesTab {
		if a.files.cancel != nil {
			a.files.cancel()
		}
		a.files.previewID++
		return cmd
	}
	if a.tab != filesTab {
		return cmd
	}
	if oldTab != filesTab {
		dir := a.files.dir
		if dir == "" {
			var err error
			dir, err = os.Getwd()
			if err != nil {
				a.files.err = err.Error()
				return cmd
			}
		}
		return tea.Batch(cmd, a.resetFiles(dir))
	}
	if oldPath != a.selectedFile() {
		return tea.Batch(cmd, a.debounceFilePreview())
	}
	return cmd
}
