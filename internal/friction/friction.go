// Package friction keeps the friction journal: a local record of what got in the way of the
// person using Motita - a run that failed, a step that had to be tried again, an action that was
// refused, an answer the person marked as not good - and the weekly summary that reads it back.
//
// It only RECORDS and READS. Nothing here changes a setting, a skill or a prompt: the journal is
// for the person (and for a later, deliberate improvement), never an input that rewrites the
// agent by itself.
//
// The journal stays on this computer, in one JSON-lines file. Nothing in it is sent anywhere, and
// every text is passed through the secret scrubber before it is written, so a key that was in a
// command or in a message never reaches the file.
package friction

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/redact"
)

// What got in the way. The names are the ones the file and the settings page carry.
const (
	// KindError is a run that ended with a failure.
	KindError = "error"
	// KindRetry is a step that did not pass and was tried again.
	KindRetry = "retry"
	// KindDenied is an action that was refused: by the person, or because nobody was there to ask.
	KindDenied = "denied"
	// KindFlagged is an answer the person marked as not good.
	KindFlagged = "flagged"
)

// Who refused an action, for a KindDenied entry.
const (
	// WhyPerson: the person answered no.
	WhyPerson = "person"
	// WhyNobody: a run with nobody at the keyboard (a scheduled one) had no one to ask.
	WhyNobody = "nobody"
)

// Kinds is every kind, in the order a summary lists them.
var Kinds = []string{KindError, KindRetry, KindDenied, KindFlagged}

// MaxEntries is how many entries the journal keeps: when it grows past it the oldest go. A
// journal that is never trimmed is a file that grows for as long as the program is used.
const MaxEntries = 2000

// textLimit is the most characters of any one text kept in an entry. Enough to recognise the
// situation, short enough that a pasted log does not end up in the journal.
const textLimit = 300

// Entry is one thing that got in the way.
type Entry struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	// Session is the id of the conversation it happened in; Project is its project, when it had one.
	Session string `json:"session,omitempty"`
	Project string `json:"project,omitempty"`
	// Turn is the conversation turn it belongs to, when it came from one.
	Turn int `json:"turn,omitempty"`
	// Why says who refused an action (WhyPerson, WhyNobody); empty for every other kind.
	Why string `json:"why,omitempty"`
	// Text is what happened, in one line: the error, the step that was retried, the refused command.
	Text string `json:"text"`
	// Ask, Answer and Note belong to a flagged answer: what the person asked, what the agent
	// answered, and the person's own words about it.
	Ask    string `json:"ask,omitempty"`
	Answer string `json:"answer,omitempty"`
	Note   string `json:"note,omitempty"`
	// Mode and Steps say how the flagged turn ran ("plan" or "task", how many steps), and Related
	// how many errors, retries and refusals the journal already had for that same turn: the
	// context that tells a bad answer after a smooth run from one after a struggle.
	Mode    string `json:"mode,omitempty"`
	Steps   int    `json:"steps,omitempty"`
	Related int    `json:"related,omitempty"`
}

// Journal is the friction journal. A journal with no path lives in memory only; a nil one drops
// everything it is given, so a caller never has to ask whether there is one.
type Journal struct {
	mu      sync.Mutex
	path    string
	entries []Entry
	// Now is the clock; a test sets it.
	Now func() time.Time
}

// Open loads the journal kept at path, or starts an empty one when the file is not there. An
// empty path is a journal in memory. A line that is not an entry is skipped: a journal that
// refused to open over one damaged line would lose all the others.
func Open(path string) (*Journal, error) {
	j := &Journal{path: path, Now: time.Now}
	if path == "" {
		return j, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return j, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the friction journal: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Kind != "" {
			j.entries = append(j.entries, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("could not read the friction journal: %w", err)
	}
	if len(j.entries) > MaxEntries {
		j.entries = j.entries[len(j.entries)-MaxEntries:]
	}
	return j, nil
}

// clean is a text as the journal keeps it: secrets masked, one line, cut at the limit.
func clean(s string) string {
	s = strings.Join(strings.Fields(redact.String(s)), " ")
	if r := []rune(s); len(r) > textLimit {
		s = string(r[:textLimit]) + "…"
	}
	return s
}

// Record writes one entry. Its texts are scrubbed and shortened first, and a missing time is now.
// An error is the disk's: the entry is still in memory, so the summary of this run of the program
// is complete even when the file could not be written.
func (j *Journal) Record(e Entry) error {
	if j == nil {
		return nil
	}
	e.Text, e.Ask, e.Answer, e.Note = clean(e.Text), clean(e.Ask), clean(e.Answer), clean(e.Note)
	e.Session, e.Project = clean(e.Session), clean(e.Project)
	j.mu.Lock()
	defer j.mu.Unlock()
	if e.At.IsZero() {
		e.At = j.Now()
	}
	j.entries = append(j.entries, e)
	if len(j.entries) > MaxEntries {
		j.entries = j.entries[len(j.entries)-MaxEntries:]
		return j.rewrite()
	}
	return j.append(e)
}

// append adds one line to the file.
func (j *Journal) append(e Entry) error {
	if j.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return fmt.Errorf("could not keep the friction journal: %w", err)
	}
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("could not keep the friction journal: %w", err)
	}
	line, _ := json.Marshal(e) // an Entry is strings and a time: it always encodes
	_, werr := f.Write(append(line, '\n'))
	if err := errors.Join(werr, f.Close()); err != nil {
		return fmt.Errorf("could not keep the friction journal: %w", err)
	}
	return nil
}

// rewrite replaces the file with the entries held, through a temporary file so a crash leaves
// the old journal and not half of the new one.
func (j *Journal) rewrite() error {
	if j.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return fmt.Errorf("could not keep the friction journal: %w", err)
	}
	var b strings.Builder
	for _, e := range j.entries {
		line, _ := json.Marshal(e)
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("could not keep the friction journal: %w", err)
	}
	if err := os.Rename(tmp, j.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("could not keep the friction journal: %w", err)
	}
	return nil
}

// Entries is a copy of the journal, oldest first.
func (j *Journal) Entries() []Entry {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Entry(nil), j.entries...)
}

// Related counts the entries of a session's turn, flagged ones not included.
func (j *Journal) Related(session string, turn int) int {
	n := 0
	for _, e := range j.Entries() {
		if e.Session == session && e.Turn == turn && e.Kind != KindFlagged {
			n++
		}
	}
	return n
}

// Clear forgets everything: the memory and the file.
func (j *Journal) Clear() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = nil
	if j.path == "" {
		return nil
	}
	if err := os.Remove(j.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not clear the friction journal: %w", err)
	}
	return nil
}
