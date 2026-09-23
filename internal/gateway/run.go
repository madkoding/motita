package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/madkoding/starlight/internal/session"
)

// maxLoggedEvents is how many events of one run the gateway keeps to replay.
//
// A turn can emit thousands of progress lines, and keeping them all would let a long run grow
// without bound - the same reason the log is bounded at all. The number is a compromise: high enough
// that a client which reconnects after a normal blip loses nothing, low enough that a run cannot
// hold a megabyte of history nobody will read. When it is exceeded the OLDEST events are evicted
// and the loss is REPORTED (see logInfo.Dropped), because a client that silently renders a stream
// with a hole in it is worse than one that says it lost lines.
const maxLoggedEvents = 512

// subscriberBuffer is how far ahead of a reader the gateway will run before cutting it off.
//
// A subscriber is an HTTP handler writing to a socket. When the socket is slower than the run, the
// channel fills, and the choice is to drop events (leaving invisible holes) or to cut the
// subscriber off. Cutting it off is self-healing: the client reconnects and replays from its last
// sequence number.
const subscriberBuffer = 64

// loggedEvent is one event of a run, with the number a client uses to say where it got to.
type loggedEvent struct {
	// Seq is this event's position in the run, starting at 1 and strictly increasing.
	Seq uint64 `json:"seq"`
	// Event is the event name (EventProgress, EventDone, ...).
	Event string `json:"event"`
	// Data is the payload exactly as it went on the wire, kept as raw JSON so replaying it cannot
	// change it. A re-encoded payload would be a second encoding that can disagree with the first.
	Data json.RawMessage `json:"data"`
}

// logInfo answers "what can you still give me?".
type logInfo struct {
	// FirstSeq is the oldest event the log still holds, or 0 when it holds none.
	FirstSeq uint64
	// LastSeq is the newest.
	LastSeq uint64
	// Dropped is how many events were evicted, so a client knows it cannot be told everything.
	Dropped uint64
}

// subscriber is one attached reader: an HTTP handler, a test, anything.
type subscriber struct {
	ch   chan loggedEvent
	lost chan struct{}
	once sync.Once
}

// cutOff closes the lost channel exactly once, so several paths calling it is safe.
func (s *subscriber) cutOff() { s.once.Do(func() { close(s.lost) }) }

// run is ONE turn of one conversation, and it lives in the gateway rather than in a connection.
//
// This is the whole difference from the version where the handler wrote straight to its client's
// socket: the run owns its output, so the connection becomes a reader of it instead of its
// destination. A client that goes away stops reading; it does not stop the turn - and when it comes
// back it asks for what it missed, which is only possible because the output was kept.
type run struct {
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	// done is closed when the run has finished, whatever the outcome.
	done chan struct{}

	mu      sync.Mutex
	seq     uint64
	log     []loggedEvent
	dropped uint64
	subs    map[*subscriber]struct{}
	// outcome is empty while the run is in flight, and "done", "error" or "cancelled" after.
	outcome  string
	result   string
	errText  string
	snapshot session.Snapshot
}

// newRun creates a run with its own context and cancellation.
//
// The caller passes the context and its cancel func rather than the run building them, so
// cancellation has exactly ONE owner: the conversation, which also knows whether a run is in
// flight. Two owners would be two answers to "is this run still going".
func newRun(id string, ctx context.Context, cancel context.CancelFunc) *run {
	return &run{
		id:     id,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		subs:   map[*subscriber]struct{}{},
	}
}

// append records one event and hands it to every attached subscriber.
//
// The sequence number is assigned HERE, under the same lock that appends, so two events can never
// share a number and a client that replays from N cannot see one twice or miss one.
func (r *run) append(event string, payload any) uint64 {
	data, err := marshalEvent(payload)
	if err != nil {
		// The payloads this package emits are always encodable; the failure has its own test in
		// event.go, and an event that cannot be encoded is dropped rather than killing the run.
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e := loggedEvent{Seq: r.seq, Event: event, Data: data}
	r.log = append(r.log, e)
	if len(r.log) > maxLoggedEvents {
		drop := len(r.log) - maxLoggedEvents
		r.log = r.log[drop:]
		r.dropped += uint64(drop)
	}

	// The delivery is done from the SAME locked section, so an event cannot be handed out before
	// it is in the log: a subscriber that received it and then replayed would otherwise see it
	// twice.
	for s := range r.subs {
		select {
		case s.ch <- e:
		default:
			// This subscriber cannot keep up. It is cut off rather than skipped: dropping the
			// event would leave it rendering a stream with an invisible hole in it.
			s.cutOff()
		}
	}
	return e.Seq
}

// since returns the events after the given sequence number, and what the log can say about its own
// completeness.
//
// from is EXCLUSIVE: a client sends the last event it saw, and gets the next one. That is what
// makes reconnection a one-line operation for the client instead of a reconciliation. Asking from a
// number past the end is therefore not an error - it means the client is current.
func (r *run) since(from uint64) ([]loggedEvent, logInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info := logInfo{Dropped: r.dropped, LastSeq: r.seq}
	if len(r.log) > 0 {
		info.FirstSeq = r.log[0].Seq
	}
	out := make([]loggedEvent, 0, len(r.log))
	for _, e := range r.log {
		if e.Seq > from {
			out = append(out, e)
		}
	}
	return out, info
}

// subscribe attaches a reader to the run from now on.
//
// The caller must pair it with unsubscribe. Replay is NOT done here: the caller asks since() and
// subscribes, in that order, so an event appended between the two is neither missed (it is in the
// replay) nor duplicated (the subscription starts after it).
func (r *run) subscribe() *subscriber {
	s := &subscriber{ch: make(chan loggedEvent, subscriberBuffer), lost: make(chan struct{})}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs[s] = struct{}{}
	return s
}

// unsubscribe detaches a reader, and is safe to call twice.
//
// Twice matters: a handler whose client vanished can reach this from the deferred cleanup AND from
// the cut-off branch, and a panic on the second call would take the whole gateway with it.
func (r *run) unsubscribe(s *subscriber) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, s)
}

// subscriberCount is the number of attached readers, for tests and for the run's own status.
func (r *run) subscriberCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subs)
}

// finish records the outcome and closes done.
//
// A run finishes EXACTLY once: the agent's turn has one ending, and a second one would report an
// outcome for a turn that already had one - which is how a cancelled run gets reported as a
// success. Callers therefore never have to reason about which finish won.
//
// It does NOT cut off the subscribers. Cutting off means "you cannot keep up", and a run that
// ended is not a reader that fell behind: its events are still in the channel, waiting to be read.
// A reader is woken by done and drains what is left before closing its stream. Doing it the other
// way around made every fast run look to its own client like a reader that had fallen behind -
// which is a lie about a turn that simply finished quickly.
func (r *run) finish(outcome, result, errText string, snap session.Snapshot) {
	r.mu.Lock()
	if r.outcome != "" {
		r.mu.Unlock()
		return
	}
	r.outcome, r.result, r.errText, r.snapshot = outcome, result, errText, snap
	r.mu.Unlock()

	close(r.done)
}

// outcomeOf reports how the run ended, and whether it has.
func (r *run) outcomeOf() (string, string, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.outcome, r.result, r.errText, r.outcome != ""
}

// String makes a run readable in a failure message, which is where it usually appears.
func (r *run) String() string {
	outcome, _, _, finished := r.outcomeOf()
	if !finished {
		outcome = "in flight"
	}
	return fmt.Sprintf("run %s (%s)", r.id, outcome)
}
