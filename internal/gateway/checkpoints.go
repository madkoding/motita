package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/gitx"
)

// A CHECKPOINT is the state of a conversation just before one input of the user's ran: the turn
// it will become, what was asked, the files as they were (a git snapshot, see gitx.Snapshot),
// and - as the run goes - every step and command it took. It has two jobs that are one record
// on purpose:
//
//   - the way back: restoring it puts the conversation and, when asked, the files where they
//     were before that input;
//   - the history of the work: the steps and commands are what the chat and the terminal drawer
//     show for that turn, and they are kept with the session so they survive a restart. Before
//     this they lived only in the browser tab that watched the run, and were gone with it.

// Caps that keep one turn from growing a session file without limit.
const (
	// maxStepsPerTurn is how many steps one turn keeps; the oldest fall off first.
	maxStepsPerTurn = 300
	// maxStepText caps one command's output or one thought. What is dropped is the middle: the
	// start says what was running and the end says how it finished.
	//
	// It matches what the agent itself keeps for the terminal (agent's terminalOutputChars), so
	// the drawer never cuts something the agent already had whole - measured: an 8 KB cap here
	// turned a 19 KB grep into a cut record with no indication of why.
	maxStepText = 32 << 10
)

// Step kinds. The client classifies a "step" further from its text; the gateway only needs the
// two that have a structure of their own.
const (
	stepCommand = "command"
	stepThought = "thought"
	stepPlain   = "step"
)

// stepRecord is one thing the agent did during a turn.
type stepRecord struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Exit is a command's result once it finished; nil while it runs (or if it never reported).
	Exit *int `json:"exit,omitempty"`
	// Out is what the command printed.
	Out string `json:"out,omitempty"`
}

// checkpoint is one user input and everything needed to go back to before it.
type checkpoint struct {
	// Turn is the index of the conversation turn this input became.
	Turn    int       `json:"turn"`
	Task    string    `json:"task"`
	Kind    string    `json:"kind"`
	Created time.Time `json:"created"`
	// Head and Snap anchor the files: the commit the tree was on and the snapshot of the tree.
	// Both are empty when the workspace is not a repository with a commit, and the checkpoint
	// then restores the conversation only.
	Head  string       `json:"head,omitempty"`
	Snap  string       `json:"snap,omitempty"`
	Steps []stepRecord `json:"steps"`
}

// checkpointView is the checkpoint as a client sees it: the anchors are the gateway's business.
type checkpointView struct {
	Turn    int          `json:"turn"`
	Task    string       `json:"task"`
	Kind    string       `json:"kind"`
	Created time.Time    `json:"created"`
	Files   bool         `json:"files"`
	Steps   []stepRecord `json:"steps"`
}

// checkpointRef names the git reference that keeps a turn's snapshot alive.
func checkpointRef(sessionID string, turn int) string {
	return "refs/motita/checkpoints/" + sessionID + "/" + strconv.Itoa(turn)
}

// undoRef keeps the state a restore replaced, so a restore can itself be taken back.
func undoRef(sessionID string) string { return "refs/motita/undo/" + sessionID }

// turnFor is the index the next input will take in the conversation. A pending turn for the same
// request is a run that resumes after a restart: it continues its own turn.
func turnFor(turns []agent.DialogueTurn, task string) int {
	if n := len(turns); n > 0 && turns[n-1].Pending && turns[n-1].User == task {
		return n - 1
	}
	return len(turns)
}

// openCheckpoint registers the checkpoint of an input that is starting, and returns whether the
// caller must still snapshot the files). A run that RESUMES its own pending turn (after a
// restart) keeps its record and its snapshot, which is from BEFORE it started.
func (c *conversation) openCheckpoint(turn int, task, kind string, resumed bool) (needSnapshot bool) {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	for i, cp := range c.checkpoints {
		if cp.Turn != turn {
			continue
		}
		if resumed && cp.Task == task {
			return false
		}
		// The number is taken by a run that left no turn behind (it failed or was
		// cancelled), so its record describes nothing that exists.
		c.checkpoints = append(c.checkpoints[:i], c.checkpoints[i+1:]...)
		break
	}
	c.checkpoints = append(c.checkpoints, &checkpoint{
		Turn: turn, Task: task, Kind: kind, Created: time.Now().UTC(), Steps: []stepRecord{},
	})
	return true
}

// setSnapshot records where the files were before the turn.
func (c *conversation) setSnapshot(turn int, head, snap string) {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	if cp := c.checkpointLocked(turn); cp != nil {
		cp.Head, cp.Snap = head, snap
	}
}

func (c *conversation) checkpointLocked(turn int) *checkpoint {
	for _, cp := range c.checkpoints {
		if cp.Turn == turn {
			return cp
		}
	}
	return nil
}

// clipMiddle cuts s to at most max bytes by dropping its middle, on rune boundaries.
func clipMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	note := fmt.Sprintf("\n... [%d of %d bytes omitted] ...\n", len(s)-max, len(s))
	half := (max - len(note)) / 2
	head, tail := half, len(s)-half
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return s[:head] + note + s[tail:]
}

// recordStep files one progress line under its turn. The lines are the agent's own, verbatim:
// see run.progress for the ones that are not steps at all.
func (c *conversation) recordStep(turn int, text string) {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	cp := c.checkpointLocked(turn)
	if cp == nil || text == "" {
		return
	}
	if rest, ok := strings.CutPrefix(text, "output (exit "); ok {
		// "output (exit N):\nbody" closes the command that is still open.
		num, body, _ := strings.Cut(rest, "):")
		code, err := strconv.Atoi(num)
		if err != nil {
			code = 0
		}
		for i := len(cp.Steps) - 1; i >= 0; i-- {
			if cp.Steps[i].Kind == stepCommand && cp.Steps[i].Exit == nil {
				cp.Steps[i].Exit = &code
				cp.Steps[i].Out = clipMiddle(strings.TrimPrefix(body, "\n"), maxStepText)
				return
			}
		}
		return
	}
	step := stepRecord{Kind: stepPlain, Text: clipMiddle(text, maxStepText)}
	switch {
	case strings.HasPrefix(text, "running:"):
		step.Kind = stepCommand
	case strings.HasPrefix(text, "thinking:"):
		step.Kind = stepThought
	}
	cp.Steps = append(cp.Steps, step)
	if len(cp.Steps) > maxStepsPerTurn {
		cp.Steps = append([]stepRecord(nil), cp.Steps[len(cp.Steps)-maxStepsPerTurn:]...)
	}
}

// checkpointViews copies the checkpoints for a client, oldest first.
func (c *conversation) checkpointViews() []checkpointView {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	out := make([]checkpointView, 0, len(c.checkpoints))
	for _, cp := range c.checkpoints {
		out = append(out, checkpointView{
			Turn: cp.Turn, Task: cp.Task, Kind: cp.Kind, Created: cp.Created,
			Files: cp.Snap != "", Steps: append([]stepRecord{}, cp.Steps...),
		})
	}
	return out
}

// checkpointRecords copies the checkpoints for the session file.
func (c *conversation) checkpointRecords() []checkpoint {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	out := make([]checkpoint, 0, len(c.checkpoints))
	for _, cp := range c.checkpoints {
		cc := *cp
		cc.Steps = append([]stepRecord{}, cp.Steps...)
		out = append(out, cc)
	}
	return out
}

// setCheckpoints loads the checkpoints a saved session had.
func (c *conversation) setCheckpoints(recs []checkpoint) {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	c.checkpoints = c.checkpoints[:0]
	for i := range recs {
		cp := recs[i]
		if cp.Steps == nil {
			cp.Steps = []stepRecord{}
		}
		c.checkpoints = append(c.checkpoints, &cp)
	}
}

// cutCheckpointsFrom removes the checkpoints of turn and every later one, and returns them.
func (c *conversation) cutCheckpointsFrom(turn int) []checkpoint {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	var cut []checkpoint
	keep := c.checkpoints[:0]
	for _, cp := range c.checkpoints {
		if cp.Turn >= turn {
			cut = append(cut, *cp)
			continue
		}
		keep = append(keep, cp)
	}
	c.checkpoints = keep
	return cut
}

// gitDir is where a session's files live and where its checkpoint references are kept: its own
// worktree when it has one, which shares the references of the project it belongs to. A
// free-standing session has no workspace of its own and works in the configured one, so that
// is where its files are. gitx refuses anything that is not the root of its own repository.
func (c *conversation) gitDir() string {
	c.stateMu.Lock()
	ws := c.workspace
	c.stateMu.Unlock()
	if ws == "" && c.svc != nil {
		ws = c.svc.Config().Agent.WorkspaceDir
	}
	return ws
}

// takeSnapshot snapshots the files of a turn that is starting. A failure is not the turn's
// failure: the checkpoint then restores the conversation only, which is still worth having.
func (s *Server) takeSnapshot(c *conversation, turn int) {
	head, snap, err := gitx.Snapshot(s.baseCtx, c.gitDir(), checkpointRef(c.id, turn))
	if err != nil {
		if s.opts.Log != nil {
			s.opts.Log.Warn("the files could not be checkpointed", "id", c.id, "turn", turn, "error", err.Error())
		}
		return
	}
	c.setSnapshot(turn, head, snap)
}

// refDir is where git can be asked about the references: the project's own checkout when the
// session has one (the references are shared by all its worktrees, and the session's own may
// already be gone), otherwise the workspace.
func (c *conversation) refDir() string {
	c.stateMu.Lock()
	pd := c.projectDir
	c.stateMu.Unlock()
	if pd != "" {
		return pd
	}
	return c.gitDir()
}

// dropCheckpointRefs lets git collect the snapshots of checkpoints that no longer exist.
func (s *Server) dropCheckpointRefs(c *conversation, cps []checkpoint) {
	dir := c.refDir()
	for _, cp := range cps {
		if cp.Snap != "" {
			gitx.DropRef(s.baseCtx, dir, checkpointRef(c.id, cp.Turn))
		}
	}
}

// forgetCheckpoints drops every checkpoint of a conversation that starts over or is deleted.
func (s *Server) forgetCheckpoints(c *conversation) {
	s.dropCheckpointRefs(c, c.cutCheckpointsFrom(0))
	gitx.DropRef(s.baseCtx, c.refDir(), undoRef(c.id))
}

// handleCheckpoints answers the checkpoints of the conversation, with the steps of each.
func (s *Server) handleCheckpoints(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"checkpoints": convOf(r).checkpointViews()})
}

// handleRestoreCheckpoint takes the conversation back to before one input.
//
// The conversation always goes back: that input and everything after it leave the transcript,
// and the answer carries the input's text so the client can offer it for editing. The files go
// back only when the body says `"files": true` and the checkpoint has them. That is destructive
// - it discards whatever changed since, the user's own edits included - so the state it replaces
// is first kept under a reference of its own, and the answer says whether that worked.
//
// A run in flight is refused, not stopped: going back while the agent is writing would race it.
func (s *Server) handleRestoreCheckpoint(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	turn, err := strconv.Atoi(r.PathValue("turn"))
	if err != nil || turn < 0 {
		writeError(w, http.StatusBadRequest, "the checkpoint number is not valid")
		return
	}
	var body struct {
		Files bool `json:"files"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if c.isRunning() {
		writeError(w, http.StatusConflict, "a run is in progress in this session: stop it before going back")
		return
	}
	c.cpMu.Lock()
	cp := c.checkpointLocked(turn)
	var found checkpoint
	if cp != nil {
		found = *cp
	}
	c.cpMu.Unlock()
	if cp == nil {
		writeError(w, http.StatusNotFound, "there is no checkpoint for that input")
		return
	}

	filesBack, undone := false, false
	if body.Files {
		if found.Snap == "" {
			writeError(w, http.StatusConflict, "that checkpoint did not keep the files: only the conversation can go back")
			return
		}
		dir := c.gitDir()
		if _, snap, serr := gitx.Snapshot(r.Context(), dir, undoRef(c.id)); serr == nil && snap != "" {
			undone = true
		}
		if err := gitx.Restore(r.Context(), dir, found.Head, found.Snap); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, gitx.ErrNoCheckpoint) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		filesBack = true
	}

	turns := c.svc.Transcript()
	if turn < len(turns) {
		c.svc.RestoreTranscript(turns[:turn])
	}
	s.dropCheckpointRefs(c, c.cutCheckpointsFrom(turn))
	s.saveSession(c)
	writeJSON(w, http.StatusOK, map[string]any{
		"task": found.Task, "files_restored": filesBack, "undo_kept": undone,
	})
}
