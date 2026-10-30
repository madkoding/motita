package knowledge

import (
	"os"
	"path/filepath"
	"strings"
)

// LessonsFile is where what the agent learned about a project is kept, relative to the workspace:
// a command that works, a convention, a trap, a decision the person made. It is plain Markdown,
// so the person can read it, fix a line or delete it.
const LessonsFile = ".motita/memory.md"

// maxLessons bounds the file: the newest lessons stay, the oldest go.
const maxLessons = 60

// maxLessonsContext bounds how much of it is handed to the model.
const maxLessonsContext = 3000

// lessonsHeading opens the file the first time it is written.
const lessonsHeading = "# What earlier tasks learned about this project\n\n"

// oneLine flattens s to a single bounded line, so one lesson stays one line in the file.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// RecordLessons appends what a finished task learned, skipping what the file already says.
func RecordLessons(workspace string, lessons []string) error {
	lessons = nonBlank(lessons)
	if workspace == "" || len(lessons) == 0 {
		return nil
	}
	path := filepath.Join(workspace, LessonsFile)
	known := Lessons(workspace)
	have := map[string]bool{}
	for _, l := range known {
		have[strings.ToLower(l)] = true
	}
	for _, l := range lessons {
		line := oneLine(l)
		if !have[strings.ToLower(line)] {
			known = append(known, line)
			have[strings.ToLower(line)] = true
		}
	}
	if len(known) > maxLessons {
		known = known[len(known)-maxLessons:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(lessonsHeading)
	for _, l := range known {
		b.WriteString("- " + l + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// Lessons is what the file holds, one lesson per entry, without the bullets.
func Lessons(workspace string) []string {
	if workspace == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(workspace, LessonsFile)) // #nosec G304 -- the project's own lessons file.
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if l, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok && strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// LessonsContext is the lessons as the prompts read them, under the project note. Empty when
// there are none.
func LessonsContext(workspace string) string {
	lines := Lessons(workspace)
	if len(lines) == 0 {
		return ""
	}
	text := "- " + strings.Join(lines, "\n- ")
	if len(text) > maxLessonsContext {
		text = text[len(text)-maxLessonsContext:]
	}
	return "\n\nWhat earlier tasks learned about this project (true unless the code says otherwise):\n" + text
}

// nonBlank returns the entries that carry text, trimmed.
func nonBlank(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
