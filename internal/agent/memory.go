package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// MemoryFile is where what the agent learned about a project is kept, relative to the workspace:
// a command that works, a convention, a trap, a decision the person made. It is plain Markdown,
// so the person can read it, fix a line or delete it.
const MemoryFile = ".motita/memory.md"

// maxMemoryLines bounds the file: the newest lessons stay, the oldest go.
const maxMemoryLines = 60

// maxMemoryContext bounds how much of it is handed to the model.
const maxMemoryContext = 3000

// memoryHeading opens the file the first time it is written.
const memoryHeading = "# What earlier tasks learned about this project\n\n"

// oneLine flattens s to a single bounded line, so one lesson stays one line in the file.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// recordLessons appends what a finished task learned, skipping what the file already says.
func recordLessons(workspace string, lessons []string) error {
	lessons = nonBlank(lessons)
	if workspace == "" || len(lessons) == 0 {
		return nil
	}
	path := filepath.Join(workspace, MemoryFile)
	known := memoryLines(workspace)
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
	if len(known) > maxMemoryLines {
		known = known[len(known)-maxMemoryLines:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(memoryHeading)
	for _, l := range known {
		b.WriteString("- " + l + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// memoryLines is what the file holds, one lesson per entry, without the bullets.
func memoryLines(workspace string) []string {
	if workspace == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(workspace, MemoryFile))
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

// memoryContext is the lessons as the prompts read them, under the project note. Empty when
// there are none.
func memoryContext(workspace string) string {
	lines := memoryLines(workspace)
	if len(lines) == 0 {
		return ""
	}
	text := "- " + strings.Join(lines, "\n- ")
	if len(text) > maxMemoryContext {
		text = text[len(text)-maxMemoryContext:]
	}
	return "\n\nWhat earlier tasks learned about this project (true unless the code says otherwise):\n" + text
}

// saveLessons keeps what a task's report says it learned, for the tasks after it.
func (a *Agent) saveLessons(report *Report) {
	if report == nil {
		return
	}
	if err := recordLessons(a.cfg.Agent.WorkspaceDir, report.Lessons); err != nil {
		a.log.Warn("could not save what the task learned", "error", err)
	}
}
