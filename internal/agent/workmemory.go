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
	"path/filepath"
	"strconv"
	"strings"

	"github.com/madkoding/motita/internal/readonly"
)

const (
	// evidenceEach caps one kept read. A 550-line source file is about 25 KB; the cap keeps
	// the head and the tail of anything larger rather than dropping it.
	evidenceEach = 48000
	// evidenceTotal caps everything kept, newest first: the prompt must not grow with the
	// number of files a long task has looked at.
	evidenceTotal = 160000
	// notesMax caps the model's notes.
	notesMax = 4000
)

// readRecord is one read whose output is kept.
type readRecord struct {
	round  int
	output string
	// path and from..to say which lines of which file this read holds, when it was a plain
	// file read (cat, sed -n 'A,Bp', head). They are what lets a DIFFERENT command that asks
	// for lines already held be answered from memory instead of being run again.
	path     string
	from, to int
}

// workMemory is what a run remembers besides the journal. The zero value is empty and usable.
type workMemory struct {
	reads map[string]readRecord
	order []string // keys of reads, oldest first
	notes string
	// fp is the fingerprint of the working tree as of the last check, and fpOK whether it could
	// be taken. A command that might write only invalidates the kept reads when the tree
	// really differs from this: `npm install` and `go test` run and change nothing the reads
	// depend on.
	fp   string
	fpOK bool
	// verif is the last run of every check the run executed itself (see verify.go).
	verif map[string]verifRecord
	// executed and recalled count, for the round in progress, the commands that ran and the
	// reads answered from memory. The loop resets them each round.
	executed, recalled int
}

// readKey is the identity of a command: the same words in the same order.
func readKey(command string) string {
	return strings.Join(strings.Fields(command), " ")
}

// layout says which kept reads the prompt actually shows, and which of those are shown in full.
// Newest first, each capped at evidenceEach, all capped at evidenceTotal. It is the single place
// that decides this, because "what the model was shown" and "what may be answered from memory"
// must be the same set: telling the model a read is "under WHAT YOU HAVE ALREADY READ" when the
// prompt dropped it to fit is how a run ends up unable to see a file it is forbidden to re-read.
func (m *workMemory) layout() (shown, whole map[string]bool) {
	shown, whole = map[string]bool{}, map[string]bool{}
	left := evidenceTotal
	for i := len(m.order) - 1; i >= 0 && left > 0; i-- {
		key := m.order[i]
		out := m.reads[key].output
		limit := min(evidenceEach, left)
		shown[key] = true
		whole[key] = len(out) <= limit
		left -= min(len(out), limit)
	}
	return shown, whole
}

// recall returns the kept output of a read that already answers this command, if nothing has
// been written since. The same words in the same order always do; and so does a plain file read
// whose lines are ALL inside a read still held: `cat f` after `sed -n '1,400p' f`, or
// `sed -n '10,50p' f` after `cat -n f`. Measured on a real session, one 550-line file was read
// eight times under six spellings, and the exact-text match recognised none of the repeats.
func (m *workMemory) recall(command string) (readRecord, bool) {
	key := readKey(command)
	shown, whole := m.layout()
	// The identical command is answered when the prompt still SHOWS it (a huge output is shown
	// head and tail, and running it again would show the same head and tail). One the prompt
	// dropped to make room is not "already read" any more, so it runs again and goes to the front.
	if rec, ok := m.reads[key]; ok && shown[key] {
		return rec, true
	}
	path, from, to, ok := fileRead(command)
	if !ok {
		return readRecord{}, false
	}
	// Containment is only sound for what the model was actually shown IN FULL.
	var best readRecord
	found := false
	for k, rec := range m.reads {
		if !whole[k] || rec.path != path || rec.from > from || rec.to < to {
			continue
		}
		if !found || rec.round > best.round {
			best, found = rec, true
		}
	}
	return best, found
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
	path, from, to, _ := fileRead(command)
	// A range that asked for more lines than came back reached the end of the file, so it holds
	// the whole of it: `sed -n '1,400p' f` on a 300-line file answers a later `cat f`.
	if path != "" && to != wholeFile && strings.Count(strings.TrimRight(output, "\n"), "\n")+1 < to-from+1 {
		to = wholeFile
	}
	m.reads[key] = readRecord{round: round, output: output, path: path, from: from, to: to}
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

// sync compares the working tree with the one last seen and drops every kept read when it
// differs, or when it cannot be told. It reports whether the reads were dropped.
//
// It replaces "this command LOOKS like a write, so forget everything": `npm install`, `go test`
// and `git status` all look like writes, run in every long task, and changed none of the files
// the model had read - and each one cost the run its whole evidence, which it then re-read.
func (m *workMemory) sync(dir string) bool {
	cur, ok := treeFingerprint(dir, false)
	changed := !ok || !m.fpOK || cur != m.fp
	m.fp, m.fpOK = cur, ok
	if changed {
		m.invalidate()
	}
	return changed
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

// wholeFile is the upper bound of a read of the entire file.
const wholeFile = int(^uint(0) >> 1)

// fileRead recognises a line that does nothing but print part of one file, and says which part:
// `cat f`, `cat -n f`, `head -n N f`, `head -N f` and `sed -n 'A,Bp' f` (also `Ap`). Anything
// else - a pipe, a redirection, several files, a flag it does not know - is not a file read and
// gets no range, so it can only ever be matched by its exact words. It errs toward "not
// recognised", which costs one extra execution and never a wrong answer.
func fileRead(command string) (path string, from, to int, ok bool) {
	if strings.ContainsAny(command, "|&;<>`$(){}*?[]\n") {
		return "", 0, 0, false
	}
	f := strings.Fields(command)
	if len(f) < 2 {
		return "", 0, 0, false
	}
	unq := func(s string) string { return strings.Trim(s, "'\"") }
	switch f[0] {
	case "cat":
		args := f[1:]
		if args[0] == "-n" {
			args = args[1:]
		}
		if len(args) != 1 || strings.HasPrefix(args[0], "-") {
			return "", 0, 0, false
		}
		return cleanPath(unq(args[0])), 1, wholeFile, true
	case "head":
		args := f[1:]
		n := 10
		switch {
		case len(args) == 3 && args[0] == "-n":
			v, err := strconv.Atoi(args[1])
			if err != nil || v < 1 {
				return "", 0, 0, false
			}
			n, args = v, args[2:]
		case len(args) == 2 && len(args[0]) > 1 && args[0][0] == '-':
			v, err := strconv.Atoi(args[0][1:])
			if err != nil || v < 1 {
				return "", 0, 0, false
			}
			n, args = v, args[1:]
		}
		if len(args) != 1 || strings.HasPrefix(args[0], "-") {
			return "", 0, 0, false
		}
		return cleanPath(unq(args[0])), 1, n, true
	case "sed":
		if len(f) != 4 || f[1] != "-n" {
			return "", 0, 0, false
		}
		script := unq(f[2])
		if !strings.HasSuffix(script, "p") {
			return "", 0, 0, false
		}
		span := strings.TrimSuffix(script, "p")
		lo, hi, found := strings.Cut(span, ",")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 1 {
			return "", 0, 0, false
		}
		b := a
		if found {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				return "", 0, 0, false
			}
		}
		return cleanPath(unq(f[3])), a, b, true
	}
	return "", 0, 0, false
}

func cleanPath(p string) string { return filepath.Clean(p) }
