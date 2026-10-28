// Package memory is motita's memory: a folder of plain Markdown files that Obsidian (or any editor)
// opens as it is. The folder IS the memory, so what the person edits outside motita is what motita
// sees, and nothing needs migrating when the index changes: it is rebuilt from the files.
//
// An entry is addressed by its path inside the memory, with forward slashes ("ideas/trip.md"). Its
// first folder is its namespace (a collection such as "Engineering" or "HR"). Every path is
// confined: no "..", no hidden segment, only .md files, and nothing that resolves through a
// symbolic link to outside the memory.
//
// Recall (see index.go, chunk.go and search.go) is a hybrid retrieval: entries are cut into small
// child passages that carry their title and heading trail, matched by words and by a holographic
// vector, and answered with the larger parent section they belong to.
package memory

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Limits: the memory is read whole to find backlinks, so a runaway loop must not make it huge.
const (
	MaxEntries    = 5000
	MaxEntryBytes = 192 << 10
	maxPathLen    = 240
	ext           = ".md"
)

// Errors a caller can tell apart.
var (
	ErrBadPath  = errors.New("an entry path is a name like \"folder/idea.md\": no \"..\", no hidden folders, .md only")
	ErrNotFound = errors.New("there is no entry with that path")
	ErrExists   = errors.New("an entry with that path already exists")
	ErrTooBig   = errors.New("the entry is too big")
	ErrTooMany  = errors.New("the memory already holds the most entries it can")
)

// Entry is one file of the memory, as the list reports it.
type Entry struct {
	Path     string    `json:"path"`
	Title    string    `json:"title"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// The disk calls that can only fail for reasons a test cannot arrange as every user (root reads
// what it likes), so a test replaces them.
var (
	readFile   = os.ReadFile
	createTemp = os.CreateTemp
	moveFile   = os.Rename
)

// Store is the folder the entries live in.
type Store struct {
	root   string
	source Source
}

// Open returns the memory at root. The folder is created when the first entry is written.
func Open(root string) *Store { return &Store{root: root} }

// Root is the memory's folder on disk.
func (v *Store) Root() string { return v.root }

// Clean returns p as the canonical entry path, or ErrBadPath.
func Clean(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if p == "" || len(p) > maxPathLen || strings.ContainsRune(p, 0) {
		return "", ErrBadPath
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return "", ErrBadPath
		}
	}
	if !strings.EqualFold(path.Ext(p), ext) {
		return "", ErrBadPath
	}
	return p, nil
}

// Title is the name an entry goes by: its file name without the extension.
func Title(p string) string { return strings.TrimSuffix(path.Base(p), path.Ext(p)) }

// abs resolves a clean path to a file under the memory, refusing a route out of it through a
// symbolic link: the deepest folder that exists must still resolve inside the memory.
func (v *Store) abs(rel string) (string, error) {
	full := filepath.Join(v.root, filepath.FromSlash(rel))
	// Clean already refuses ".."; this is the same rule at the one place a path becomes a file.
	if !strings.HasPrefix(full, filepath.Clean(v.root)+string(filepath.Separator)) {
		return "", ErrBadPath
	}
	rootReal, err := filepath.EvalSymlinks(v.root)
	if err != nil {
		// A memory that does not exist yet has nothing to escape through.
		return full, nil
	}
	dir := filepath.Dir(full)
	for {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
				return "", ErrBadPath
			}
			break
		}
		dir = filepath.Dir(dir)
	}
	if fi, err := os.Lstat(full); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", ErrBadPath
	}
	return full, nil
}

// List returns every entry in path order. A folder that cannot be read is skipped: the memory is the
// person's, and one locked folder must not hide the rest.
func (v *Store) List() []Entry {
	out := []Entry{}
	_ = filepath.WalkDir(v.root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return fs.SkipDir
		case p == v.root:
			return nil
		case strings.HasPrefix(d.Name(), "."):
			return skipHidden(d)
		case len(out) >= MaxEntries:
			return filepath.SkipAll
		default:
			// a visible entry under the cap: look at it below
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() && strings.EqualFold(filepath.Ext(d.Name()), ext) {
			rel, _ := filepath.Rel(v.root, p)
			rel = filepath.ToSlash(rel)
			out = append(out, Entry{Path: rel, Title: Title(rel), Size: info.Size(), Modified: info.ModTime().UTC()})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// skipHidden passes over a hidden entry: a hidden folder is not entered.
func skipHidden(d fs.DirEntry) error {
	if d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// Read returns the text of an entry and when it last changed.
func (v *Store) Read(p string) (string, time.Time, error) {
	rel, err := Clean(p)
	if err != nil {
		return "", time.Time{}, err
	}
	full, err := v.abs(rel)
	if err != nil {
		return "", time.Time{}, err
	}
	fi, err := os.Stat(full)
	if err != nil || !fi.Mode().IsRegular() {
		return "", time.Time{}, ErrNotFound
	}
	if fi.Size() > MaxEntryBytes {
		return "", time.Time{}, ErrTooBig
	}
	b, err := readFile(full)
	if err != nil {
		return "", time.Time{}, err
	}
	return string(b), fi.ModTime().UTC(), nil
}

// Write saves an entry, creating its folders. create makes it fail when the entry already exists.
func (v *Store) Write(p, body string, create bool) (Entry, error) {
	rel, err := Clean(p)
	if err != nil {
		return Entry{}, err
	}
	if len(body) > MaxEntryBytes {
		return Entry{}, ErrTooBig
	}
	full, err := v.abs(rel)
	if err != nil {
		return Entry{}, err
	}
	_, statErr := os.Stat(full)
	exists := statErr == nil
	switch {
	case exists && create:
		return Entry{}, ErrExists
	case !exists && len(v.List()) >= MaxEntries:
		return Entry{}, ErrTooMany
	default:
		// an overwrite, or room for a new entry
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return Entry{}, err
	}
	// Written beside the entry and renamed over it, so an editor that watches the folder (Obsidian)
	// never reads half an entry.
	tmp, err := createTemp(filepath.Dir(full), ".motita-*.tmp")
	if err != nil {
		return Entry{}, err
	}
	_, werr := tmp.WriteString(body)
	if err := errors.Join(werr, tmp.Close(), moveFile(tmp.Name(), full)); err != nil {
		os.Remove(tmp.Name())
		return Entry{}, err
	}
	return Entry{Path: rel, Title: Title(rel), Size: int64(len(body)), Modified: time.Now().UTC()}, nil
}

// Delete removes an entry. The folders it leaves empty stay: they are the person's.
func (v *Store) Delete(p string) error {
	rel, err := Clean(p)
	if err != nil {
		return err
	}
	full, err := v.abs(rel)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(full); err != nil || !fi.Mode().IsRegular() {
		return ErrNotFound
	}
	return os.Remove(full)
}

// Rename moves an entry. It refuses to replace another one.
func (v *Store) Rename(from, to string) error {
	src, err := Clean(from)
	if err != nil {
		return err
	}
	dst, err := Clean(to)
	if err != nil {
		return err
	}
	srcFull, err := v.abs(src)
	if err != nil {
		return err
	}
	dstFull, err := v.abs(dst)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(srcFull); err != nil || !fi.Mode().IsRegular() {
		return ErrNotFound
	}
	if _, err := os.Stat(dstFull); err == nil {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dstFull), 0o755); err != nil {
		return err
	}
	return os.Rename(srcFull, dstFull)
}
