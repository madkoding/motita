package tui

import (
	"context"
	"fmt"
	"strings"
)

// Command is one slash command: what it is typed as, and what it means.
//
// The catalogue is the single source of truth for three things that used to be written
// separately and drifted: the handler, the help screen, and the completion popup. A command
// that appears in one and not the others is a promise the interface does not keep.
//
// The catalogue holds DATA only, and the actions live in commandActions below, keyed by the
// same Name. The split is not decoration: the help screen is GENERATED from this table, so a
// closure here that reached the help text would make `commands` depend on `helpText` and
// `helpText` on `commands` — the package would not compile. Keeping the two apart is what
// lets the help be generated rather than written twice.
type Command struct {
	// Name is what the user types, with the slash: "/plan". It is also the key into
	// commandActions — the two are held together by TestEveryCommandHasAnAction.
	Name string
	// Aliases are the other spellings the handler accepts, including the bare words
	// ("q", "quit") and the punctuation ("?") that predate the slash form.
	Aliases []string
	// Help is the one-line description shown in the completion popup.
	Help string
	// Arg is the placeholder for the argument, empty when the command takes none. It is what
	// the popup shows to teach the syntax.
	Arg string
	// Group is the category the completion popup groups by, used for colour coding.
	// "mode" = changes how the agent runs, "action" = does something, "session" = manages
	// the conversation, "meta" = help and quit.
	Group string
}

// commands is every slash command, in the order the popup presents them: the ones that
// change mode first, then the ones that act, then the ones about the session, then leaving.
var commands = []Command{
	{Name: "/task", Aliases: []string{"/t"}, Help: "Task mode: make changes and prove them with your check", Group: "mode"},
	{Name: "/plan", Aliases: []string{"/p"}, Help: "Plan mode: ask about the project, nothing is changed", Group: "mode"},
	{Name: "/models", Aliases: []string{"/m"}, Help: "list the provider's models, or switch to one", Arg: "[id]", Group: "mode"},
	{Name: "/config", Aliases: []string{"/c"}, Help: "run the setup again: provider, key, model, check", Group: "mode"},
	{Name: "/reasoning", Aliases: []string{"/r", "/think"}, Help: "how hard the model thinks: off, low, medium, high", Group: "mode"},
	{Name: "/find", Aliases: []string{"/f"}, Help: "filter the conversation", Arg: "text", Group: "action"},
	{Name: "/session", Aliases: []string{"/s"}, Help: "context used, and what was carried over", Group: "session"},
	{Name: "/sessions", Help: "list the conversations the gateway holds", Group: "session"},
	{Name: "/attach", Help: "switch to another conversation", Arg: "session id", Group: "session"},
	{Name: "/good", Help: "rate the last answer as good", Arg: "note", Group: "action"},
	{Name: "/bad", Help: "rate the last answer as bad; the note says what to fix", Arg: "what was wrong", Group: "action"},
	{Name: "/value", Aliases: []string{"/v"}, Help: "what motita has learned from your ratings", Group: "action"},
	{Name: "/new", Help: "start a fresh conversation", Group: "session"},
	{Name: "/update", Help: "install the newest motita release", Group: "action"},
	{Name: "/help", Aliases: []string{"/h", "h", "help", "?"}, Help: "every command and key", Group: "meta"},
	{Name: "/quit", Aliases: []string{"/q", "q", "quit"}, Help: "leave motita", Group: "meta"},
}

// Commands returns a copy of the catalogue so other packages (the gateway)
// can expose it through an endpoint without depending on the internal slice.
func Commands() []Command { return append([]Command(nil), commands...) }

// commandActions performs a command, keyed by the Name it belongs to. It reports whether the
// interface should quit.
//
// The argument is passed to every action rather than only to the ones that take one: the
// signature is the same for all of them, which is what lets a single table serve `/plan` and
// `/bad <note>` without a second, parallel list of "commands that carry an argument". Two
// lists meant two places to add a command, and the one that was forgotten did nothing.
var commandActions = map[string]func(t *TUI, ctx context.Context, arg string) bool{
	"/task": func(t *TUI, _ context.Context, _ string) bool { t.setScreen(ScreenTask); return false },
	"/plan": func(t *TUI, _ context.Context, _ string) bool { t.setScreen(ScreenPlan); return false },
	"/models": func(t *TUI, ctx context.Context, arg string) bool {
		// With an id it picks the model for this session, the way /reasoning picks the level:
		// in memory, from the next turn on.
		if model := strings.TrimSpace(arg); model != "" {
			t.Runner.SetLLM("", model)
			t.addMessage(AuthorSystem, fmt.Sprintf("model set to %s for this session", model))
			t.drawFrame()
			return false
		}
		t.setScreen(ScreenModels)
		t.runModels(ctx)
		return false
	},
	// The setup is something done and finished, not a place to stay: once it has run, the next
	// thing typed is a task with the new setup, so the interface goes back to Task.
	"/config": func(t *TUI, ctx context.Context, _ string) bool {
		t.setScreen(ScreenConfig)
		t.runConfig(ctx)
		t.setScreen(ScreenTask)
		return false
	},
	"/reasoning": func(t *TUI, _ context.Context, _ string) bool { t.cycleReasoning(); return false },
	// The typed alternative to Ctrl+F, and the one that works everywhere: a terminal in
	// canonical mode consumes control bytes itself (Ctrl+U is the driver's kill-line,
	// Ctrl+D its EOF), so the shortcut cannot be relied on. This goes through the ordinary
	// line reader, which is the same path a task takes.
	//
	// With an argument it applies the filter in one step; without one it opens the box.
	// applyFind already draws that distinction, so it is not repeated here.
	"/find": func(t *TUI, _ context.Context, arg string) bool { t.applyFind(arg); return false },
	// The session report: the window, what is in use, and what a compaction carried. A
	// conversation the user cannot inspect is one they cannot trust.
	"/session": func(t *TUI, _ context.Context, _ string) bool {
		t.addPreformatted(AuthorSystem, t.Runner.ConversationReport())
		return false
	},
	// The list of conversations, and which one this interface is on. It is how a user discovers
	// that the gateway holds more than one, and the ids the other command takes.
	"/sessions": func(t *TUI, ctx context.Context, _ string) bool { t.printSessions(ctx); return false },
	// Moving to another conversation, and READING it: coming back to a conversation means seeing
	// what happened while you were away, so the announcement is made by attachTo itself - one
	// place says what entering a session means, and a second message here would be a second
	// version of it.
	"/attach": func(t *TUI, ctx context.Context, arg string) bool {
		id := strings.TrimSpace(arg)
		if id == "" {
			t.addMessage(AuthorSystem, "usage: /attach <session id> — /sessions lists them.")
			return false
		}
		if err := t.attachTo(ctx, id); err != nil {
			// Reported in the chat rather than swallowed: silence here would leave the user
			// typing into a conversation they did not choose.
			t.addMessage(AuthorSystem, err.Error())
		}
		return false
	},
	"/good": func(t *TUI, _ context.Context, note string) bool { t.recordVerdict(true, note); return false },
	"/bad":  func(t *TUI, _ context.Context, note string) bool { t.recordVerdict(false, note); return false },
	// What the library has learned, worst first: the entries that need attention are the ones
	// at the top, and the complaints attached to them are what a fix is written from.
	"/value": func(t *TUI, _ context.Context, _ string) bool {
		t.addPreformatted(AuthorSystem, t.Runner.RewardReport())
		return false
	},
	// A new conversation is a clean screen: the old one is gone from the model's memory, and
	// leaving it on screen would suggest the next answer can still see it. The welcome screen says
	// what happened, the way it greets a first run.
	"/new": func(t *TUI, _ context.Context, _ string) bool {
		t.Runner.ResetConversation()
		t.messages = nil
		t.scroll = 0
		t.Notice = "Started a new conversation: the next message begins fresh."
		t.drawFrame()
		return false
	},
	// The gateway replaces its own binary and restarts, so the interface says what is
	// happening and then lets the user decide when to come back.
	"/update": func(t *TUI, ctx context.Context, _ string) bool { t.runUpdate(ctx); return false },
	"/help": func(t *TUI, _ context.Context, _ string) bool {
		t.addPreformatted(AuthorSystem, helpText)
		return false
	},
	"/quit": func(t *TUI, _ context.Context, _ string) bool { return true },
}

// recordVerdict writes the user's verdict on the turn that just ran.
//
// This is the only reward signal in the system, and it is the user's: the agent never rates
// its own work. The optional note on /bad is what makes the verdict actionable — a number
// says a skill failed, the note says which step was wrong, and that is the only form of the
// complaint a fix can be written from.
func (t *TUI) recordVerdict(ok bool, note string) {
	t.addPreformatted(AuthorSystem, t.Runner.RecordVerdict(ok, note))
}

// hasAlias reports whether a spelling is one of the command's aliases.
func (c Command) hasAlias(s string) bool {
	for _, a := range c.Aliases {
		if a == s {
			return true
		}
	}
	return false
}

// singleKeyBindings are the bare keys the interface acts on without Enter.
//
// They are NOT in the catalogue above: they carry no name the user could type as a
// command, they must not appear in the completion popup, and the help screen documents
// them in its navigation section instead. Keeping them out of the table is what stops
// "j" being advertised as a command that does not exist.
var singleKeyBindings = map[string]func(t *TUI){
	"tab": func(t *TUI) { t.nextScreen() },
	"j":   func(t *TUI) { t.scrollBy(+1) },
	"k":   func(t *TUI) { t.scrollBy(-1) },
	"g":   func(t *TUI) { t.scrollToTop() },
}

// completions returns the commands whose name or alias starts with what has been typed.
//
// Only a line that starts with "/" is a command being typed. A bare word used to be completed as
// if the slash were there, and Enter accepts the highlighted candidate - so a message that
// happened to be one word was turned into a command: "new" cleared the conversation, and "good",
// typed as the answer to the agent's question, was recorded as a rating of the last turn. The bare
// spellings a user DOES mean as commands ("q", "help", "?") are matched in full by the handler,
// with no popup needed. An empty list means the text is not a command, and the line is a message.
func completions(line string) []Command {
	prefix := strings.ToLower(strings.TrimSpace(line))
	if !strings.HasPrefix(prefix, "/") {
		return nil
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

	// The cap counts EVERY row this function will return, including the "and N more" row: a cap
	// that bounded only the candidates would let the trailer push the frame past the bottom of the
	// terminal. The keys that work in the popup are in the footer, so no row is spent on them here.
	showMore := false
	shown := cands
	hidden := 0
	if max > 0 && len(cands) > max {
		// The budget is at least one because the cap is never below one: the layout owns that
		// floor, and duplicating it would be a guard that cannot be reached.
		keep := max
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
		if !t.fits(row, w-2*leftMargin-2) {
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
		// The other rows are indented by the marker's width, so every name starts in the same
		// column whichever row is highlighted.
		if i == t.completingIdx {
			lines = append(lines, t.plainLine(t.color(colAccent, 0, "›")+" "+row))
		} else {
			lines = append(lines, t.plainLine("  "+row))
		}
	}
	if showMore {
		lines = append(lines, t.plainLine(t.muted(fmt.Sprintf("… and %d more, keep typing", hidden))))
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
