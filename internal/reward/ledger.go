// Package reward keeps a long-term value per skill, moved only by the user's own verdict.
//
// The problem it solves: the procedure library is a shelf, and its search ranks by how well
// the words match. That ranking cannot tell a skill that helped from one that wasted the
// turn — both mention "zephyr" and "build". So a library accumulates documents that read as
// relevant and are not, and the model keeps reaching for them.
//
// The signal here is the USER's verdict and nothing else. The agent never rates itself: a
// model that scores its own work is the documented way to reinforce its own mistakes, and it
// would be scoring the very thing the reward is meant to correct. If the user marks nothing,
// no value moves — an unmarked turn is silence, not a zero.
//
// Value is a running average, so a skill's score is "how well has this worked, on average,
// lately". Recent verdicts weigh more (see DECAY): a success from three months ago should not
// hold a skill above one that has been working all week.
package reward

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The filesystem calls whose failure branches cannot be provoked from a test any other way are
// seams, the same pattern the rest of this repository uses: a directory that cannot be created,
// a temp file that cannot be opened, a rename that fails. These are disk problems, and the
// handling for them has to be exercised rather than hoped for.
var (
	createTemp  = os.CreateTemp
	renameFile  = os.Rename
	mkdirAll    = os.MkdirAll
	writeLedger = func(f *os.File, data []byte) (int, error) { return f.Write(data) }
	closeLedger = func(f *os.File) error { return f.Close() }
)

// DECAY is how much of the previous value survives each verdict.
//
// 0.8 is a compromise, and it is worth saying what it trades. At 1.0 the value is a plain
// average that never forgets: a skill that worked ten times and then broke stays "good"
// forever. At 0.5 the value is nearly the last verdict alone, which makes the score jump on
// one bad day. With 0.8 a skill carries roughly the weight of its last four verdicts, which
// is long enough to be a trend and short enough to notice a change.
//
// The formula is V' = DECAY*V + (1-DECAY)*signal, with V starting at 0 and the signal being
// +1 for a good turn and -1 for a bad one. So V lives in [-1, 1] and needs no clamping.
const DECAY = 0.8

// Score is the accumulated value of one skill, with the evidence behind it.
type Score struct {
	// Value is the running average in [-1, 1]. Positive means the verdicts have been good.
	Value float64 `json:"value"`
	// Good and Bad are the raw counts, kept so the number can be read with its evidence:
	// "0.7" from one verdict and from forty are not the same claim.
	Good int `json:"good"`
	Bad  int `json:"bad"`
	// Updated is when the last verdict arrived, which is what lets a stale score be seen
	// as stale.
	Updated time.Time `json:"updated"`
	// Notes is what the user SAID about the verdicts, most recent last.
	//
	// A number says a skill did not work; it cannot say why, and "why" is the only thing that
	// makes a skill fixable. With the user's own words attached, the agent can read the
	// procedure, see which step the note is about, and rewrite it — turning a skill that failed
	// into one that works, which is worth far more than ranking it lower forever.
	Notes []Note `json:"notes,omitempty"`
}

// Note is one piece of feedback the user wrote with a verdict.
type Note struct {
	At   time.Time `json:"at"`
	Good bool      `json:"good"`
	// Text is the user's own words. It is the highest-trust context in the system: it comes
	// from the person who knows what they wanted, and it is quoted to the model verbatim.
	Text string `json:"text,omitempty"`
	// Addressed means the skill was SAVED after this note arrived — the fix was attempted.
	//
	// It is set automatically rather than by asking, because "the procedure was rewritten" is
	// the observable event. Whether the fix worked is answered by the next verdict, not by a
	// checkbox.
	Addressed bool `json:"addressed,omitempty"`
}

// MaxNotes is how many notes are kept per skill: the recent ones are what the agent can act
// on, and the ledger must not grow without bound as verdicts accumulate.
const MaxNotes = 5

// Unaddressed returns the COMPLAINTS that no fix has been attempted for, most recent first.
//
// Only bad verdicts qualify. A note attached to a good verdict is a comment — "this worked
// because of X" — and offering it as a complaint would tell the agent to repair a procedure
// that just did its job. The note is still kept: it is history, and the report shows it.
func (s Score) Unaddressed() []Note {
	var out []Note
	for i := len(s.Notes) - 1; i >= 0; i-- {
		if !s.Notes[i].Addressed && !s.Notes[i].Good {
			out = append(out, s.Notes[i])
		}
	}
	return out
}

// Complaints returns every bad-verdict note, fixed or not, most recent first. It is what a
// report shows; Unaddressed is what the agent is asked to act on.
func (s Score) Complaints() []Note {
	var out []Note
	for i := len(s.Notes) - 1; i >= 0; i-- {
		if !s.Notes[i].Good {
			out = append(out, s.Notes[i])
		}
	}
	return out
}

// Recent returns up to n notes, most recent first.
func (s Score) Recent(n int) []Note {
	var out []Note
	for i := len(s.Notes) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, s.Notes[i])
	}
	return out
}

// noteCap bounds one note. A verdict is a sentence, not a report: past this it is a document,
// and it would crowd the skill's own procedure out of the context it is read in.
const noteCap = 500

// TrimNote bounds a note, trimming on a rune boundary so it stays valid UTF-8.
//
// It is exported so the bound can be tested directly: going through Attribute exercises it
// only for notes long enough to be trimmed, and a branch reached by accident is a branch that
// stops being tested the moment the cap changes.
func TrimNote(s string) string {
	s = collapse(s)
	if len([]rune(s)) <= noteCap {
		return s
	}
	return string([]rune(s)[:noteCap]) + "…"
}

// collapse folds whitespace to single spaces, so a note stays one line wherever it is shown.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// Uses is how many verdicts this skill has taken part in.
func (s Score) Uses() int { return s.Good + s.Bad }

// Ledger is the set of scores, stored as a file next to the library.
//
// It lives in its own file rather than inside each skill's markdown because the body of a
// skill is text the MODEL writes and rewrites: embedding state in it would mean the agent's
// next save silently wipes the history, and parsing prose to find numbers is the kind of
// coupling that breaks on the first document that deviates.
type Ledger struct {
	// Path is the file the scores are stored in. Empty means an in-memory ledger, which is
	// what tests use and what an embedder gets if it does not want persistence.
	Path string

	// Scores is keyed by the sanitised skill name.
	Scores map[string]Score `json:"scores"`
	// Now is the clock, injected so a test can age a value without waiting.
	Now func() time.Time `json:"-"`

	// dirty tracks whether anything moved since the last save, so a verdict that changed
	// nothing does not rewrite the file.
	dirty bool
}

// ErrNoSkill is returned when a verdict names no skill, which is a normal answer rather than
// a failure: it means the turn did not consult the library.
var ErrNoSkill = errors.New("no skill took part in this turn")

// Open loads the ledger at path, or returns an empty one if there is nothing there.
//
// A ledger that cannot be read is reported rather than silently replaced: losing the history
// would be the whole value of the feature gone, and starting empty without saying so would
// hide that it happened.
func Open(path string) (*Ledger, error) {
	l := &Ledger{Path: path, Scores: map[string]Score{}, Now: time.Now}
	if path == "" {
		return l, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, fmt.Errorf("could not read the reward ledger %s: %w", path, err)
	}
	if len(data) == 0 {
		return l, nil
	}
	if err := json.Unmarshal(data, l); err != nil {
		return nil, fmt.Errorf("the reward ledger %s is not valid JSON: %w", path, err)
	}
	if l.Scores == nil {
		l.Scores = map[string]Score{}
	}
	return l, nil
}

// Get returns the score for a skill, and whether it has any history at all.
//
// A skill with no history is reported as absent rather than as a zero: the two mean different
// things, and showing "0.0" for "never used" would read as "this failed".
func (l *Ledger) Get(name string) (Score, bool) {
	s, ok := l.Scores[name]
	return s, ok
}

// Value returns the score of a skill and whether it has any history.
//
// The second return is not decoration: the library uses it to tell "never used" from "used and
// scored zero", and those need different treatment — the first should not be ranked as if it
// had failed.
func (l *Ledger) Value(name string) (float64, bool) {
	s, ok := l.Scores[name]
	if !ok {
		return 0, false
	}
	return s.Value, true
}

// ValueOf is Value for callers that only want the number, with 0 for "no history".
func (l *Ledger) ValueOf(name string) float64 {
	v, _ := l.Value(name)
	return v
}

// Attribute records one verdict over the skills that took part in a turn.
//
// Credit is split in proportion to how often each skill was consulted, so a turn that read
// three skills does not give each of them the same credit as a turn that leaned on one. The
// shares always sum to exactly one, so a verdict moves the total value by the same amount
// whatever the number of skills involved — otherwise marking turns with long tool chains
// would inflate the ledger.
//
// An empty list is ErrNoSkill, not a silent success: the caller has to say what it does when
// a turn consulted nothing, and pretending a verdict landed would make the feature look like
// it works when it cannot.
func (l *Ledger) Attribute(names []string, counts map[string]int, good bool, note string) error {
	if len(names) == 0 {
		return ErrNoSkill
	}
	if l.Now == nil {
		l.Now = time.Now
	}

	total := 0
	for _, n := range names {
		c := counts[n]
		if c < 1 {
			c = 1 // a skill that was named took part at least once
		}
		total += c
	}
	// No zero-total guard is needed: every count is floored at 1, so a non-empty list always
	// has total >= len(names) >= 1. A branch for it would be unreachable, and the floor above
	// is the rule that makes it so.

	signal := -1.0
	if good {
		signal = 1.0
	}
	now := l.Now()

	for _, n := range names {
		c := counts[n]
		if c < 1 {
			c = 1
		}
		share := float64(c) / float64(total)
		s := l.Scores[n]
		// The signal is scaled by the share, so the credit is split rather than duplicated.
		s.Value = DECAY*s.Value + (1-DECAY)*signal*share
		if good {
			s.Good++
		} else {
			s.Bad++
		}
		s.Updated = now
		// The note is attached to EVERY skill the turn used, not only to one of them: the
		// user is describing what went wrong with the turn, and which of the skills it read
		// is at fault is exactly what is not known yet. Each skill carries the evidence, and
		// the score decides which one is actually suspect.
		if trimmed := TrimNote(note); trimmed != "" {
			s.Notes = append(s.Notes, Note{At: now, Good: good, Text: trimmed})
			if len(s.Notes) > MaxNotes {
				s.Notes = s.Notes[len(s.Notes)-MaxNotes:]
			}
		}
		l.Scores[n] = s
	}
	l.dirty = true
	return nil
}

// Addressed records that a skill was rewritten after the notes it carries.
//
// It is what stops the agent from being nagged with the same complaint forever: a note whose
// fix has already been attempted is no longer offered as work to do. Whether the attempt
// succeeded is a separate question, and it is answered by the next verdict — the score moves
// either way, and the note stops being handed out as soon as the rewrite happened.
//
// It reports whether anything changed, so a caller can tell "marked" from "there was nothing
// to mark" without a second query.
func (l *Ledger) Addressed(name string) bool {
	s, ok := l.Scores[name]
	if !ok {
		return false
	}
	changed := false
	for i := range s.Notes {
		if !s.Notes[i].Addressed {
			s.Notes[i].Addressed = true
			changed = true
		}
	}
	if !changed {
		return false
	}
	l.Scores[name] = s
	l.dirty = true
	return true
}

// Outstanding returns the skills with feedback that no fix has attempted, worst value first.
//
// This is the work queue the feature exists for: a skill that failed and has the user's
// explanation attached is a concrete, bounded thing the agent can repair, and it is worth
// more than any amount of re-ranking.
func (l *Ledger) Outstanding() []struct {
	Name  string
	Score Score
} {
	var out []struct {
		Name  string
		Score Score
	}
	for _, entry := range l.Sorted() {
		if len(entry.Score.Unaddressed()) > 0 {
			out = append(out, entry)
		}
	}
	return out
}

// Save writes the ledger, atomically and only when something changed.
//
// Atomic because a half-written ledger is a corrupt one, and this file is the only record of
// what the user thought of the library — losing it silently would reset months of verdicts.
func (l *Ledger) Save() error {
	if l.Path == "" || !l.dirty {
		return nil
	}
	if err := mkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return fmt.Errorf("could not create the ledger directory: %w", err)
	}
	// The encode cannot fail: every field is a string, an int, a float or a time, and none of
	// those can produce a value the encoder rejects. A branch here would be unreachable, so
	// the error is ignored rather than reported as a case nobody can ever see.
	data, _ := json.MarshalIndent(l, "", "  ")
	data = append(data, '\n')

	tmp, err := createTemp(filepath.Dir(l.Path), ".scores.*.tmp")
	if err != nil {
		return fmt.Errorf("could not create the ledger file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := writeLedger(tmp, data); err != nil {
		tmp.Close()
		return fmt.Errorf("could not write the ledger: %w", err)
	}
	if err := closeLedger(tmp); err != nil {
		return fmt.Errorf("could not close the ledger: %w", err)
	}
	if err := renameFile(tmp.Name(), l.Path); err != nil {
		return fmt.Errorf("could not install the ledger: %w", err)
	}
	l.dirty = false
	return nil
}

// Sorted returns the scores by value, best first, for a human reading the file.
func (l *Ledger) Sorted() []struct {
	Name  string
	Score Score
} {
	out := make([]struct {
		Name  string
		Score Score
	}, 0, len(l.Scores))
	for n, s := range l.Scores {
		out = append(out, struct {
			Name  string
			Score Score
		}{n, s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score.Value != out[j].Score.Value {
			return out[i].Score.Value > out[j].Score.Value
		}
		return out[i].Name < out[j].Name
	})
	return out
}
