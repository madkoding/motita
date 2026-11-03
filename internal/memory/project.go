package memory

import (
	"path"
	"strings"
)

// A session that works in a project reaches the same memory as every other, through a view that
// knows the project's collection (the first folder of a path, see Namespace):
//
//   - what the agent writes under a bare title goes into that collection, so what it learns about
//     one project stays apart from the next; a title that starts with "/" is written at the top of
//     the memory, for what holds whatever the project (the person's preferences);
//   - a bare title it reads or adds to is looked for in the collection first;
//   - a search ranks the collection's passages first, without hiding the rest: what was written
//     for every project, or for another one, is still found.
//
// The person's own folders are untouched, and a search that names a collection (ns:Name) is
// answered as asked.

// ForProject returns the view of the memory for a session in the project named name; an empty name
// is the memory itself. The name is used as a folder, so what cannot be one is dropped from it.
func (v *Store) ForProject(name string) *Store {
	if v == nil {
		return nil
	}
	c := *v
	c.project = projectFolder(name)
	return &c
}

// Project is the collection this view writes to, "" for the memory itself.
func (v *Store) Project() string { return v.project }

// projectFolder is name as a single folder name a memory path accepts, "" when nothing is left.
func projectFolder(name string) string {
	name = strings.TrimSpace(strings.NewReplacer("/", "-", "\\", "-", "\x00", "").Replace(name))
	name = strings.TrimLeft(name, ".")
	if len(name) > 80 {
		name = name[:80]
	}
	return strings.TrimSpace(name)
}

// inProject says whether an entry path is in this view's collection.
func (v *Store) inProject(p string) bool {
	return v.project != "" && strings.EqualFold(Namespace(p), v.project)
}

// scopedRef is where a title the model gave goes in this view: a bare title into the project's
// collection, a title that starts with "/" at the top of the memory, anything else as given. top
// says the model asked for the top explicitly.
func (v *Store) scopedRef(ref string) (out string, top bool) {
	ref = strings.TrimSpace(strings.ReplaceAll(ref, "\\", "/"))
	if strings.HasPrefix(ref, "/") {
		return strings.TrimLeft(ref, "/"), true
	}
	if v.project != "" && !strings.Contains(ref, "/") {
		return v.project + "/" + ref, false
	}
	return ref, false
}

// resolveRef finds the entry a title names, looking in the project's collection first. A title that
// starts with "/" names the entry at the top of the memory before any other.
func (v *Store) resolveRef(all []Entry, ref string) (string, bool) {
	scoped, top := v.scopedRef(ref)
	if top || v.project != "" {
		want := strings.ToLower(strings.TrimSuffix(scoped, ext))
		for _, e := range all {
			if strings.ToLower(strings.TrimSuffix(e.Path, path.Ext(e.Path))) == want {
				return e.Path, true
			}
		}
	}
	return Resolve(all, ref)
}

// projectHint is the sentence a write in the project's collection ends with, "" outside one.
func (v *Store) projectHint(p string) string {
	if !v.inProject(p) {
		return ""
	}
	return " It is in this project's collection " + `"` + v.project + `"` +
		`; write a title that starts with "/" for what holds in every project.`
}
