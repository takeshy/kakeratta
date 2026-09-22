package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const noteSourceIndexName = "note-sources.json"

// noteSourceIndex is derived from the managed source blocks in notes. Keeping
// it beside the notes makes entry -> note lookups proportional to the notes
// that actually cite an entry, rather than to the whole workspace.
type noteSourceIndex map[string]map[string][]string

func sourceIDs(text string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, source := range NoteSources(text) {
		if source.EntryID != "" && !seen[source.EntryID] {
			seen[source.EntryID] = true
			ids = append(ids, source.EntryID)
		}
	}
	return ids
}

func indexSet(index noteSourceIndex, persona, path string, before, after []string) {
	for _, id := range before {
		if index[id] == nil {
			continue
		}
		paths := index[id][persona]
		kept := paths[:0]
		for _, candidate := range paths {
			if candidate != path {
				kept = append(kept, candidate)
			}
		}
		if len(kept) == 0 {
			delete(index[id], persona)
			if len(index[id]) == 0 {
				delete(index, id)
			}
		} else {
			index[id][persona] = kept
		}
	}
	for _, id := range after {
		if index[id] == nil {
			index[id] = map[string][]string{}
		}
		paths := index[id][persona]
		found := false
		for _, candidate := range paths {
			found = found || candidate == path
		}
		if !found {
			index[id][persona] = append(paths, path)
			sort.Strings(index[id][persona])
		}
	}
}

func loadNoteSourceIndex(root string) (noteSourceIndex, error) {
	index := noteSourceIndex{}
	if err := readJSON(filepath.Join(root, noteSourceIndexName), &index); err == nil {
		return index, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	memoryRoot := filepath.Join(root, "memory")
	err := filepath.WalkDir(memoryRoot, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			return nil
		}
		relative, err := filepath.Rel(memoryRoot, full)
		if err != nil {
			return err
		}
		persona := filepath.ToSlash(relative)
		slash := len(persona)
		for i, r := range persona {
			if r == '/' {
				slash = i
				break
			}
		}
		if slash == len(persona) {
			return nil
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		indexSet(index, persona[:slash], persona[slash+1:], nil, sourceIDs(string(raw)))
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := writeJSON(filepath.Join(root, noteSourceIndexName), index); err != nil {
		return nil, err
	}
	return index, nil
}
