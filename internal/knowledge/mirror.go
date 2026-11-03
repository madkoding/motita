package knowledge

import (
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/memory"
)

// ProjectsCollection is the memory collection that mirrors each project's lessons, so what tasks
// learn shows in the person's memory (the window, search, Obsidian) and not only in the project.
const ProjectsCollection = "Projects"

// KeepLessons records lessons in the project's lessons file and adds the ones the file did not
// know yet to the person's memory, in the project's entry under ProjectsCollection. Every finished
// task and every "remember in:project" goes through here, so the memory grows on its own instead
// of waiting for the model to decide to save. m may be nil (no memory in this session).
func KeepLessons(m *memory.Store, workspace string, lessons []string) error {
	fresh := newLessons(workspace, lessons)
	if err := RecordLessons(workspace, lessons); err != nil {
		return err
	}
	mirrorLessons(m, workspace, fresh)
	return nil
}

// ProjectEntry is the memory entry that mirrors a project's lessons: Projects/<folder name>.
func ProjectEntry(workspace string) string {
	name := filepath.Base(filepath.Clean(workspace))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "project"
	}
	return ProjectsCollection + "/" + name
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
	m.AppendText(ProjectEntry(workspace), "- "+strings.Join(lessons, "\n- "))
}
