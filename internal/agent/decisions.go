package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DecisionsFile is where a goal's decisions are kept, relative to the workspace. It lives in the
// project so the spec travels with the code, and it is plain Markdown so the user can edit or
// delete a line to overrule a decision.
const DecisionsFile = ".motita/decisions.md"

// maxDecisionsContext bounds how much of the file is handed back to the model.
const maxDecisionsContext = 4000

// recordDecisions appends what a goal decided. Nothing is written for a run that decided nothing.
func recordDecisions(workspace, goal string, ds []ReportDecision) error {
	if workspace == "" || len(ds) == 0 {
		return nil
	}
	path := filepath.Join(workspace, DecisionsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s — %s\n\n", time.Now().UTC().Format("2006-01-02 15:04"), oneLine(goal))
	for _, d := range ds {
		if d.Why != "" {
			fmt.Fprintf(&b, "- %s — %s\n", d.Decision, d.Why)
		} else {
			fmt.Fprintf(&b, "- %s\n", d.Decision)
		}
	}
	_, err = f.WriteString(b.String())
	return err
}

// decisionsContext is the standing spec handed to the next run, so a decision taken earlier is
// respected instead of re-made. Empty when there is none.
func decisionsContext(workspace string) string {
	text := ReadDecisions(workspace)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if len(text) > maxDecisionsContext {
		text = text[len(text)-maxDecisionsContext:]
	}
	return "\n\nDecisions already taken for this project (respect them unless the user says otherwise):\n" + text
}

// ReadDecisions returns the decisions file, or "" when there is none.
func ReadDecisions(workspace string) string {
	if workspace == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(workspace, DecisionsFile))
	if err != nil {
		return ""
	}
	return string(b)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// NumberedDecisions renders the decisions file with each decision numbered, so one can be named
// to RemoveDecision. The numbers run across the whole file, oldest first.
func NumberedDecisions(workspace string) string {
	text := ReadDecisions(workspace)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	n := 0
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.HasPrefix(line, "- ") {
			n++
			line = fmt.Sprintf("%d. %s", n, line[2:])
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// RemoveDecision deletes the n-th decision (1-based, as NumberedDecisions numbers them) and returns
// its text. A dated heading left with no decisions under it goes with it.
func RemoveDecision(workspace string, n int) (string, error) {
	text := ReadDecisions(workspace)
	lines := strings.Split(text, "\n")
	idx, count := -1, 0
	for i, l := range lines {
		if strings.HasPrefix(l, "- ") {
			count++
			if count == n {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("there is no decision %d (there are %d)", n, count)
	}
	removed := strings.TrimPrefix(lines[idx], "- ")
	lines = append(lines[:idx], lines[idx+1:]...)

	// Drop headings whose section is now empty.
	var kept []string
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") {
			empty := true
			for _, next := range lines[i+1:] {
				if strings.HasPrefix(next, "## ") {
					break
				}
				if strings.HasPrefix(next, "- ") {
					empty = false
					break
				}
			}
			if empty {
				continue
			}
		}
		kept = append(kept, l)
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	path := filepath.Join(workspace, DecisionsFile)
	if out == "" {
		return removed, os.Remove(path)
	}
	return removed, os.WriteFile(path, []byte("\n"+out+"\n"), 0o644)
}
