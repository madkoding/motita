package notes

import (
	"os"
	"path/filepath"
	"strings"
)

// DeleteFolder removes every note inside the folder p, sub-folders included, and the folders it
// leaves empty, and answers how many notes it removed. A file that is not a note stays, and so does
// the folder that holds it: those are the person's. The vault's own root cannot be deleted this way.
func (v *Vault) DeleteFolder(p string) (int, error) {
	// A folder is checked as the path of a note inside it: the same rules, in one place.
	inside := strings.Trim(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"), "/") + "/x" + ext
	rel, err := Clean(inside)
	if err != nil {
		return 0, err
	}
	full, err := v.abs(rel)
	if err != nil {
		return 0, err
	}
	dir := filepath.Dir(full)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return 0, ErrNotFound
	}
	removed := removeNotes(dir)
	_ = os.Remove(dir)
	return removed, nil
}

// removeNotes deletes the notes under dir, and the folders that end up empty below it.
func removeNotes(dir string) int {
	// A folder that cannot be read has nothing to remove.
	entries, _ := os.ReadDir(dir)
	removed := 0
	for _, e := range entries {
		at := filepath.Join(dir, e.Name())
		switch {
		case strings.HasPrefix(e.Name(), "."):
			// hidden entries are not notes and are never entered
		case e.IsDir():
			removed += removeNotes(at)
			// Remove refuses a folder that still holds something, which is what keeps it.
			_ = os.Remove(at)
		case e.Type().IsRegular() && strings.EqualFold(filepath.Ext(e.Name()), ext):
			_ = os.Remove(at)
			removed++
		default:
			// a link or any other file is left where it is
		}
	}
	return removed
}
