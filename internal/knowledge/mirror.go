package knowledge

import (
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/redact"
)

// ProjectsCollection is where earlier versions mirrored each project's lessons (Projects/<folder>).
// The lessons now go into the project's own collection, the one its sessions write to, and the
// reorganization of the memory moves the old entries there.
const ProjectsCollection = "Projects"

// LessonsTitle is the entry of a project's collection that holds its lessons.
const LessonsTitle = "Lessons"

// KeepLessons records lessons in the project's lessons file and adds the ones the file did not
// know yet to the person's memory, in the project's entry under ProjectsCollection. Every finished
// task and every "remember in:project" goes through here, so the memory grows on its own instead
// of waiting for the model to decide to save. m may be nil (no memory in this session).
func KeepLessons(m *memory.Store, workspace string, lessons []string) error {
	clean := make([]string, len(lessons))
	for i, l := range lessons {
		clean[i] = redact.String(l)
	}
	lessons = clean
	fresh := newLessons(workspace, lessons)
	if err := RecordLessons(workspace, lessons); err != nil {
		return err
	}
	mirrorLessons(m, workspace, fresh)
	return nil
}

// ProjectEntry is the memory entry that mirrors a project's lessons: <project>/Lessons, in the
// collection the project's sessions write to. The project is the one the memory view belongs to
// (its title), else the workspace folder's name. ok is false when neither names one: lessons are
// never kept under a name every project would share.
func ProjectEntry(m *memory.Store, workspace string) (string, bool) {
	name := ""
	if m != nil {
		name = m.Project()
	}
	if name == "" {
		name = filepath.Base(filepath.Clean(workspace))
	}
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "", false
	}
	return name + "/" + LessonsTitle, true
}

// newLessons are the lessons, flattened to one line each, that the project's file does not hold yet.
func newLessons(workspace string, lessons []string) []string {
	if workspace == "" {
		return nil
	}
	have := map[string]bool{}
	for _, l := range Lessons(workspace) {
		have[strings.ToLower(l)] = true
	}
	var out []string
	for _, l := range nonBlank(lessons) {
		line := oneLine(l)
		if key := strings.ToLower(line); !have[key] {
			have[key] = true
			out = append(out, line)
		}
	}
	return out
}

// mirrorLessons appends lessons to the project's memory entry as a bullet list.
func mirrorLessons(m *memory.Store, workspace string, lessons []string) {
	if m == nil || len(lessons) == 0 {
		return
	}
	if entry, ok := ProjectEntry(m, workspace); ok {
		m.Keep(entry, "- "+strings.Join(lessons, "\n- "), memory.KindLesson)
	}
}
