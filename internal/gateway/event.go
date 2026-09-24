package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/madkoding/motita/internal/session"
)

// Event names on the run stream. They are constants because the client switches on them: a
// literal typed on both sides is a stream that stops being understood after one rename.
const (
	// EventProgress carries one progress line, verbatim from the agent.
	EventProgress = "progress"
	// EventApproval asks the client to confirm a consequential command. The run is BLOCKED
	// until the client answers on POST /v1/runs/approval.
	EventApproval = "approval"
	// EventDone carries the turn's result and the session figures that go with it.
	EventDone = "done"
	// EventError carries a failure that ended the run.
	//
	// A failure AFTER the first byte cannot be a status code - the 200 and the headers are
	// already on the wire - so it belongs here, where the client is reading.
	EventError = "error"
	// EventAttached opens a stream that joined a run already in flight.
	//
	// It comes FIRST, before the replay, and it carries what a client cannot learn from the
	// events themselves: the sequence range the log can still serve, whether anything was
	// evicted, and the approval that is pending RIGHT NOW. A client that reconnects while the
	// run is blocked on a question has to be told about it, or it waits forever for a run that
	// is waiting for it.
	EventAttached = "attached"
)

// progressEvent is one line of the agent's output.
type progressEvent struct {
	Text string `json:"text"`
}

// approvalEvent is a command waiting for a human answer.
type approvalEvent struct {
	// ID identifies this one question, so an answer cannot be replayed onto the next one.
	ID string `json:"id"`
	// Command is the line exactly as it would run. The user approves THIS text, so it is
	// never summarised on the way here.
	Command string `json:"command"`
	// Reason says why it is being asked about.
	Reason string `json:"reason"`
	// Rule names the policy rule, so a repeat complaint can be traced to a line of code.
	Rule string `json:"rule"`
}

// doneEvent ends a turn.
type doneEvent struct {
	Result  string           `json:"result"`
	Session session.Snapshot `json:"session"`
}

// attachedEvent is the preamble of a stream that joined a run in flight.
//
// It is a SNAPSHOT and not a replay: the events after it are the run's own, and this says what
// state they are arriving into. Every field here answers a question the events cannot: where the
// log starts (so a client can tell it lost lines), what the newest sequence is (so it can tell it
// is current), whether a question is open (so it can answer it), and how the run ended if it
// already has (so it does not wait on a stream that will never produce anything).
type attachedEvent struct {
	RunID string `json:"run_id"`
	// FirstSeq is the oldest event the log still holds. A client that asked to resume from an
	// older one has lost events, and this is the number that says so.
	FirstSeq uint64 `json:"first_seq"`
	// LastSeq is the newest event in the log.
	LastSeq uint64 `json:"last_seq"`
	// Dropped is how many events were evicted before this client arrived. It is what turns
	// "there is a hole" into "there is a hole of this size".
	Dropped uint64 `json:"dropped"`
	// PendingApproval is the question waiting for an answer, if the run is blocked on one.
	//
	// Omitted when nothing is being asked, rather than sent as null: a client switching on the
	// field's presence should not have to also test for null.
	PendingApproval *approvalEvent `json:"pending_approval,omitempty"`
	// Outcome is empty while the run is in flight, and "done", "error" or "cancelled" after it
	// ends. A client that attaches to a finished run is TOLD, instead of waiting on a stream
	// that will never produce anything.
	Outcome string `json:"outcome,omitempty"`
}

// startStream writes the headers a long-lived stream needs, and gets them on the wire before
// the first event so a client is not left guessing whether the request was even accepted.
//
// The initial comment line is deliberate: it is a valid server-sent event that carries no data,
// and it is what makes the headers arrive at a client that is waiting for its first byte.
func startStream(w http.ResponseWriter, rc *http.ResponseController) error {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// A proxy that buffers would hold the whole run until it ended, which turns a stream into a
	// hang. This asks it not to.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, ": connected\n\n"); err != nil {
		return err
	}
	return rc.Flush()
}

// writeEvent frames one server-sent event and flushes it.
//
// The framing is a WIRE CONTRACT: exactly one data line per event, and the payload is JSON so
// it can never contain a raw newline (JSON escapes them). That invariant is what lets the
// client dispatch on the data line instead of buffering multi-line events, and it is asserted
// on both ends - see streamEvents in client.go.
//
// The `id:` line is what makes reconnection possible: SSE defines it so a client can remember
// where it got to and ask for the rest, and without it an interrupted stream has to be
// reconciled instead of resumed. It carries the SEQUENCE NUMBER from the run's log, so it means
// the same thing across connections - a client that saw id 42 reconnects and asks for what
// follows 42. A frame that is not part of the numbering (the preamble, a refusal) passes 0.
func writeEvent(w http.ResponseWriter, rc *http.ResponseController, seq uint64, event string, payload any) error {
	data, err := marshalEvent(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, event, data); err != nil {
		return err
	}
	return rc.Flush()
}

// writeRaw writes an event that came out of the log.
//
// The payload is written AS IT WAS STORED rather than re-encoded. Re-encoding would be a second
// encoding of the same value and the two could differ - a replay that is not byte-identical to
// the original is a replay a client can tell apart from the live stream, which breaks the one
// property reconnection is supposed to have: that the client cannot tell it was away.
func writeRaw(w http.ResponseWriter, rc *http.ResponseController, e loggedEvent) error {
	if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Event, e.Data); err != nil {
		return err
	}
	return rc.Flush()
}

// marshalEvent encodes one event payload.
//
// It is separate from the writing so the failure has a test. json.Marshal cannot fail for the
// structs this package emits, so a test drives it with a value that genuinely cannot be
// encoded (a channel). Keeping the branch reachable is the same rule the rest of the repository
// follows: an error path nothing can provoke is an error path that rots.
func marshalEvent(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("could not encode the event: %w", err)
	}
	return data, nil
}
