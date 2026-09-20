package tui

import (
	"fmt"
	"strings"
)

// Command is one slash command: what it is typed as, what it does, and whether it takes an
// argument.
//
// The catalogue is the single source of truth for three things that used to be written
// separately and drifted: the handler's switch, the help screen, and the completion popup.
// A command that appears in one and not the others is a promise the interface does not keep.
type Command struct {
	// Name is what the user types, with the slash: "/plan".
	Name string
	// Aliases are the other spellings the handler accepts.
	Aliases []string
	// Help is the one-line description shown in the completion popup.
	Help string
	// Arg is the placeholder for the argument, empty when the command takes none. It is what
	// the popup shows to teach the syntax.
	Arg string
}

// commands is every slash command, in the order the popup presents them: the ones that
// change mode first, then the ones that act, then the ones about the session, then leaving.
var commands = []Command{
	{Name: "/task", Aliases: []string{"/t"}, Help: "run a task in the sandbox"},
	{Name: "/plan", Aliases: []string{"/p"}, Help: "read-only mode: investigate and explain"},
	{Name: "/models", Aliases: []string{"/m"}, Help: "list the models the provider publishes"},
	{Name: "/config", Aliases: []string{"/c"}, Help: "first-run wizard: provider, model, check"},
	{Name: "/reasoning", Aliases: []string{"/r", "/think"}, Help: "cycle the reasoning level"},
	{Name: "/find", Aliases: []string{"/f"}, Help: "filter the conversation", Arg: "text"},
	{Name: "/session", Aliases: []string{"/s"}, Help: "context used, and any carried summary"},
	{Name: "/good", Help: "mark the last turn as good (moves skill value)", Arg: "note"},
	{Name: "/bad", Help: "mark the last turn as bad; the note says what to fix", Arg: "what was wrong"},
	{Name: "/value", Aliases: []string{"/v"}, Help: "what the library has learned, worst first"},
	{Name: "/new", Help: "start a new conversation"},
	{Name: "/help", Aliases: []string{"/h"}, Help: "this screen"},
	{Name: "/quit", Aliases: []string{"/q"}, Help: "leave"},
}

// completions returns the commands whose name or alias starts with the given prefix.
//
// A leading slash is expected and stripped if missing, so the function answers the same
// question whether it is called with what the user has typed ("/pl") or with a bare word.
// An empty completion list means the typed text is not a command prefix, and the caller then
// treats the line as ordinary input.
func completions(line string) []Command {
	prefix := strings.ToLower(strings.TrimSpace(line))
	if prefix == "" {
		return nil
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	// A line with a space is already an argument: the popup has done its job and must get
	// out of the way.
	if strings.ContainsAny(prefix, " \t") {
		return nil
	}

	var out []Command
	for _, c := range commands {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c)
			continue
		}
		for _, a := range c.Aliases {
			if strings.HasPrefix(a, prefix) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// completionLines renders the popup: one row per candidate, the name and its argument in the
// accent colour and the description muted, followed by how to accept.
//
// It is drawn ABOVE the composer rather than replacing the conversation, so the user keeps
// the context they are deciding in.
func (t *TUI) completionLines(w int) []string {
	return t.completionLinesCapped(w, 0)
}

// completionLinesCapped renders the popup with at most `cap` rows, so a long candidate list
// cannot take the frame past the bottom of the terminal. A cap of zero means "no limit", which
// is what a caller that has already measured the space wants.
//
// When the list is cut, the last row says how many are not shown: a silently truncated menu
// looks like the complete one, and the user would not know to keep typing.
func (t *TUI) completionLinesCapped(w, max int) []string {
	if !t.completing() {
		return nil
	}
	cands := completions(t.draft)
	// No second emptiness check: completing() above is exactly "not searching, and this same
	// call returned at least one candidate", so the list cannot be empty here. Testing it again
	// was dead code that read as a safety net.

	// Width of the name column: the longest name plus its argument, so the descriptions line
	// up. The guide is explicit that columnar data must align.
	nameWidth := 0
	for _, c := range cands {
		label := c.Name
		if c.Arg != "" {
			label += " " + c.Arg
		}
		if n := visibleLen(label); n > nameWidth {
			nameWidth = n
		}
	}

	// The cap counts EVERY row this function will return, including the optional "and N more" row
	// and the hint. Rendering a candidate and then adding two trailer rows is how a cap of one
	// came out as four, which quietly put the frame back past the bottom of the terminal — the
	// cap has to bound the result, not the candidate loop.
	showMore := false
	// How many rows the trailer costs. The hint is worth a row whenever there is one to spare;
	// below that it is dropped, because seeing WHICH command is offered matters more than being
	// told which key accepts it — and without dropping something a one-row popup is impossible,
	// which is what a very short terminal needs.
	trailer := 0
	showHint := max <= 0 || max >= 2
	if showHint {
		trailer++
	}

	shown := cands
	hidden := 0
	if max > 0 && len(cands) > max-trailer {
		// The cap counts every row returned, trailer included. The budget here is at least one
		// because the cap is never below one: the layout owns that floor, and duplicating it
		// would be a guard that cannot be reached.
		keep := max - trailer
		// The "and N more" line costs a row too, and only makes sense when a candidate survives
		// beside it — with a budget of one the candidate wins, because seeing WHICH command is
		// offered matters more than being told how many others there are.
		if keep > 1 {
			keep--
			showMore = true
		}
		hidden = len(cands) - keep
		shown = cands[:keep]
	}

	lines := make([]string, 0, len(shown)+2)
	for i, c := range shown {
		label := c.Name
		if c.Arg != "" {
			label += " " + c.Arg
		}
		pad := strings.Repeat(" ", nameWidth-visibleLen(label))
		row := t.color(colAccent, 0, label) + pad + "  " + t.muted(c.Help)
		if !t.fits(row, w-2*leftMargin) {
			row = t.color(colAccent, 0, label)
		}
		// The selected row carries an arrow marker so the user can see which row
		// the up and down keys will accept without having to count from the top.
		// The marker is drawn in the same accent colour as the candidate name, so
		// the popup reads as one accent block plus a single moving arrow, not two
		// competing highlights. The marker is added at the head of the row, so
		// plainLine still applies its own leftMargin and the popup stays aligned
		// with the conversation beside it.
		//
		// The marker is only painted when the highlighted row is one of the rows
		// that survived the cap. When the user has moved the highlight past the
		// end of the visible window, no row carries it — the popup is honest about
		// what it can show, and the next keystroke either scrolls the highlight
		// back into view or the popup shrinks enough to fit it.
		if i == t.completingIdx {
			lines = append(lines, t.plainLine(t.color(colAccent, 0, "›")+" "+row))
		} else {
			lines = append(lines, t.plainLine(row))
		}
		}
	if showMore {
		lines = append(lines, t.plainLine(t.muted(fmt.Sprintf("… and %d more", hidden))))
	}
	// The hint is dropped before a candidate is: knowing that a key completes the list is worth
	// less than seeing what it would complete to. It is also the row that makes a one-row popup
	// possible at all, which is what keeps the frame inside a very short terminal.
	if showHint {
		lines = append(lines, t.plainLine(t.muted("→ completes · Enter runs · Esc cancels")))
	}
	return lines
}

// completing reports whether the popup should be drawn: the user is typing a command and
// there is at least one candidate.
func (t *TUI) completing() bool {
	if t.searching {
		return false
	}
	return len(completions(t.draft)) > 0
}
