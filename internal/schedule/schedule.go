// Package schedule holds the tasks that fire on their own.
//
// It is a package of its own, and it does not import the gateway, for one reason: the
// part that decides WHEN something is due is pure arithmetic over a record, and
// keeping it away from the transport is what lets it be tested with an injected clock
// instead of a real timer. The gateway supplies the two things this package cannot
// know: where the records live (a directory) and what firing actually does (a Firer).
//
// Nothing here adds a dependency: encoding/json and time are the standard library, and
// the released binary has a 10 MB ceiling with roughly 1 MB of room beneath it.
package schedule

import (
	"encoding/json"
	"fmt"
	"time"
)

// DefaultSessionID is the conversation a scheduled task fires into when it names none.
//
// It is "default" rather than a new session per firing: the default conversation is the
// one every gateway holds, so a task created from the interface lands where the user is
// already looking, and its turn is attachable like any other.
const DefaultSessionID = "default"

// Kind is what a scheduled task runs.
const (
	KindTask = "task"
	KindPlan = "plan"
)

// Duration is a time.Duration that travels as the string Go prints for it ("30m",
// "1h30m0s") instead of as the integer of nanoseconds the standard marshaller emits.
//
// The record is written to a JSON file an operator may open and edit, and a number
// like 1800000000000 is a number nobody can review. The cost is twenty lines and a
// round-trip test; the alternative is a file that has to be decoded in the reader's
// head.
type Duration time.Duration

// MarshalJSON writes the duration in the form time.ParseDuration reads back.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON reads that form, and REFUSES anything else.
//
// Refusing rather than defaulting matters: a zero cadence is a task that never fires,
// which is indistinguishable from a broken feature, and the error names what could not
// be read so the file can be fixed.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("a duration must be written as a string like \"30m\": %w", err)
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("could not read the duration %q (use \"30m\", \"1h\", \"1h30m\"): %w", text, err)
	}
	*d = Duration(parsed)
	return nil
}

// Schedule is ONE task that fires on its own.
type Schedule struct {
	ID string `json:"id"`
	// Title is what a front end draws. It is the only field a human reads.
	Title string `json:"title"`
	// Task is the text submitted to the agent, exactly as it would be typed.
	Task string `json:"task"`
	// Kind is KindTask or KindPlan. A plan run changes nothing, which is a legitimate
	// thing to schedule (a nightly audit), so both are allowed.
	Kind string `json:"kind"`
	// SessionID is the conversation the turn lands in.
	SessionID string `json:"session_id"`
	// Every is the cadence.
	Every Duration `json:"every"`
	// Enabled is the pause switch. A disabled task is never due.
	Enabled bool `json:"enabled"`
	// Created is when the task was made, and it is the origin the FIRST firing is
	// measured from.
	Created time.Time `json:"created"`
	// LastRun is when it last fired, or the zero time when it never has. It is the
	// origin every later firing is measured from.
	LastRun time.Time `json:"last_run,omitempty"`
	// LastOutcome says how the last firing ended, in a sentence a user reads. It is
	// empty until there has been one.
	LastOutcome string `json:"last_outcome,omitempty"`
	// RunCount is how many times it has fired.
	RunCount int `json:"run_count,omitempty"`
}

// Due reports whether this task should fire at the given moment.
//
// The origin is Created until the task has run, and LastRun afterwards, which gives the
// behaviour a scheduler is expected to have: a task created a minute ago with an hourly
// cadence does not fire immediately, and a gateway that restarted twice does not catch
// up on the firings it missed.
//
// A cadence of zero or less is NEVER due rather than always due. Zero is refused at the
// door (see the gateway's create handler), and "never" is the safe reading while it is
// not: "always" is an infinite loop that starts the moment a malformed file is loaded.
func (s Schedule) Due(now time.Time) bool {
	if !s.Enabled || s.Every <= 0 {
		return false
	}
	every := time.Duration(s.Every)
	if s.LastRun.IsZero() {
		return !now.Before(s.Created.Add(every))
	}
	return !now.Before(s.LastRun.Add(every))
}

// Next reports when this task fires again after the given moment.
//
// It exists for a front end that says "next run at 14:00" without re-deriving the rule,
// which is how a second copy of the cadence arithmetic gets written and drifts.
func (s Schedule) Next(after time.Time) time.Time {
	if s.Every <= 0 {
		return after
	}
	origin := s.Created
	if !s.LastRun.IsZero() {
		origin = s.LastRun
	}
	next := origin.Add(time.Duration(s.Every))
	for next.Before(after) {
		next = next.Add(time.Duration(s.Every))
	}
	return next
}
