package agent

// THE WORKING MEMORY of a run.
//
// Reported from real use: a session on a real project ran past 100 rounds and never
// finished. The log shows why: `cat -n lib/api.ts` was executed 69 times, `lib/types.ts` 64
// times and `components/admin-page.tsx` 60 times. The model was not being careless. The
// journal it reads each round keeps only the last keepRoundsInFull rounds in full, cuts each
// one's output at 3000 bytes, and shrinks the rest to a line with the command alone. A file
// read in round 5 had vanished by round 12, so the model - correctly - read it again, and
// forgot it again. Every round bought a re-read and nothing else.
//
// Two things fix that, and both live here:
//
//   - EVIDENCE. The output of a command that only READS is kept whole, keyed by the
//     command, and shown to every later round under its own heading, outside the journal's
//     compaction. A read that would return the same thing (nothing has been written since)
//     is not run again: the model is told where the answer is. A write invalidates every
//     kept read, because after it the files may say something else.
//   - NOTES. The model may write down what it has decided and what is left. They are
//     replaced whole each time, so they stay short, and they are shown every round.

import (
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/readonly"
)

const (
	// evidenceEach caps one kept read. A 550-line source file is about 25 KB; the cap keeps
	// the head and the tail of anything larger rather than dropping it.
	evidenceEach = 24000
	// evidenceTotal caps everything kept, newest first: the prompt must not grow with the
	// number of files a long task has looked at.
	evidenceTotal = 90000
	// notesMax caps the model's notes.
	notesMax = 4000
)

// readRecord is one read whose output is kept.
type readRecord struct {
	round  int
	output string
}

// workMemory is what a run remembers besides the journal. The zero value is empty and usable.
type workMemory struct {
	reads map[string]readRecord
	order []string // keys of reads, oldest first
	notes string
	// executed and recalled count, for the round in progress, the commands that ran and the
	// reads answered from memory. The loop resets them each round.
	executed, recalled int
}

// readKey is the identity of a command: the same words in the same order.
func readKey(command string) string {
	return strings.Join(strings.Fields(command), " ")
}

// recall returns the kept output of an identical read, if nothing has been written since.
func (m *workMemory) recall(command string) (readRecord, bool) {
	rec, ok := m.reads[readKey(command)]
	return rec, ok
}

// remember keeps the output of a read. A read that is seen again moves to the newest place.
func (m *workMemory) remember(command string, round int, output string) {
	if m.reads == nil {
		m.reads = map[string]readRecord{}
	}
	key := readKey(command)
	if _, seen := m.reads[key]; seen {
		m.forget(key)
	}
	m.reads[key] = readRecord{round: round, output: output}
	m.order = append(m.order, key)
}

func (m *workMemory) forget(key string) {
	delete(m.reads, key)
	for i, k := range m.order {
		if k == key {
			m.order = append(m.order[:i], m.order[i+1:]...)
			return
		}
	}
}

// invalidate drops every kept read: something was written, so none of them can be trusted.
func (m *workMemory) invalidate() {
	m.reads = nil
	m.order = nil
}

// setNotes replaces the notes. Blank input leaves them as they were: a reply that carries no
// notes is not a request to forget them.
func (m *workMemory) setNotes(s string) {
	if s = strings.TrimSpace(s); s != "" {
		m.notes = truncate(s, notesMax)
	}
}

// render is what the execute phase reads next to the journal. It is empty when there is
// nothing to show, so a first round is unchanged.
func (m *workMemory) render() string {
	var b strings.Builder
	if m.notes != "" {
		b.WriteString("\n## YOUR NOTES\nWritten by you in earlier rounds. They are yours to keep " +
			"current: send \"notes\" again to replace them.\n")
		b.WriteString(m.notes)
		b.WriteString("\n")
	}
	if len(m.order) > 0 {
		b.WriteString("\n## WHAT YOU HAVE ALREADY READ\nThe full output of read-only commands run " +
			"earlier and still valid: nothing has been changed since. It is HERE, so do not run " +
			"these commands again - a repeat is not executed.\n")
		left := evidenceTotal
		for i := len(m.order) - 1; i >= 0 && left > 0; i-- {
			key := m.order[i]
			rec := m.reads[key]
			out := truncateMiddle(rec.output, min(evidenceEach, left))
			left -= len(out)
			fmt.Fprintf(&b, "\n### (round %d) $ %s\n%s\n", rec.round, key, out)
		}
	}
	return b.String()
}

// readsOnly reports whether a shell line cannot change anything. It is conservative on
// purpose: a line it cannot vouch for is treated as a write, which costs one extra
// execution and never a stale answer.
//
// The per-program judgement is NOT here. It used to be a second reader list of its own —
// `readerPrograms` with `git`, `sed`, `find` and `awk` special-cased inline — and having two
// lists meant having two answers: this one said `sed -n '1,300p' file` could not change
// anything and the policy said it could, so the same line was refused mid-run and, on the
// rounds where the model was not asked about it, not even remembered. readonly.Classify is
// now the single answer.
func readsOnly(line string) bool {
	line = strings.NewReplacer("2>/dev/null", " ", "2>&1", " ").Replace(line)
	var quote rune
	var seg strings.Builder
	var segs []string
	flush := func() {
		segs = append(segs, seg.String())
		seg.Reset()
	}
	rs := []rune(line)
	for i, r := range rs {
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			}
			seg.WriteRune(r)
		case quote == '"':
			if r == '"' {
				quote = 0
			}
			if r == '`' || (r == '$' && i+1 < len(rs) && rs[i+1] == '(') {
				return false
			}
			seg.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			seg.WriteRune(r)
		case r == '>' || r == '<' || r == '`' || (r == '$' && i+1 < len(rs) && rs[i+1] == '('):
			return false
		case r == ';' || r == '&' || r == '|' || r == '\n':
			flush()
		default:
			seg.WriteRune(r)
		}
	}
	flush()
	seen := false
	for _, s := range segs {
		f := strings.Fields(s)
		if len(f) == 0 {
			continue
		}
		seen = true
		// The PROGRAM-and-its-arguments decision is the shared one, in readonly, so the
		// memory that decides whether a read may be replayed and the policy that decides
		// whether a line may run cannot disagree — they already did: both treated `sed`
		// as a writer whatever its arguments said, so `sed -n '1,300p' file` was never
		// remembered, and both were silent about `sort -o out.txt`, which writes. The
		// metacharacters above are checked first because they are what this function
		// knows and readonly, by construction, never sees.
		if kind, _ := readonly.Classify(f[0], f[1:]); kind != readonly.KindReader {
			return false
		}
	}
	return seen
}

// isRead reports whether an action is a plain shell command that only reads.
func isRead(c Command) bool {
	kind := strings.ToLower(strings.TrimSpace(c.Kind))
	return (kind == "" || kind == "command") && readsOnly(c.Command)
}

// isWrite reports whether an action can change the checkout: a shell command that is not
// provably a reader. A library action (search_skills, read_skill...) touches no project file.
func isWrite(c Command) bool {
	kind := strings.ToLower(strings.TrimSpace(c.Kind))
	if kind != "" && kind != "command" {
		return false
	}
	return strings.TrimSpace(c.Command) != "" && !readsOnly(c.Command)
}
