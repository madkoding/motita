package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

// THE RUN'S AGENTS, in the footer and in a panel.
//
// A run can start agents of its own in the background. Without a view of them the interface showed
// one spinner for all of it: a main agent waiting on three others looked exactly like a main agent
// that was stuck, and nothing said what any of it was costing. The agent reports the list as one
// progress line (agent.AgentsPrefix) whenever it changes; the interface keeps the latest and draws
// it two ways - a short count and token total in the footer, always, and the full list in a panel
// the user opens with Ctrl+G or /agents. The panel starts closed: the count is enough to know
// something is running, and a list that opens by itself would take the conversation's rows.

// keyAgents is Ctrl+G. It was free (Ctrl+F searches, Ctrl+U and Ctrl+D scroll) and it is handled
// inline, without a line: toggling a view must not throw away what the user is typing.
const keyAgents = 0x07

// Agent state glyphs. They are from the CP437 repertoire like every other glyph here, so a VGA
// console draws them: a running agent takes the spinner frame instead of a fixed glyph.
const (
	glyphPassed    = "√" // √
	glyphFailed    = "x"
	glyphCancelled = "-"
	glyphAgents    = "≡" // ≡ the agents segment in the footer
)

// takeAgents consumes a line that is a snapshot of the run's agents, and reports whether it was
// one. Every path a run reports through calls it first - a task, a plan, a followed run - so the
// snapshot never reaches the conversation as a line of JSON.
func (t *TUI) takeAgents(line string) bool {
	agents, ok := agent.ParseAgentsLine(line)
	if !ok {
		return false
	}
	t.draw.Lock()
	t.agents = agents
	t.draw.Unlock()
	return true
}

// toggleAgents opens or closes the agents panel.
func (t *TUI) toggleAgents() {
	t.draw.Lock()
	t.agentsOpen = !t.agentsOpen
	open, n := t.agentsOpen, len(t.agents)
	t.draw.Unlock()
	if open && n == 0 {
		t.addMessage(AuthorSystem, t.tr("No agents yet: a task lists here the agents it starts, with their time and tokens."))
	}
	t.drawFrame()
}

// timeNow is the clock the elapsed times are measured with. Tests pin it.
func (t *TUI) timeNow() time.Time {
	if t.clock != nil {
		return t.clock()
	}
	return time.Now()
}

// agentsLabel is the footer's agents segment: how many background agents are running and what
// the run has spent, or only the spend when the main agent works alone. Empty when nothing has
// been reported.
func (t *TUI) agentsLabel() string {
	var total int64
	running, subs := 0, 0
	for _, a := range t.agents {
		total += a.Tokens.Total()
		if a.Parent != "" {
			subs++
			if a.State == agent.AgentRunning {
				running++
			}
		}
	}
	switch {
	case subs > 0:
		return t.trf("%s %d running %s %s tok", glyphAgents, running, glyphMid, humanTokens(total))
	case total > 0:
		return humanTokens(total) + " tok"
	}
	return ""
}

// showingAgents reports whether the panel draws. It gives way to the completion popup: the user is
// typing a command, and the popup is what that keystroke is about.
func (t *TUI) showingAgents() bool { return t.agentsOpen && !t.completing() }

// agentsRows is how many rows the open panel asks for: a header and one row per agent, plus a
// detail row for each finished agent that left a branch or a summary.
func (t *TUI) agentsRows() int { return len(t.agentsLines(t.bodyWidth())) }

// agentsLinesCapped is the panel limited to max rows (zero meaning no limit). The header is kept
// and the OLDEST agents go first: the newest are the ones still working.
func (t *TUI) agentsLinesCapped(max int) []string {
	lines := t.agentsLines(t.bodyWidth())
	if max <= 0 || len(lines) <= max {
		return lines
	}
	return append(lines[:1], lines[len(lines)-max+1:]...)
}

// agentsLines draws the panel at the given width: one row per agent with its state, purpose,
// elapsed time, tokens and round, its latest activity after it, and, once it has finished, where
// its work is.
func (t *TUI) agentsLines(width int) []string {
	head := t.color(colAccent, 0, t.tr("Agents")) + t.muted(" "+glyphMid+" "+t.tr("Ctrl+G or /agents closes"))
	lines := []string{t.plainLine(clipLine(head, width))}
	if len(t.agents) == 0 {
		return append(lines, t.plainLine(t.muted(clipLine("  "+t.tr("none yet"), width))))
	}
	now := t.timeNow()
	for _, a := range t.agents {
		figures := elapsedText(a, now) + "  " + humanTokens(a.Tokens.Total()) + " tok"
		if a.Round > 0 {
			figures += "  r" + strconv.Itoa(a.Round)
		}
		purpose := a.Purpose
		if a.Parent == "" {
			purpose = t.tr("main") + " " + glyphMid + " " + purpose
		}
		// The figures are never cut: they are what the panel is for. The purpose takes what is
		// left, and the activity only what is left after that.
		room := width - 4 - visibleLen(figures)
		left := clipLine(purpose, room)
		if act := strings.TrimSpace(a.Activity); act != "" && room-visibleLen(left) > 6 {
			left += t.muted(" " + glyphMid + " " + clipLine(act, room-visibleLen(left)-3))
		}
		gap := room - visibleLen(left)
		if gap < 1 {
			gap = 1
		}
		lines = append(lines, t.plainLine(clipLine(t.agentGlyph(a)+" "+left+strings.Repeat(" ", gap)+" "+t.muted(figures), width)))
		if a.State != agent.AgentRunning {
			detail := strings.TrimSpace(strings.Join(nonEmpty(a.Branch, firstLine(a.Summary)), " "+glyphMid+" "))
			if detail != "" {
				lines = append(lines, t.plainLine(t.muted(clipLine("  "+detail, width))))
			}
		}
	}
	return lines
}

// agentGlyph marks an agent's state by shape, not only by colour.
func (t *TUI) agentGlyph(a agent.AgentInfo) string {
	switch a.State {
	case agent.AgentPassed:
		return t.color(colSuccess, 0, glyphPassed)
	case agent.AgentFailed:
		return t.color(colError, 0, glyphFailed)
	case agent.AgentCancelled:
		return t.muted(glyphCancelled)
	}
	return t.color(colAccent, 0, spinner[t.spin%len(spinner)])
}

// elapsedText is how long an agent ran, or has been running: counted from its start while it
// runs, so the panel's clock moves between snapshots, and fixed at its end once it finished.
func elapsedText(a agent.AgentInfo, now time.Time) string {
	d := time.Duration(a.ElapsedMS) * time.Millisecond
	switch {
	case a.State == agent.AgentRunning && !a.Started.IsZero():
		d = now.Sub(a.Started)
	case !a.Finished.IsZero() && !a.Started.IsZero():
		d = a.Finished.Sub(a.Started)
	}
	if d < 0 {
		d = 0
	}
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// humanTokens writes a token count the way a person reads one: 950, 48.2k, 1.3M.
func humanTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
	return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
}

// nonEmpty keeps the strings that say something.
func nonEmpty(items ...string) []string {
	var out []string
	for _, s := range items {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// firstLine is the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
