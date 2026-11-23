package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/underwoo/elzar/internal/files"
)

// Files actions are intentionally absent from allAppBindings: they do not
// reserve keys in the job/account plugin scopes.
var filesKeys = struct{ enter, open, collapse, parent, hidden, refresh, pager, editor, submit, filter key.Binding }{
	key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "enter directory")),
	key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "expand/collapse")),
	key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h", "collapse/parent")),
	key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "parent directory")),
	key.NewBinding(key.WithKeys("."), key.WithHelp(".", "hidden files")),
	key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh files")),
	key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "view")),
	key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
	key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "submit script")),
	key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter names")),
}

func (a *App) filesHelpGroups() []helpGroup {
	return []helpGroup{
		{title: "Navigation", bindings: []key.Binding{appKeys.up, appKeys.down, appKeys.count, appKeys.top, appKeys.bottom, appKeys.pageUp, appKeys.pageDown}},
		{title: "Files", bindings: []key.Binding{filesKeys.enter, filesKeys.open, filesKeys.collapse, filesKeys.parent, filesKeys.hidden, filesKeys.refresh, filesKeys.pager, filesKeys.editor, filesKeys.submit, filesKeys.filter}},
		{title: "App", bindings: []key.Binding{appKeys.tab, appKeys.shiftTab, key.NewBinding(key.WithHelp(formatKeyLabel(a.options.TabPrefix)+" 1/2/3/4", "queue/recent/accounts/files")), appKeys.help, appKeys.quit}},
	}
}
func (a *App) handleFilesKey(msg tea.KeyMsg) tea.Cmd {
	f := &a.files
	if f.filtering {
		switch msg.String() {
		case "enter", "esc":
			f.filtering = false
		case "backspace":
			r := []rune(f.filter)
			if len(r) > 0 {
				f.filter = string(r[:len(r)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				f.filter += string(msg.Runes)
			}
		}
		a.rebuildFiles()
		return nil
	}
	switch {
	case key.Matches(msg, appKeys.quit):
		if f.cancel != nil {
			f.cancel()
		}
		return tea.Quit
	case key.Matches(msg, appKeys.tab), msg.String() == "shift+tab":
		a.toggleTabKey(msg.String())
	case key.Matches(msg, filesKeys.filter):
		f.filtering = true
	case key.Matches(msg, appKeys.back):
		f.filter = ""
		a.rebuildFiles()
	case key.Matches(msg, filesKeys.hidden):
		f.hidden = !f.hidden
		return a.resetFiles(f.dir)
	case key.Matches(msg, filesKeys.refresh):
		return a.resetFiles(f.dir)
	case key.Matches(msg, filesKeys.parent):
		f.filter = ""
		return a.enterFiles(filepath.Dir(f.dir), f.dir)
	case key.Matches(msg, filesKeys.collapse):
		path := a.selectedFile()
		if f.expanded[path] {
			delete(f.expanded, path)
			a.rebuildFiles()
		} else if path != "" && filepath.Dir(path) != f.dir {
			parent := filepath.Dir(path)
			delete(f.expanded, parent)
			a.rebuildFiles()
			for i, r := range f.rows {
				if r.path == parent {
					f.cursor = i
					break
				}
			}
			a.keepFilesCursorVisible()
		} else {
			f.filter = ""
			return a.enterFiles(filepath.Dir(f.dir), f.dir)
		}
	case key.Matches(msg, filesKeys.enter), key.Matches(msg, filesKeys.open):
		path := a.selectedFile()
		if path == "" {
			return nil
		}
		r := f.rows[f.cursor]
		if r.entry.Kind != files.Directory && r.entry.Kind != files.Symlink {
			return nil
		}
		if key.Matches(msg, filesKeys.enter) {
			return a.enterFiles(path, "")
		}
		if f.expanded[path] {
			delete(f.expanded, path)
			a.rebuildFiles()
		} else {
			f.expanded[path] = true
			return a.listFiles(path)
		}
	case key.Matches(msg, filesKeys.pager):
		return a.openFileExternal("PAGER", "less")
	case key.Matches(msg, filesKeys.editor):
		return a.openFileExternal("EDITOR", "vi")
	case key.Matches(msg, filesKeys.submit):
		path := a.selectedFile()
		if path == "" {
			return nil
		}
		if err := a.openSubmitForm(path); err != nil {
			f.err = err.Error()
		} else {
			f.err = ""
		}
		return nil
	}
	return nil
}
func (a *App) openFileExternal(env, fallback string) tea.Cmd {
	path := a.selectedFile()
	if path == "" {
		return nil
	}
	// A selected successful preview is required, so FIFOs/devices cannot be sent
	// to tools accidentally. External tools own their subsequent filesystem I/O.
	if a.files.previewPath != path || a.files.previewError != "" {
		a.files.err = "Wait for a regular-file preview before opening"
		return nil
	}
	argv := strings.Fields(os.Getenv(env))
	if len(argv) == 0 {
		argv = []string{fallback}
	}
	argv = append(argv, path)
	return tea.ExecProcess(exec.Command(argv[0], argv[1:]...), func(err error) tea.Msg { return filesExternalDone{err} })
}
func fileText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}
func (a *App) renderFiles(width int) string {
	f := &a.files
	var out strings.Builder
	fmt.Fprintln(&out, titleStyle.Render("elzar"))
	fmt.Fprintln(&out, a.tabLine())
	fmt.Fprintln(&out, truncateLine(fileText(f.dir), width))
	leftWidth := width
	if width >= 100 {
		leftWidth = width / 2
	}
	rows := a.tableRowCapacity()
	preview := []string{"Select a regular file"}
	if f.previewPath != "" {
		if f.previewError != "" {
			preview = []string{f.previewError}
		} else {
			preview = []string{fmt.Sprintf("%d bytes · %s", f.preview.Size, f.preview.ModTime.Local().Format("2006-01-02 15:04:05"))}
			preview = append(preview, strings.Split(strings.ReplaceAll(fileText(f.preview.Text), "\t", "    "), "\n")...)
			if f.preview.Truncated {
				preview = append([]string{preview[0] + " · truncated at 64 KiB"}, preview[1:]...)
			}
		}
	} else if a.selectedFile() != "" {
		preview = []string{"loading preview…"}
	}
	for line := 0; line < rows; line++ {
		index := f.scroll + line
		text := ""
		if index < len(f.rows) {
			r := f.rows[index]
			mark := "  "
			if index == f.cursor {
				mark = "> "
			}
			kind := "  "
			if r.entry.Kind == files.Directory {
				kind = "▸ "
				if f.expanded[r.path] {
					kind = "▾ "
				}
			}
			if f.scripts[r.path] {
				kind = "* "
			}
			text = mark + strings.Repeat("  ", r.depth) + kind + fileText(r.entry.Name)
			if r.entry.Kind == files.Symlink {
				text += " → " + fileText(r.entry.Target)
			}
			if f.loading[r.path] {
				text += " loading…"
			}
		} else if line == 0 && f.loading[f.dir] {
			text = "loading…"
		} else if line == 0 {
			text = "(empty directory)"
		}
		text = truncateLine(strings.ReplaceAll(text, "\n", "�"), leftWidth)
		if width >= 100 {
			right := ""
			if line < len(preview) {
				right = preview[line]
			}
			fmt.Fprintln(&out, pad(text, leftWidth)+" │ "+truncateLine(right, width-leftWidth-3))
		} else {
			fmt.Fprintln(&out, text)
		}
	}
	status := f.err
	if status == "" {
		omitted := 0
		for _, listing := range f.children {
			omitted += listing.Omitted
		}
		if omitted > 0 {
			status = fmt.Sprintf("%d more not shown in loaded directories", omitted)
		}
	}
	if status == "" {
		status = fmt.Sprintf("%d entries", len(f.rows))
	}
	if f.filtering || f.filter != "" {
		status += "  /" + f.filter
	}
	if nav := a.navigationStatus(); nav != "" {
		status += "  " + nav
	}
	fmt.Fprintln(&out, truncateLine(status, width))
	fmt.Fprint(&out, truncateLine("enter open · l expand · h collapse · . hidden · / filter · v view · e edit · ? help", width))
	return fitOutput(out.String(), width)
}
