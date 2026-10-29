package tui

// The conversation the runner holds: its archive on disk, the recall of earlier turns, and a
// report of how much of the model's window is in use.

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/contextmem"
)

// SetContextID tells the runner which conversation it is serving, and so which archive on
// disk belongs to it. The gateway knows the id (it is the session's), and passing it here is
// what lets a served conversation find its archive again after a restart. A local
// conversation keeps the random id it was born with.
func (r *AppRunner) SetContextID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	r.contextMu.Lock()
	defer r.contextMu.Unlock()
	if id == r.contextID {
		return
	}
	// What was archived under the placeholder id belongs to no conversation the user can
	// return to, so it goes rather than being left as an orphan.
	if r.context != nil {
		_ = r.context.Close()
		r.context = nil
	}
	r.contextID = id
}

// contextStore returns the archive of this conversation, opening it on first use. Nil when
// the feature is off, or when there is no home to keep it in: both are states where the
// right answer is to carry on without it.
func (r *AppRunner) contextStore() *contextmem.Store {
	r.contextMu.Lock()
	defer r.contextMu.Unlock()
	return r.contextStoreLocked()
}

// contextStoreLocked opens the archive. The caller holds contextMu. It reads the
// configuration, so the lock order is contextMu then cfgMu; nothing takes them the other way.
func (r *AppRunner) contextStoreLocked() *contextmem.Store {
	if r.context != nil {
		return r.context
	}
	cfg := r.Config()
	if !cfg.ContextMemory.Enabled {
		return nil
	}
	dir := contextmem.Dir(config.ContextDir(), r.contextID)
	if dir == "" {
		return nil
	}
	store, err := contextmem.Open(dir, contextmem.Options{
		RecallTokens: cfg.ContextMemory.RecallTokens,
		RecallHits:   cfg.ContextMemory.RecallHits,
		MaxTurns:     cfg.ContextMemory.MaxTurns,
	})
	if err != nil {
		if r.Log != nil {
			r.Log.Warn("could not open the conversation archive", "error", err.Error())
		}
		return nil
	}
	r.context = store
	return store
}

// resetContext deletes this conversation's archive and forgets it, so the next turn opens an
// empty one with the same id.
func (r *AppRunner) resetContext() {
	r.contextMu.Lock()
	defer r.contextMu.Unlock()
	if r.context != nil {
		if err := r.context.Close(); err != nil && r.Log != nil {
			r.Log.Warn("could not delete the conversation archive", "error", err.Error())
		}
		r.context = nil
	}
}

// syncContext writes the turns that are not archived yet. It runs after a turn is recorded,
// which is the only moment the runner knows the conversation grew: the transcript is the
// record, and the archive is its history.
func (r *AppRunner) syncContext() {
	store := r.contextStore()
	if store == nil {
		return
	}
	turns := r.history()
	archived := make([]contextmem.Turn, len(turns))
	for i, t := range turns {
		archived[i] = contextmem.Turn{
			Kind:    t.Kind,
			User:    t.User,
			Agent:   t.Agent,
			Pending: t.Pending,
		}
	}
	// The structured report is the other half of a task turn, and it is what a later turn
	// needs to know what was verified rather than only what was said.
	if n := len(archived); n > 0 && archived[n-1].Kind == agent.KindTask {
		if rep := r.lastReport(); rep != nil {
			archived[n-1].Report = rep.Text()
		}
	}
	if err := store.Sync(archived); err != nil && r.Log != nil {
		r.Log.Warn("could not archive the conversation", "error", err.Error())
	}
}

// lastReport is the report of the turn that just finished, or nil.
func (r *AppRunner) lastReport() *agent.Report {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	return r.report
}

// recallTask is what a Task-mode turn asks. live is how many turns the agent's prompt still
// carries verbatim, which the agent knows: the turn in progress counts as one at the top level.
func (r *AppRunner) recallTask(ctx context.Context, query string, live int) (string, int) {
	return r.recall(ctx, query, live)
}

// recallPlan is what a Plan-mode turn asks. live is the number of turns the session still
// shows: before a compaction that is all of them, and after one it is only the tail.
func (r *AppRunner) recallPlan(ctx context.Context, query string, live int) (string, int) {
	return r.recall(ctx, query, live)
}

// recall asks the archive, with the previous turn added to the query: a short request ("and
// the other one?") means nothing on its own, and the exchange it answers is what gives it its
// subject.
func (r *AppRunner) recall(ctx context.Context, query string, live int) (string, int) {
	store := r.contextStore()
	if store == nil {
		return "", 0
	}
	if last := r.lastUserTurn(); last != "" {
		query += "\n" + last
	}
	return store.Recall(ctx, query, live)
}

// lastUserTurn is the most recent thing the person asked, or "" when there is none.
func (r *AppRunner) lastUserTurn() string {
	turns := r.history()
	for i := len(turns) - 1; i >= 0; i-- {
		if u := strings.TrimSpace(turns[i].User); u != "" {
			return u
		}
	}
	return ""
}

// ConversationReport describes the conversation for the interface: how much of the window is
// in use, how it compacts, and what the carried summary holds.
//
// It is exposed because a session the user cannot see is a session they cannot trust: an
// agent whose history was folded without a word looks like one that simply forgot.
func (r *AppRunner) ConversationReport() string {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.session == nil {
		return "This conversation has not started yet. Ask something in Plan mode."
	}
	s := r.session
	var b strings.Builder
	fmt.Fprintf(&b, "model       %s\n", s.Model)
	fmt.Fprintf(&b, "context     %d tokens\n", s.Window)
	fmt.Fprintf(&b, "in use      %d tokens (%.0f%% of the usable window)\n", s.Tokens(), s.Used()*100)
	fmt.Fprintf(&b, "compaction  at %.0f%%, keeping the last %d messages\n", s.CompactAt*100, s.KeepRecent)
	fmt.Fprintf(&b, "messages    %d held", s.Len())
	if n := s.Compactions(); n > 0 {
		fmt.Fprintf(&b, ", %d compaction(s)", n)
	}
	b.WriteString("\n")
	if s.Summary() != "" {
		b.WriteString("\n--- carried summary ---\n")
		b.WriteString(s.Summary())
		b.WriteString("\n")
	}
	return b.String()
}

// history returns the Task-mode conversation to seed a new agent with.
func (r *AppRunner) history() []agent.DialogueTurn {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	return append([]agent.DialogueTurn(nil), r.transcript...)
}

// Transcript returns the Task-mode conversation, which is what a front end draws.
//
// It is the EXPORTED half of history(), and it exists so that the gateway's Service contract can
// reach it without this package importing internal/gateway - the interface is declared
// structurally, so the method name is the whole coupling. The copy is deliberate: the caller gets
// the turns as they were, and a turn appended a moment later cannot change the slice it holds.
func (r *AppRunner) Transcript() []agent.DialogueTurn { return r.history() }

// remember stores the conversation a finished turn ended with.
func (r *AppRunner) remember(turns []agent.DialogueTurn) {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	r.transcript = append([]agent.DialogueTurn(nil), turns...)
}

// RestoreTranscript loads a saved conversation into the runner, replacing
// anything it currently holds. It is how a session persisted to disk and
// reloaded on startup carries its history forward.
func (r *AppRunner) RestoreTranscript(turns []agent.DialogueTurn) {
	// Loading a saved conversation is storing it, exactly as a finished turn does.
	r.remember(turns)
}

// ResetTranscript drops the Task-mode conversation, which is how /new leaves a subject
// behind. Plan mode's session is reset alongside it, because to the user they are the same
// conversation.
func (r *AppRunner) ResetTranscript() {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	r.transcript = nil
}
