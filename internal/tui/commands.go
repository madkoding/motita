package tui

import "strings"

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

	lines := make([]string, 0, len(cands)+1)
	for _, c := range cands {
		label := c.Name
		if c.Arg != "" {
			label += " " + c.Arg
		}
		pad := strings.Repeat(" ", nameWidth-visibleLen(label))
		row := t.color(colAccent, 0, label) + pad + "  " + t.muted(c.Help)
		if !t.fits(row, w-2*leftMargin) {
			row = t.color(colAccent, 0, label)
		}
		lines = append(lines, t.plainLine(row))
	}
	lines = append(lines, t.plainLine(t.muted("Tab completes · Enter runs · Esc cancels")))
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
