// Package tui implements the interactive text-based user interface.
//
// It is deliberately built with the Go standard library only: no termios, no
// curses, no raw mode. The screen is redrawn in full frames so the whole flow
// is testable with bytes.Buffer and portable to every target platform.
//
// The interface is a conversational chat: the user types tasks or prompts at
// the bottom and the agent answers above, showing what it is doing in plain
// English (or Spanish) instead of JSON log lines.
//
// Ctrl+C (SIGINT, SIGTERM) is handled through the context: the caller cancels
// the context and the TUI returns immediately.
package tui

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/approvals"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/i18n"
)

// Exit codes returned by the TUI.
const (
	ExitSuccess     = 0
	ExitError       = 1
	ExitInterrupted = 2
)

// Author identifies who wrote a chat line.
type Author int

const (
	AuthorUser Author = iota
	AuthorAgent
	AuthorSystem
)

func (a Author) String() string {
	switch a {
	case AuthorUser:
		return "you"
	case AuthorAgent:
		return "motita"
	case AuthorSystem:
		return "system"
	default:
		return "?"
	}
}

// Screen is one of the main views.
type Screen int

const (
	ScreenTask Screen = iota
	ScreenPlan
	ScreenModels
	ScreenConfig
	// ScreenAuto picks Plan or Task for each message from its wording (internal/intent). It is
	// last so the values of the screens before it do not move.
	ScreenAuto
)

func (s Screen) String() string {
	switch s {
	case ScreenTask:
		return "Task"
	case ScreenPlan:
		return "Plan"
	case ScreenModels:
		return "Models"
	case ScreenConfig:
		return "Config"
	case ScreenAuto:
		return "Auto"
	default:
		return "?"
	}
}

var screenOrder = []Screen{ScreenTask, ScreenPlan, ScreenModels, ScreenConfig}

// Message is one line in the conversation.
type Message struct {
	Author  Author
	Text    string
	Pending bool // true while the agent is still producing this line
	// Frozen marks a line that must never be written into again: a tool
	// announcement is an event that has already happened, and the output that
	// follows it belongs to the next block.
	Frozen bool
	// Preformatted marks text whose layout is part of its meaning: the help screen
	// is a two-column reference, and re-flowing it destroys the alignment that makes
	// it readable at a glance. Preformatted text is drawn line by line, exactly as
	// written (still clipped to the panel, never rewrapped).
	Preformatted bool
}

// TUI is the conversational terminal user interface.
type TUI struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Runner  Runner
	NoColor bool
	// ShowActions lists every command and action the agent runs; false (the default) shows only a counter.
	ShowActions bool

	// Lang is the language the interface is drawn in, resolved from ui.language and the
	// environment by whoever builds it. The zero value is English, the language every string is
	// written in; /language changes it while the interface runs.
	Lang i18n.Lang

	// Version is the build to name in the header box. WHICH build depends on how this interface
	// was started, and the caller decides: an interface speaking through a local gateway names
	// this program, and one attached to a gateway somebody else runs names THE GATEWAY - because a
	// client that prints its own version while talking to a different build is a confident lie.
	//
	// Empty draws no box at all, which is what an embedder that does not care gets for free.
	Version string

	// Setup runs the setup wizard for /config, and nil means the runner's own (RunConfig). It is a
	// field rather than a runner method because the wizard is the one thing that cannot be remote:
	// it reads the terminal in front of the user and writes the configuration of the machine they
	// are sitting at, so an interface speaking to a gateway is handed THIS process's wizard here,
	// while every other request still goes to the gateway through the runner, unwrapped.
	Setup func(context.Context) error

	// LiveInput asks for character-at-a-time editing even when the terminal mode could not be
	// changed: for an embedder that already put its terminal in that mode, and for the tests,
	// which drive the live editor through a pipe.
	LiveInput bool

	// Notice is shown at the top of the welcome screen until the first message: what just happened
	// before the interface opened, such as a setup that was completed. Empty shows nothing.
	Notice string

	// liveNotice is a line raised while the interface runs (a pull request's CI ended): shown in the
	// footer for a while. It is guarded by draw, like everything the painter reads.
	liveNotice string

	// Width and Height override the drawing area. Zero means "ask the
	// environment": tests set them to make the layout deterministic, and an
	// embedder can pin them to a fixed size.
	Width, Height int

	// lastFrame is the rows as they were last written, and paintedScreen says whether anything
	// has been written yet. Together they let a repaint write only what changed instead of the
	// whole frame, which is what made typing feel slow.
	//
	// They are only meaningful while draw is held: a frame is built and diffed under the same
	// lock, so the comparison cannot see a half-updated copy.
	lastFrame     []string
	paintedScreen bool

	screen Screen
	// autoTurn says the last message was routed by auto mode, which applies the needed
	// suggestions of its turn without asking (see openAskIfPending).
	autoTurn bool
	// lastPlan is the answer of the last plan turn, which a go-ahead typed next carries out as a
	// task; a task turn clears it (see submit).
	lastPlan string
	messages []Message
	// current is the turn in flight and nil when there is none; queued is the message typed while
	// it ran, sent when it ends. Both belong to the input loop's goroutine (see turns.go).
	current *turn
	queued  string
	// loopCtx is the context the input loop runs under, for turns started outside a key press.
	loopCtx context.Context
	// inbox receives the byte the one outstanding read produced, and reading says that read is
	// still waiting. There is never more than one: a second reader on the same input would race the
	// first for the next keystroke, and whichever lost would swallow it.
	inbox      chan byteRead
	reading    bool
	reader     *bufio.Reader
	cancelRun  context.CancelFunc
	runningCtx context.Context
	// prevSession is the session left last, where /back returns; "" until a session is left.
	prevSession string
	// othersWorking is how many OTHER sessions of the gateway have a run in flight, as last asked.
	// Guarded by draw: the poll beside the input loop writes it and the painter reads it.
	othersWorking int
	// following is the run followed on its own goroutine (followLiveRun), nil when there is none.
	// Guarded by draw, because that goroutine sets and clears it while the input loop reads it.
	following *follow
	// busy is true while a turn is in flight, which turns the status dot into a
	// spinner; spin is the animation frame advanced on every repaint.
	busy bool
	spin int
	// draw serialises painting. A frame is drawn from the input loop and from the
	// resize watcher, and both read the state the other mutates, so the whole paint
	// is held — rendering outside the lock and writing inside it would still race on
	// Width, scroll and messages. -race caught exactly that.
	draw sync.Mutex
	// ask is the window the agent's questions are answered in, and nil when there is nothing
	// being asked. It is view state for a turn in progress: it opens when the agent cannot read
	// a request and closes when the answers are sent.
	ask *askState

	// confirm is the window a consequential command is approved in, and nil when nothing is
	// being confirmed. Unlike the questions window it is answered DURING a run: the agent is
	// blocked waiting for the answer while the run loop reads the keys.
	confirm *confirmState
	// approvals is how a blocked agent reaches this loop.
	//
	// It is created once, lazily, and the Once is not tidiness: this field is read from TWO
	// goroutines at once — the run loop is already receiving on the channel when the agent, in
	// its own goroutine, sends the first request. A bare `if nil` check is a data race between
	// them, and the damage is worse than a torn read: each caller can come away holding a
	// DIFFERENT channel, so the run loop waits on one nobody will ever send to while the agent
	// waits for an answer. The turn hangs, the confirmation window never opens, and the user
	// sees the agent stop responding. -race caught exactly that.
	//
	// Lazy rather than built in New because the tests construct a TUI as a struct literal — the
	// many small ones that never run a real turn never need a channel, and requiring one would
	// make every one of them wrong for no reason.
	approvalsOnce sync.Once
	approvals     chan *confirmState
	// remembered is the file of command shapes the person said to always allow here, opened
	// once, the first time a command asks. See alwaysallow.go.
	rememberedOnce sync.Once
	remembered     *approvals.Store

	// query filters the conversation; searching is true while the user is typing it.
	//
	// Both are view state: they describe what is being looked at, not what the session
	// contains, so neither is persisted and neither survives a new turn's scroll reset.
	query     string
	searching bool
	// painted records that the first frame has been drawn, so the screen is wiped once at
	// launch and never again.
	painted bool
	// charMode is true while the terminal delivers one byte at a time, which is what makes
	// live editing possible. It is set from the mode the run obtained, so a terminal that
	// refused it keeps the whole-line path.
	charMode bool
	// completingIdx is the row the popup is highlighting. It moves up and down with the
	// arrow keys while the popup is open, so the user can pick a candidate without typing
	// the whole name. Zero is the first row, which is also the row Tab and the right
	// arrow used to fill — keeping that as the default is what makes the existing gesture
	// keep working for the user who never reaches for an arrow.
	completingIdx int
	// termMode is the handle the run obtained on the controlling terminal. The wizard
	// needs to step the terminal back to cooked so it can read whole lines, and then put
	// it back into the same mode when it returns — both moves go through this handle.
	termMode *terminalMode
	// draft is the line being typed. With the terminal in character mode the interface owns
	// the editing, which is what makes a live completion popup possible: the candidate list
	// has to know what has been typed so far.
	draft string
	// scroll is how many rows the conversation is lifted above its newest line.
	// Zero means "pinned to the bottom", which is where a chat belongs: new
	// output arrives at the end. Raising it walks back through history, which is
	// the only way to read a long answer on a fixed-height screen.
	//
	// It is transient view state, deliberately not persisted: it describes where
	// the window is, not what the session contains.
	scroll int

	// agents is the latest snapshot of the run's agents, and agentsOpen whether their panel is
	// showing (see agents.go). Both are read while painting, so they are written under draw.
	agents     []agent.AgentInfo
	agentsOpen bool
	// clock is what elapsed times are measured against; nil is the wall clock. Tests pin it.
	clock func() time.Time

	// The git login seams (git.go): the store of logins, the browser opener and the line reader
	// a login prompts with. Nil means the real one. gitNudged is set once the startup nudge has
	// been decided, and gitNudge turns the nudge on (New does; a bare struct literal stays quiet).
	gitStore  func() gitforge.Store
	openURL   func(string) error
	gitLine   func(ctx context.Context, secret bool) (string, bool)
	gitNudge  bool
	gitNudged bool
	// prompting is true while a git login reads a line; maskInput hides what is typed (a token).
	prompting, maskInput bool
}

// New creates a TUI with sensible defaults for production use.
func New(runner Runner) *TUI {
	return &TUI{
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
		Runner: runner,
		screen: ScreenAuto,

		gitNudge: true,
	}
}

// Run displays the chat and dispatches user input until the user quits or the
// context is cancelled.
func (t *TUI) Run(ctx context.Context) int {
	t.loopCtx = ctx
	t.drawFrame()

	// The confirmation channel is installed before anything can run: a turn that proposes a
	// consequential command has to be able to reach the user, and an interface that installs
	// this after the first turn would refuse the commands of that turn instead of asking.
	t.installApprover()

	// Leaving wipes the screen.
	//
	// A full-screen interface that exits and leaves its frame behind hands the shell back a
	// window covered in text that is not the user's: their prompt is somewhere above it, and the
	// first thing they have to do is clear it. The interface took the screen over when it
	// started — it cleared on the way in — so it has to give it back on the way out.
	//
	// It is a defer, and it is the FIRST one registered, so it runs LAST: the mouse, the
	// terminal mode and the background painter are all restored before the screen is wiped, and
	// the wipe is therefore the final thing written.
	//
	// It runs on every exit path — quit, Ctrl+C, a cancelled context, a closed input, a panic
	// that unwinds through here — because a cleanup attached to one of them is a cleanup that
	// leaks on the others.
	defer t.clearOnExit()

	// A resize repaints at the new geometry. The read below cannot be interrupted by
	// a signal — the terminal is not in raw mode, so ReadByte blocks until a line
	// arrives — which is why this needs its own goroutine rather than a check in the
	// loop.
	//
	// The painter is waited for on the way out: stop closes the notification channel,
	// the loop ends, and only then does Run return. A paint still in flight would
	// otherwise write a frame over the shell prompt after the interface had exited.
	// The mouse is requested for the duration of the run and released on the way out. A
	// terminal left in reporting mode would send movement events to the shell after the
	// program exits, which is the same class of rudeness as leaving the cursor hidden.
	t.enableMouse()
	defer t.disableMouse()

	// Character-at-a-time input for the duration of the run, so a keystroke is seen as it is
	// typed instead of at the end of the line. The restore runs from a defer and is
	// idempotent; the panic path below reuses it.
	//
	// It degrades rather than failing: a terminal that refuses the mode leaves the interface
	// reading whole lines, which is the behaviour it had before, and the completion popup
	// simply does not appear.
	mode := enterRaw()
	t.charMode = mode.active || t.LiveInput
	t.termMode = mode
	// Both defers read t.termMode when they run, not now: /config swaps in a new handle, and
	// that one is the handle to undo.
	defer func() { t.termMode.restore() }()
	defer recoverRaw(&t.termMode)()

	// The nudge to connect a git host: local files only, so it can be made before the loop.
	if t.gitNudge {
		t.announceGit()
	}

	// The offer to upgrade is made beside the loop, never before it: the check can wait on the
	// network, and the interface must open without it. It is waited for on the way out, before
	// the screen is wiped, for the reason the painter below is.
	if _, ok := t.Runner.(updater); ok {
		defer beside(ctx, t.announceUpdate)()
	}

	// The CI of a session's pull request is followed by the gateway; the notices are raised here.
	if _, ok := t.Runner.(SessionSwitcher); ok {
		defer beside(ctx, t.watchPRs)()
	}

	defer t.paintOnResize()()

	for {
		line, ok := t.readLine(ctx)
		if !ok {
			// A turn still running is let finish (a closed input is a script that piped its
			// task and is waiting for the answer), or has been stopped by the same cancellation
			// that ended the read.
			t.finishTurn(ctx)
			if ctx.Err() != nil {
				return ExitInterrupted
			}
			return ExitSuccess
		}
		if t.dispatchLine(ctx, line) {
			return ExitSuccess
		}
	}
}

// beside runs work in a goroutine, with a context of its own under ctx, and returns the function
// that cancels it and waits for it to end.
func beside(ctx context.Context, work func(context.Context)) func() {
	workCtx, stop := context.WithCancel(ctx)
	var running sync.WaitGroup
	running.Add(1)
	go func() {
		defer running.Done()
		work(workCtx)
	}()
	return func() {
		stop()
		running.Wait()
	}
}

// paintOnResize repaints the frame each time the terminal changes size, and returns the function
// that stops watching and waits for the painter.
func (t *TUI) paintOnResize() func() {
	resized, stopWatch := watchResize()
	var painting sync.WaitGroup
	painting.Add(1)
	go func() {
		defer painting.Done()
		for range resized {
			t.drawFrame()
		}
	}()
	return func() {
		stopWatch()
		painting.Wait()
	}
}

// dispatchLine hands a line read from the input to whatever owns the input right now. It reports
// whether the user asked to quit.
func (t *TUI) dispatchLine(ctx context.Context, line string) bool {
	// While a command waits for approval the keys answer it. This is only reached in
	// character mode: without it the window is answered where the turn is awaited.
	if t.answeringConfirm() {
		t.handleConfirmLine(line)
		return false
	}

	// While the search is open the input belongs to it: a line typed there is a
	// query, not a task, and running it would be a surprise nobody asked for.
	if t.searching {
		t.applyQuery(line)
		return false
	}

	// While the agent's questions are open the input belongs to THEM, and it is checked
	// before the shortcuts for the same reason the search is: a digit or an arrow typed
	// here is an answer, not a task to run.
	if t.asking() && t.handleAskKey(ctx, line) {
		return false
	}

	// Global shortcuts are checked before interpreting the line as chat.
	if handled, quit := t.handleShortcut(ctx, line); handled {
		if quit {
			t.leaveTurn(ctx)
		}
		return quit
	}

	// A message typed while the agent works waits for it, instead of being lost or starting a
	// second turn over the first.
	if t.current != nil && strings.TrimSpace(line) != "" {
		t.queue(line)
		return false
	}

	t.runLine(ctx, line)
	return false
}

// runLine runs what was typed on the current screen. On the Models and Setup screens an empty
// line runs the screen's action (refresh the catalogue, run the setup), and anything typed is a
// task: see submit.
func (t *TUI) runLine(ctx context.Context, line string) {
	switch {
	case t.screen == ScreenModels && strings.TrimSpace(line) == "":
		t.runModels(ctx)
	case t.screen == ScreenConfig && strings.TrimSpace(line) == "":
		t.runConfig(ctx)
	default:
		t.submit(ctx, line)
	}
}

// handleShortcut interprets command-like input and view-switching keys.
// It returns (handled, shouldQuit).
//
// It is two layers, because the interface really has two: keys the TERMINAL sends
// (escape sequences, control bytes, the mouse) which are matched against the raw
// line, and NAMES the user types, which are matched against a trimmed, lowercased
// copy. Mixing them in one switch is what made the raw cases fragile — trimming a
// control byte deletes it, so a normalising switch can never see Tab.
func (t *TUI) handleShortcut(ctx context.Context, line string) (bool, bool) {
	if handled, quit := t.handleRawKey(line); handled {
		return true, quit
	}
	return t.handleTypedCommand(ctx, line)
}

// pressEscape is the universal way out, and it leaves in the reverse order of how the interface
// was entered: the search first, then a run, then the scroll.
func (t *TUI) pressEscape() {
	if t.searching || t.query != "" {
		t.closeSearch()
		return
	}
	if cancel := t.currentCancel(); cancel != nil {
		cancel()
		return
	}
	t.scrollToBottom()
}

// handleRawKey answers the keys the terminal reports as bytes: control characters,
// escape sequences and mouse reports. None of them is something a user typed as
// text, so a key the interface does not use is CONSUMED rather than passed on —
// otherwise it reaches the chat and is sent to the model.
func (t *TUI) handleRawKey(line string) (bool, bool) {
	switch line {
	case "	", "tab":
		// Tab is special: TrimSpace deletes it, so it can only be seen here. The
		// "tab" spelling is matched in the typed switch below.
		t.nextScreen()
		return true, false
	case keyEsc:
		t.pressEscape()
		return true, false
	case keyUp:
		t.scrollBy(+1)
		return true, false
	case keyDown:
		t.scrollBy(-1)
		return true, false
	case keyPgUp:
		t.scrollBy(t.chatRows())
		return true, false
	case keyPgDn:
		t.scrollBy(-t.chatRows())
		return true, false
	case keyHome:
		t.scrollToTop()
		return true, false
	case keyEnd:
		t.scrollToBottom()
		return true, false
	case keyHalfUp:
		t.scrollBy(t.chatRows() / 2)
		return true, false
	case keyHalfDown:
		t.scrollBy(-t.chatRows() / 2)
		return true, false
	case keyFind:
		t.openSearch()
		return true, false
	default:
		// Not a named key: the control bytes and the mouse are checked below.
	}

	// The remaining control bytes are not text and must not be sent. This is the belt to the
	// reader's braces: the reader refuses to build a line containing one, and this refuses to
	// dispatch one that arrived by some other path.
	//
	// That is exactly what happened to Ctrl+D, Ctrl+F and Ctrl+U: the reader captured them from
	// the input and returned them as tokens, the interface had no case for them, and they were
	// dispatched as a chat message — so the model received control characters and reported them.
	if isStrayControlByte(line) {
		return true, false
	}

	// A mouse report is a CSI sequence like any other, so it arrives here intact. The
	// wheel scrolls; anything else the terminal reports (a click, a drag, a release, a move) is
	// CONSUMED and ignored, because the keyboard is the interface and the mouse is an addition to
	// it.
	//
	// Consuming every report, not just the wheel, is what stopped the mouse from typing: a click
	// or a drag fell through this and every other handler, so it reached the chat as a line of
	// escape-sequence text. The terminal was asked to send these events; an unhandled one is not
	// something the user typed.
	if lines, ok := mouseScroll(line); ok {
		t.scrollBy(lines)
		return true, false
	}
	if isMouseReport(line) {
		return true, false
	}

	// Any OTHER escape sequence is a KEY, not a message.
	//
	// This is the catch-all that was missing. The cases above name the keys the interface
	// acts on, and everything else — the right and left arrows, Delete, F1, Shift+Tab, and every
	// other sequence a terminal can send — fell through to the dispatch and was sent as a chat
	// message. Pressing an arrow SENT A MESSAGE: the user saw their own input submitted as if
	// they had pressed Enter.
	//
	// A sequence introduced by ESC is never something a user typed: text does not contain
	// escape sequences, they are how the terminal reports a key. So the rule is stated once, by
	// shape, rather than by listing every sequence that exists — the list can only ever be
	// incomplete, and what is missing from it becomes a message sent by accident.
	if strings.HasPrefix(line, "\x1b") {
		return true, false
	}

	return false, false
}

// isStrayControlByte reports whether the line is a single control byte with no
// meaning of its own. The tokens that DO have a meaning — Ctrl+D, Ctrl+F, Ctrl+U —
// are matched before this and are deliberately not listed: an interface that both
// acts on a key and swallows it would be saying two things about the same byte.
func isStrayControlByte(line string) bool {
	if len(line) != 1 {
		return false
	}
	c := line[0]
	if c == 0x7f {
		return true
	}
	return c < 0x20 && c != '	' && c != '\r' && c != '\n'
}

// handleTypedCommand answers a NAME the user typed: a slash command, a single-letter binding,
// or a command followed by an argument.
//
// The names live in ONE table (see commands.go). With a hand-written switch the same name had
// to be kept in step with the completion popup and the help screen by hand, and a command that
// existed in two of the three was a promise the interface did not keep.
func (t *TUI) handleTypedCommand(ctx context.Context, line string) (bool, bool) {
	// The line is normalised to lowercase as a whole, so a typed "Tab", "Q" or "/PLAN" reaches
	// the same entry as its lowercase spelling. That is what the interface already did for
	// command names, and it is the only reason a spelled-out key name works at all.
	trimmed := strings.TrimSpace(strings.ToLower(line))

	// Shift+G is checked against the raw line, before the lowercasing above: the table is
	// keyed on a lowercased copy, so "G" could never reach its own entry and the binding would
	// be dead. The convention is worth the extra check — "g for the top, G for the bottom" is
	// what a vim user's fingers expect.
	if line == "G" {
		t.scrollToBottom()
		return true, false
	}

	if run, ok := singleKeyBindings[trimmed]; ok {
		run(t)
		return true, false
	}

	// The NAME is everything up to the first space; the rest is the argument. Splitting once,
	// here, is what lets a single table hold both "/plan" and "/bad <note>" — and it is why an
	// unknown name is refused instead of being matched by prefix against every entry.
	name, arg := trimmed, ""
	if i := strings.IndexAny(trimmed, " 	"); i >= 0 {
		name, arg = trimmed[:i], strings.TrimSpace(trimmed[i+1:])
	}

	// The argument is taken from the RAW line when the name matches, so it keeps the user's
	// own capitalisation and quotes: it is their words, and /bad quotes them back verbatim.
	if raw := strings.TrimSpace(line); arg != "" {
		if i := strings.IndexAny(raw, " 	"); i >= 0 {
			arg = strings.TrimSpace(raw[i+1:])
		}
	}

	for _, c := range commands {
		if c.Name != name && !c.hasAlias(name) {
			continue
		}
		if t.refuseWhileBusy(c.Name) {
			return true, false
		}
		run, ok := commandActions[c.Name]
		if !ok {
			// Unreachable while TestEveryCommandHasAnAction passes, but it is ANSWERED here
			// rather than left to fall through: falling through would dispatch the name to
			// the MODEL. The user typed a command the popup and the help screen advertised,
			// so it was never a question, and sending it on would spend a turn on a line
			// nobody asked. A menu entry that says it is broken is better than one that
			// becomes a task by accident.
			t.addMessage(AuthorSystem, t.trf("the command %s is listed but has no implementation.", c.Name))
			return true, false
		}
		return true, run(t, ctx, arg)
	}

	// A line that matches no command and no binding is CONTENT, not an error: in Task and Plan
	// it is what the user wants sent to the model, and that is a documented contract
	// (TestUnknownCommandInAConversationViewIsATask). Being a slash-prefixed line does not
	// change that — the interface has no reserved word, and refusing "/unknown" here would
	// break the very case the contract exists for.
	return false, false
}

// applyFind filters the conversation in one step, without leaving the search box open.
// It is the typed form of the search: the filter is applied and the box is closed, so
// the next line typed is a task again.
func (t *TUI) applyFind(query string) {
	if query == "" {
		t.openSearch()
		return
	}
	t.query = query
	t.searching = false
	t.scroll = 0
	t.drawFrame()
}

// openSearch puts the interface into search mode. It is a mode rather than a prefix
// argument because the query is built a character at a time and the result is visible
// while it is typed.
func (t *TUI) openSearch() {
	t.searching = true
	t.scroll = 0 // a new filter is read from its start
	t.drawFrame()
}

// applyQuery updates the filter with a line of input.
//
// An empty line closes the search and keeps the filter: pressing Enter on an empty
// query means "leave it as it is", which is how a reader confirms a filter they are
// happy with. Esc is the way to clear it.
func (t *TUI) applyQuery(line string) {
	if strings.TrimSpace(line) == "" {
		t.searching = false
		t.drawFrame()
		return
	}
	t.query = strings.TrimSpace(line)
	t.scroll = 0
	t.drawFrame()
}

// closeSearch leaves search mode and clears the filter, restoring the whole
// conversation. Leaving a filter applied with no visible sign of it would hide the
// user's own history from them.
func (t *TUI) closeSearch() {
	t.searching = false
	t.query = ""
	t.scroll = 0
	t.drawFrame()
}

// matchingMessages is the conversation narrowed by the active query, or all of it when
// there is none. Matching is case-insensitive and covers the speaker as well as the
// text, so "you" finds the user's turns.
func (t *TUI) matchingMessages() []Message {
	if t.query == "" {
		return t.visibleMessages()
	}
	needle := strings.ToLower(t.query)
	var out []Message
	for _, m := range t.visibleMessages() {
		if strings.Contains(strings.ToLower(m.Text), needle) ||
			strings.Contains(strings.ToLower(m.Author.String()), needle) {
			out = append(out, m)
		}
	}
	return out
}

// chatRows is the distance a page key moves: the number of conversation rows the
// frame can currently show.
//
// It is computed from the same shedding rules the layout applies, but WITHOUT
// calling the layout: that would recurse, since the layout reads the scroll
// offset this paging changes. The arithmetic below mirrors the fixed parts of the
// frame and the droppable ones, so a page matches what is on screen.
func (t *TUI) chatRows() int {
	_, h := t.size()
	if h <= 0 {
		// The terminal did not report a height, so there is no page to speak of.
		return minChatLines
	}
	// No branch for a terminal below the size gate: the gate refuses to draw at all there, so
	// this helper is never reached with such a height. A branch for it would be unreachable, and
	// an unreachable branch reads as a safeguard while testing nothing.
	//
	// The fixed rows are counted in ONE place (permanentRows), so this cannot drift from what
	// the layout actually draws. It used to carry its own "8", written when the frame had a
	// different shape; a stale count here is a scrollbar that lies about how much is visible.
	room := h - permanentRows
	if room < minChatLines {
		return minChatLines
	}
	return room
}

// scrollBy moves the view. Scrolling up is clamped where the layout clamps it —
// at the oldest line that can still be shown — and scrolling down stops at the
// newest one, so the view can never be left floating in empty space.
func (t *TUI) scrollBy(delta int) {
	t.scroll += delta
	if t.scroll < 0 {
		t.scroll = 0
	}
	if max := t.maxScroll(); t.scroll > max {
		t.scroll = max
	}
	t.drawFrame()
}

func (t *TUI) scrollToTop() {
	t.scroll = t.maxScroll()
	t.drawFrame()
}

func (t *TUI) scrollToBottom() {
	t.scroll = 0
	t.drawFrame()
}

// maxScroll is the furthest back the view can go: as far as the oldest line that
// the window can actually show.
//
// It is the conversation length minus the rows on screen, NOT one less than the
// whole body. Offsetting by the whole body would lift the newest line off the
// bottom and, on a conversation short enough to fit, would hide the only message
// there is — the user would press "go to top" and watch their text disappear.
// When everything already fits there is nowhere to go, which is why this is zero.
func (t *TUI) maxScroll() int {
	total := len(t.bodyLines())
	if room := t.chatRows(); total > room {
		return total - room
	}
	return 0
}

// bodyLines is the conversation as it would be drawn with no trimming and no
// scroll. It is the coordinate space the scroll offset moves through.
func (t *TUI) bodyLines() []string {
	return t.chatLines(t.inner())
}

// nextScreen switches between Task and Plan.
//
// Exactly those two, and only those two. Tab used to walk the whole screen order, which also
// contains the model list and the configuration wizard: pressing it twice to get back where you
// started landed on a different screen instead, and the two screens a user actually toggles
// while working were buried in a four-stop cycle. The other two are still reachable by their
// commands (/models, /config), which is where they belong — they are things you go to on
// purpose, not things you rotate through by accident.
//
// Auto mode joined the cycle as its third stop: Task, Plan, Auto, and back to Task. Anything
// else goes to Plan, so a stray screen never traps the toggle.
func (t *TUI) nextScreen() {
	switch t.screen {
	case ScreenPlan:
		t.setScreen(ScreenAuto)
	case ScreenAuto:
		t.setScreen(ScreenTask)
	default:
		t.setScreen(ScreenPlan)
	}
}

func (t *TUI) setScreen(s Screen) {
	t.screen = s
	t.drawFrame()
}

func (t *TUI) cycleReasoning() {
	levels := []string{"off", "low", "medium", "high"}
	current := strings.ToLower(t.Runner.Config().LLM.Reasoning.Level)
	if current == "" {
		current = "medium"
	}
	nextIdx := 0
	for i, l := range levels {
		if l == current {
			nextIdx = (i + 1) % len(levels)
			break
		}
	}
	next := levels[nextIdx]
	t.Runner.SetReasoning(next)
	t.addMessage(AuthorSystem, t.trf("reasoning set to %s", next))
	t.drawFrame()
}

// addMessage appends a chat line and repaints.
func (t *TUI) addMessage(author Author, text string) {
	t.messages = append(t.messages, Message{Author: author, Text: text})
	t.drawFrame()
}

// addPreformatted appends a message whose own layout carries meaning, so it is drawn
// as written instead of being word wrapped. The help screen is a key reference: the
// alignment between a key and its description is what makes it scannable, and
// wrapping collapses the runs of spaces that produce it.
//
// A reference is read from its TOP. When it is taller than the window, the view is lifted so its
// first line is the first one on screen: pinned to the bottom like an answer, the help screen
// opened on its last command and its first half was never seen.
func (t *TUI) addPreformatted(author Author, text string) {
	m := Message{Author: author, Text: text, Preformatted: true}
	t.messages = append(t.messages, m)
	inner := t.conversationWidth()
	tall := len(t.messageLines(m, inner))
	start := len(t.chatLines(inner)) - tall
	lift := tall - t.chatRows()
	if start > 0 {
		lift++ // the "earlier lines" marker takes the top row once anything is above
	}
	if lift > 0 {
		t.scroll = lift
	}
	t.drawFrame()
}

// beginTurn marks the interface as busy and repaints, so the status line shows a
// spinner for the whole duration of a turn instead of a static dot.
//
// A new turn also returns the view to the bottom: starting a task is a request to
// see its output, so the window follows the newest line again even if the user was
// reading history a moment ago.
func (t *TUI) beginTurn() {
	t.draw.Lock()
	t.busy = true
	t.spin++
	t.draw.Unlock()
	t.scroll = 0
}

// endTurn clears the busy flag and repaints the finished conversation.
func (t *TUI) endTurn() {
	t.draw.Lock()
	t.busy = false
	t.draw.Unlock()
	t.drawFrame()
}

// setCancel installs the cancel func a key stops the turn with.
//
// It exists because the fields are read from the INPUT LOOP - Escape and Ctrl+C - while a run that
// was FOLLOWED is driven from its own goroutine. Writing them directly would be a data race on the
// one path the user has to reach when a turn will not stop, and -race catches it.
func (t *TUI) setCancel(cancel context.CancelFunc, runCtx context.Context) {
	t.draw.Lock()
	defer t.draw.Unlock()
	t.cancelRun = cancel
	t.runningCtx = runCtx
}

// clearCancel releases the cancel func once the turn is over, so Escape goes back to meaning
// "scroll to the newest line".
func (t *TUI) clearCancel() {
	t.setCancel(nil, nil)
}

// currentCancel returns the cancel func of the turn in flight, or nil when there is none.
//
// The read is under the lock because the writer may be a followed run's own goroutine: the fields
// are shared between that goroutine and the input loop, which is the loop that has to reach them.
func (t *TUI) currentCancel() context.CancelFunc {
	t.draw.Lock()
	defer t.draw.Unlock()
	return t.cancelRun
}

// advance repaints while a turn is running, which animates the spinner. It is
// called from the progress loop, so a slow model still shows movement.
//
// New output never moves a view the user lifted: the offset counts rows above the
// newest line, so growing the conversation underneath leaves the window where it
// is — the reader keeps the line they were on while the answer grows below. Only
// an explicit jump (a new turn, g/G, End) returns to the bottom.
func (t *TUI) advance() {
	// The spinner frame is read by drawFrame under t.draw (the progress loop and the
	// run goroutine paint concurrently), so it is advanced under the same lock.
	t.draw.Lock()
	if t.busy {
		t.spin++
	}
	t.draw.Unlock()
	t.drawFrame()
}

// helpText is the help screen, generated from the command catalogue.
//
// It is built rather than written so the screen cannot drift from what the handler accepts:
// the previous version was a second copy of the same list, and it had already gone out of
// step — it advertised "/t task" while the handler and the popup knew "/task". A help screen
// that documents a spelling nothing accepts is worse than no help at all.
var helpText = buildHelp(i18n.EN)

// helpSections names the catalogue's groups as the help screen titles them, in the order they are
// shown. The group values themselves are shared with the browser interface, which colours by them,
// so the friendly titles live here rather than in the catalogue.
var helpSections = [][2]string{
	{"mode", "Modes and setup"},
	{"session", "Conversations"},
	{"action", "Search and feedback"},
	{"meta", "Help"},
}

// buildHelp renders the reference: the keys first, because they are what a newcomer needs, then
// every command under its topic, with the description the catalogue carries.
func buildHelp(lang i18n.Lang) string {
	tr := func(s string) string { return i18n.T(lang, s) }
	var b strings.Builder
	b.WriteString(tr("Keys (no Enter needed)") + "\n")
	for _, h := range [][2]string{
		{"Enter", "send what you typed"},
		{"Tab", "switch between Task and Plan"},
		{"/", "open the command list (↑↓ choose, → complete)"},
		{"↑↓ PgUp PgDn", "scroll the conversation (also j/k, Ctrl+U/Ctrl+D)"},
		{"g / G", "oldest / newest"},
		{"Ctrl+F", "search the conversation (or /find)"},
		{"Ctrl+G", "show or hide the run's agents (or /agents)"},
		{"Esc", "close / stop the running task / back to the newest"},
		{"Ctrl+C", "stop and quit"},
	} {
		b.WriteString("  " + pad(h[0], 16) + tr(h[1]) + "\n")
	}

	for _, sec := range helpSections {
		b.WriteString("\n" + tr(sec[1]) + "\n")
		for _, c := range commands {
			if c.Group != sec[0] {
				continue
			}
			label := c.Name
			if len(c.Aliases) > 0 {
				label += " (" + strings.Join(c.Aliases, ", ") + ")"
			}
			if c.Arg != "" {
				label += " " + tr(c.Arg)
			}
			b.WriteString("  " + pad(label, 24) + tr(c.Help) + "\n")
		}
	}
	return b.String()
}

// pad pads a plain string to a column, for the help's two-column layout. The width is counted
// in runes: the key names include arrows, and counting their bytes would push every description
// after them out of its column.
func pad(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s + " "
	}
	return s + strings.Repeat(" ", width-n)
}

// minHeight is the number of rows below which the interface stops trying to draw
// a frame. The guide is explicit: below a workable size, show a message instead
// of a broken layout. Trying to fit anyway produces a frame whose every part has
// been dropped — the worst of both worlds, since the user cannot read it and
// cannot tell why.
// It is DERIVED from the parts the frame always draws, not chosen: two rules, the composer, the
// status line and the bar are permanent, and a conversation of fewer than minChatLines rows is
// not a conversation. A hand-picked 8 was two rows short of that, so the smallest terminal the
// gate accepted could not hold the frame it then drew — the frame came out taller than the
// window and scrolled.
const minHeight = permanentRows + minChatLines

// tooSmallLines is the whole screen when the terminal cannot hold the interface.
// It says the size it needs and the size it has, so the user can fix it instead
// of guessing.
func (t *TUI) tooSmallLines(w, h int) []string {
	msg := []string{
		"",
		"  " + t.color(colWarning, 0, t.tr("The window is too small to draw Motita.")),
		"",
		"  " + t.trf("resize it to at least %d columns and %d rows,", minWidth, minHeight),
		"  " + t.tr("or press q to quit."),
		"",
		"  " + t.trf("now: %d x %d", w, h),
	}
	return msg
}

// recoverRaw restores the terminal before letting a panic continue.
//
// A session that dies while the terminal is in cbreak with echo off hands the user back a
// shell that shows nothing they type and runs nothing they see. The restore therefore happens
// on the way out of a panic as well as on the normal path, and it is the FIRST thing done —
// before the panic is allowed to keep unwinding.
//
// It is a named function returning the deferred call rather than an inline closure so the
// behaviour can be tested: triggering a real panic inside a live terminal is not something a
// test should do.
//
// It takes a pointer to the handle so it restores the handle current at panic time.
func recoverRaw(mode **terminalMode) func() {
	return func() {
		if r := recover(); r != nil {
			(*mode).restore()
			panic(r)
		}
	}
}
