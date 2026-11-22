package files

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const EntryLimit = 5000

type Kind string

const (
	Directory Kind = "dir"
	Regular   Kind = "file"
	Symlink   Kind = "symlink"
	Other     Kind = "other"
)

type Entry struct {
	Name   string
	Kind   Kind
	Target string
}
type Listing struct {
	Entries []Entry
	Omitted int
}

func (l Listing) Marker() string {
	if l.Omitted == 0 {
		return ""
	}
	return fmt.Sprintf("%d more not shown", l.Omitted)
}

// List reads directory entries only; it never opens their contents or follows
// symlinks. Call List on a selected symlink directory to enter it explicitly.
func List(dir string, showHidden bool) (Listing, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Listing{}, fmt.Errorf("list directory %q: %w", dir, err)
	}
	result := Listing{}
	for _, e := range entries {
		if !showHidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		kind := Other
		switch t := e.Type(); {
		case t.IsDir():
			kind = Directory
		case t.IsRegular():
			kind = Regular
		case t&os.ModeSymlink != 0:
			kind = Symlink
		}
		result.Entries = append(result.Entries, Entry{Name: e.Name(), Kind: kind})
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if (a.Kind == Directory) != (b.Kind == Directory) {
			return a.Kind == Directory
		}
		return a.Name < b.Name
	})
	if len(result.Entries) > EntryLimit {
		result.Omitted = len(result.Entries) - EntryLimit
		result.Entries = result.Entries[:EntryLimit]
	}
	for i := range result.Entries {
		e := &result.Entries[i]
		if e.Kind == Symlink {
			e.Target, err = os.Readlink(filepath.Join(dir, e.Name))
			if err != nil {
				return Listing{}, fmt.Errorf("read symlink %q: %w", e.Name, err)
			}
		}
	}
	return result, nil
}
