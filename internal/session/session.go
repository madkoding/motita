package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/madkoding/motita/internal/llm"
)

// Summariser turns a block of conversation into a short account of it.
//
// It is the llm client in production, and a one-method interface here because the
// compaction path — what happens when the context is about to overflow — is exactly the
// path that must be testable without a provider. Leaving it concrete would make the most
// delicate logic in this package reachable only through the network.
type Summariser interface {
	Complete(ctx context.Context, messages []llm.Message) (string, error)
}

// Session is one conversation: the history the model sees, plus the rules that keep it
// inside the window.
//
// The invariant that matters is that the history is never silently shortened. Dropping
// the oldest messages is how an agent forgets the task it was given and starts answering
// the wrong question; when this session runs out of room it SUMMARISES what it is about
// to remove and carries the summary forward as a message of its own.
type Session struct {
	// System is the instruction that opens every request. It is never summarised and
	// never dropped: it is the contract, not the conversation.
	System string
	// Window is the model's context length in tokens, and Reserve is the room kept for
	// the answer and the next tool round — without it the session compacts at exactly the
	// point where the model still needs space to reply.
	Window  int
	Reserve int
	// CompactAt is the fraction of the usable window at which compaction is triggered.
	// A fraction rather than a hard edge, so the session compacts once with room to spare
	// instead of fighting the limit on every turn.
	CompactAt float64

	// KeepRecent is how many of the most recent messages are always preserved verbatim.
	// A summary of the last exchange is useless: the user is still talking about it.
	KeepRecent int

	// Summariser performs the compaction, and Model is what the provider is told.
	Summariser Summariser
	Model      string

	// mu guards everything below it.
	//
	// The session is read by the interface on every repaint — the status bar asks for the
	// token count — while the planner appends the tool results from the goroutine running the
	// turn. That is two goroutines on one slice, and the race detector flagged exactly that:
	// Snapshot() walking the messages while Append() extended them. A mutex is the whole fix;
	// the alternative, a copy for the reader, is the same lock with more allocation.
	mu sync.Mutex
	// messages is everything after the system prompt, oldest first.
	messages []llm.Message
	// summary is the accumulated account of what compaction removed. It is carried as
	// the first message of every request, so nothing that was summarised is lost.
	summary string
	// compactions counts them, for the report and for tests.
	compactions int
	// lastErr is why the last compaction could not be done, if it could not.
	lastErr error
}

// Defaults for a session built without explicit numbers.
const (
	// DefaultReserve leaves room for the answer and one round of tool calls.
	DefaultReserve = 4096
	// DefaultCompactAt triggers at 80% of the usable window.
	DefaultCompactAt = 0.8
	// DefaultKeepRecent is the tail that is never summarised.
	DefaultKeepRecent = 6
)

// ErrNoSummariser is returned when compaction is needed and none is configured. It is a
// real error rather than a silent truncation: the caller asked for a session that keeps
// its context, and quietly losing it instead is the failure being prevented.
var ErrNoSummariser = errors.New("the context must be compacted but no summariser is configured")

// New builds a session for a model, deriving the window when one is not given.
func New(model, system string, window int) *Session {
	if window <= 0 {
		window = ContextWindow(model)
	}
	return &Session{
		System:     system,
		Window:     window,
		Reserve:    DefaultReserve,
		CompactAt:  DefaultCompactAt,
		KeepRecent: DefaultKeepRecent,
		Model:      model,
	}
}

// Messages is the history as the model should see it: the system prompt, then the
// summary of what was compacted, then the conversation itself.
func (s *Session) Messages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messagesLocked()
}

// messagesLocked builds the history. The caller holds the lock.
func (s *Session) messagesLocked() []llm.Message {
	out := make([]llm.Message, 0, len(s.messages)+2)
	if s.System != "" {
		out = append(out, llm.Message{Role: "system", Content: s.System})
	}
	if s.summary != "" {
		out = append(out, llm.Message{Role: "system", Content: s.summaryNote()})
	}
	return append(out, s.messages...)
}

// Append adds a message to the conversation.
func (s *Session) Append(m llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
}

// AppendAll adds several messages, which is what a turn produces.
func (s *Session) AppendAll(msgs ...llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msgs...)
}

// Len is how many messages the conversation holds, excluding the system prompt.
func (s *Session) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// Compactions is how many times this session has compacted its context.
func (s *Session) Compactions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compactions
}

// Summary is the account carried forward from earlier compactions, empty when none.
func (s *Session) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summary
}

// LastError is why the most recent compaction failed, if it did. The caller reports it
// rather than the session pretending the context is intact.
func (s *Session) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Usable is the number of tokens available to the conversation: the window minus the
// reserve kept for the answer.
func (s *Session) Usable() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usableLocked()
}

// usableLocked is Usable without the lock, for callers that already hold it.
func (s *Session) usableLocked() int {
	// The reserve is sized for an ordinary window. Taken whole from a small one (phi3's
	// 4096) it left nothing, and the session asked to compact before the first request, so
	// a small window keeps at most a quarter of itself back.
	usable := s.Window - min(s.Reserve, s.Window/4)
	if usable < 1 {
		// A window smaller than the reserve is a misconfiguration, and the honest answer
		// is a small but positive budget rather than a negative one that would make the
		// trigger arithmetic meaningless.
		return 1
	}
	return usable
}

// Window is the model's context length, and Used is the fraction of the usable budget in
// use. They are the two numbers a status bar needs; the formatted report lives in the
// interface, because how to write a figure is a presentation decision and the session has
// no business making it.
//
// Both are exposed as fields on the snapshot below rather than as extra methods, so a caller
// reads them in one lock-free step.
//
// Tokens is the estimated size of what the model would receive right now.
func (s *Session) Tokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	// messagesLocked, not Messages: Messages takes the same lock, and this is called from
	// Snapshot which already holds it. Go's mutex is not reentrant, so the obvious version
	// deadlocks the first time the interface asks for the token count.
	return EstimateTokens(s.messagesLocked())
}

// Used is the fraction of the usable window in use.
func (s *Session) Used() float64 { return float64(s.Tokens()) / float64(s.Usable()) }

// Snapshot is what a status bar needs: the window, the usage, and where compaction sits.
// It is a copy, so a caller can read it while a turn is appending to the conversation.
type Snapshot struct {
	Model      string
	Window     int
	Tokens     int
	Used       float64
	CompactAt  float64
	KeepRecent int
	Messages   int
	Folds      int
	Summary    string
}

// Snapshot returns the current figures. It is the only way a concurrent reader should look
// at a session: the fields above are rewritten by compaction, and reading them directly
// from another goroutine is a race.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens := EstimateTokens(s.messagesLocked())
	used := float64(tokens) / float64(s.usableLocked())
	return Snapshot{
		Model:      s.Model,
		Window:     s.Window,
		Tokens:     tokens,
		Used:       used,
		CompactAt:  s.CompactAt,
		KeepRecent: s.KeepRecent,
		Messages:   len(s.messages),
		Folds:      s.compactions,
		Summary:    s.summary,
	}
}

// NeedsCompaction reports whether the context has grown past the trigger.
func (s *Session) NeedsCompaction() bool {
	at := s.CompactAt
	if at <= 0 || at > 1 {
		at = DefaultCompactAt
	}
	return s.Used() >= at
}

// Compact frees room before the next request, without losing what it removes.
//
// The work is done in three steps, and the middle one is the whole point:
//
//  1. keep the tail verbatim — the user is still talking about it;
//  2. ask the model to summarise the rest, together with any summary already carried, so
//     the account is cumulative rather than a series of disconnected notes;
//  3. replace the removed messages with that summary, as its own message, and record the
//     fact so the interface can say what happened.
//
// Nothing is dropped without being summarised. If the summary cannot be produced — no
// summariser, a provider failure — the session reports the failure and leaves the history
// ALONE: an over-long context is a provider error the caller can act on, while a silently
// truncated one is a wrong answer nobody can trace.
func (s *Session) Compact(ctx context.Context) error {
	if !s.NeedsCompaction() {
		return nil
	}
	keep := s.KeepRecent
	if keep < 1 {
		keep = DefaultKeepRecent
	}
	if len(s.messages) <= keep {
		// Nothing to summarise yet: the window is smaller than the tail that must stay
		// verbatim, which no amount of summarising can fix. Reporting it is better than
		// looping.
		return fmt.Errorf("the context is full but only %d messages are held, fewer than the %d kept verbatim", len(s.messages), keep)
	}

	cut := len(s.messages) - keep
	removed := s.messages[:cut]
	tail := s.messages[cut:]

	// A tool result whose assistant call is being removed must go too, or the remaining
	// history starts with an orphan the provider rejects.
	for len(tail) > 0 && tail[0].Role == "tool" {
		removed = append(removed, tail[0])
		tail = tail[1:]
	}

	if s.Summariser == nil {
		s.lastErr = ErrNoSummariser
		return s.lastErr
	}

	summary, err := s.summarise(ctx, removed)
	if err != nil {
		s.lastErr = err
		return err
	}

	s.summary = summary
	s.messages = tail
	s.compactions++
	s.lastErr = nil
	return nil
}

// compactPrompt is what the summariser is asked. It is written as an instruction to a
// careful colleague rather than as "summarise this", because the value of the summary is
// entirely in what it preserves: the task, the decisions already made, and the facts
// discovered — the three things whose loss makes an agent repeat work or contradict
// itself.
const compactPrompt = `You are maintaining the working memory of an autonomous agent.

Below is part of a conversation that must be removed to free space in the context window.
Write a compact account that lets the agent continue the task without repeating work.

Preserve, in this order of importance:
1. What the user asked for, in their own terms, including constraints and corrections.
2. Decisions already taken and the reason for each, so they are not revisited.
3. Facts discovered from tools: file paths, command results, versions, counts, errors.
4. What was tried and failed, so it is not tried again.
5. What remains to be done.

Omit: pleasantries, restatements of the plan, and tool output that carried no conclusion.
Write in the third person, as notes to be read before continuing. Be specific: names,
paths and numbers, never "some files" or "the version". Do not invent anything that is
not in the conversation.`

// summarise asks for the account. The messages being removed are handed over as a
// transcript, which is what the model can read without ambiguity.
func (s *Session) summarise(ctx context.Context, removed []llm.Message) (string, error) {
	var b strings.Builder
	if s.summary != "" {
		// The account already carried is compacted together with what is being removed,
		// so the summary stays a single document instead of a growing stack of them.
		b.WriteString("## Account carried from earlier\n\n")
		b.WriteString(s.summary)
		b.WriteString("\n\n")
	}
	b.WriteString("## Conversation to fold in\n\n")
	for _, m := range removed {
		b.WriteString(renderMessage(m))
	}

	req := []llm.Message{
		{Role: "system", Content: compactPrompt},
		{Role: "user", Content: b.String()},
	}
	out, err := s.Summariser.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("could not compact the context: %w", err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		// An empty summary would lose the history it was meant to preserve, so it is
		// treated as a failure rather than installed.
		return "", errors.New("the summariser returned nothing, so the context was left alone")
	}
	return out, nil
}

// renderMessage writes one message as a transcript line. Tool calls and their results are
// rendered explicitly: they are where the discovered facts live, and a transcript that
// flattens them to their text loses the very thing being preserved.
func renderMessage(m llm.Message) string {
	var b strings.Builder
	switch m.Role {
	case "user":
		b.WriteString("user: ")
	case "assistant":
		b.WriteString("agent: ")
	case "tool":
		b.WriteString("tool result: ")
	default:
		b.WriteString(m.Role + ": ")
	}
	if text := strings.TrimSpace(m.Content); text != "" {
		b.WriteString(text)
	}
	for _, call := range m.ToolCalls {
		fmt.Fprintf(&b, "\n  [called %s with %s]", call.Function.Name, strings.TrimSpace(string(call.Function.Arguments)))
	}
	b.WriteString("\n\n")
	return b.String()
}

// summaryNote frames the carried account so the model treats it as established fact
// rather than as something the user just said.
func (s *Session) summaryNote() string {
	return "## Context carried from earlier in this session\n\n" +
		"The following is an account of the conversation that came before, kept because the " +
		"full exchanges no longer fit in the context window. Treat it as established: it " +
		"records what was asked, decided and discovered.\n\n" + s.summary
}
