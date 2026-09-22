// Package skills gives the agent a library of procedures it can look up and extend.
//
// A skill is a document that describes HOW to do a kind of work: the steps, the pitfalls
// already paid for, the commands that are known to work. The model is told it has this
// library and that looking things up is expected — a procedure written down once is worth
// more than the same discovery made again on every task.
//
// Skills are files, not prompt text. They are never inlined into the system prompt: that
// would spend the context of every task on procedures most tasks do not need, and it would
// make the library unfixable from inside the session. The model asks for one when it needs
// it, and can write a new one when it learns something.
package skills

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// builtinFS holds the procedures that ship with the program.
//
// They are embedded rather than written from a string constant so the document keeps its own
// file — syntax highlighting, a diff that shows prose, and no Go escaping of markdown — while
// still travelling inside the binary. A skill is prose, and prose in a Go string literal is
// prose nobody edits.
//
//go:embed builtin/*.md
var builtinFS embed.FS

// builtinReadDir and builtinReadFile are the two filesystem calls whose failure a test cannot
// arrange from the outside: an embed.FS lives in the binary, and there is no way to make one of
// its reads fail by arranging a directory. They are seams, the same pattern the rest of this
// package uses for the calls that only fail on a broken disk — an embedded set that cannot be
// read is a broken build, and the handling for it has to be exercised rather than hoped for.
var (
	builtinReadDir  = builtinFS.ReadDir
	builtinReadFile = builtinFS.ReadFile
)

// Skill is one document in the library.
type Skill struct {
	// Name is the file name without its extension, and the handle the model uses.
	Name string
	// Title is the first heading, which is what a human recognises.
	Title string
	// Summary is the first non-heading paragraph: what the skill is for.
	Summary string
	// Path is where it lives, so the model can be told what it read.
	Path string
	// Body is the whole document, filled in by Load and left empty by the index.
	Body string
}

// Scorer is the long-term value of a skill, as the library needs it.
//
// It is an interface and not the reward package itself so the library keeps no dependency on
// how the value is produced. All the library asks is "how has this one worked out", and a
// nil Scorer means the feature is off and the ordering is exactly what it was.
type Scorer interface {
	// Value returns the accumulated value of a skill, and whether it has any history.
	Value(name string) (float64, bool)
}

// Library is a directory of skill documents.
type Library struct {
	// Dir is where the documents live. It is created on the first write, so a session can
	// teach the agent something without anyone preparing the directory first.
	Dir string
	// Scorer, when set, breaks ties between equally relevant skills by how well each has
	// worked out. It never overrides relevance: see Search for why that order matters.
	Scorer Scorer
	// MaxFileBytes caps a single document. A skill is a procedure, not an archive: past this
	// size it is either a data file or it needs splitting, and reading it into the context
	// would cost more than it returns.
	MaxFileBytes int
	// Builtins includes the procedures embedded in the binary — the ones the program ships
	// with, as opposed to the ones a session has written down.
	//
	// They are READ-ONLY and they are shadowed, not merged: a document in Dir with the same
	// name wins, so a user who wants to correct one writes their own rather than being
	// overruled by the binary. Deleting that document brings the shipped one back, which is
	// the honest behaviour for a procedure nobody can lose.
	//
	// It is off in New so a library is exactly the directory it was given: a test that asks
	// what is in a directory must not be answered with what is in the executable.
	Builtins bool
}

// DefaultMaxFileBytes is the cap when none is configured: enough for a thorough procedure,
// far short of anything that would crowd out the conversation.
const DefaultMaxFileBytes = 64 * 1024

// The two filesystem calls whose failure branches a test cannot reach from the outside are
// seams, the same pattern the rest of the repository uses: a temporary file that cannot be
// created and a rename that fails are disk problems, and the handling for them has to be
// exercised rather than hoped for. Everything else is reached by arranging the real
// directory — a broken symlink, a read-only parent, a directory where a file should be.
var (
	createTemp = os.CreateTemp
	renameFile = os.Rename
	writeTemp  = func(f *os.File, body string) (int, error) { return f.WriteString(body) }
	closeTemp  = func(f *os.File) error { return f.Close() }
	// readBody is the read the search performs, kept as a seam because the index has already
	// read every document by the time a search runs: the only way a body can fail to load at
	// that point is a file that changed underneath the process, which is exactly the case the
	// skip branch exists for.
	readBody = func(l *Library, path string) (string, error) { return l.read(path) }
)

// ErrNotFound is returned when no skill matches a query, and it is a distinct error because
// the caller says something different for it: "no skill matches" is a normal answer that
// invites the model to proceed on its own, while an I/O failure is a problem to report.
var ErrNotFound = errors.New("no skill matches")

// New returns a library rooted at a directory.
func New(dir string) *Library {
	return &Library{Dir: dir, MaxFileBytes: DefaultMaxFileBytes}
}

// Name is the identifier a skill is addressed by: lower case, no spaces, no extension.
//
// It is sanitised rather than rejected because the name comes from a model: a name with a
// slash or a pair of dots would let a write escape the directory, and refusing the whole
// call over a space would be a worse experience than normalising it.
func Name(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	// Strip any extension the caller included.
	name = strings.TrimSuffix(name, ".md")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == '/':
			// A space, a dot or a slash becomes a separator, never a path element.
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return ""
	}
	// A name is a handle in a prompt, so it stays short enough to read in a list.
	if len(out) > 64 {
		out = out[:64]
	}
	return strings.Trim(out, "-")
}

// path is where a skill named name lives. The sanitised name cannot contain a separator, so
// the result is always inside the directory.
func (l *Library) path(name string) string {
	return filepath.Join(l.Dir, name+".md")
}

// builtinDir is the folder inside the embedded filesystem. It is not a valid skill name, so a
// document a session writes here can never collide with a path inside it.
const builtinDir = "builtin"

// builtinSkills returns the embedded procedures, parsed and keyed by name.
//
// It reads on every call rather than caching: an embed.FS serves from memory, the set is small,
// and a cache would be one more thing to invalidate for no measured gain.
func builtinSkills() (map[string]Skill, error) {
	entries, err := builtinReadDir(builtinDir)
	if err != nil {
		return nil, fmt.Errorf("could not read the built-in skills: %w", err)
	}
	out := make(map[string]Skill, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := builtinReadFile(builtinDir + "/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("could not read the built-in skill %s: %w", e.Name(), err)
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		s := parse(name, "builtin:"+e.Name(), string(data))
		s.Body = string(data)
		out[name] = s
	}
	return out, nil
}

// builtin returns one embedded skill, and whether the library is serving them at all.
func (l *Library) builtin(name string) (Skill, bool, error) {
	if !l.Builtins {
		return Skill{}, false, nil
	}
	all, err := builtinSkills()
	if err != nil {
		return Skill{}, false, err
	}
	s, ok := all[name]
	return s, ok, nil
}

// Search finds the skills whose name, title, summary or body match a query, best first.
//
// Matching the BODY as well as the headings is deliberate: a user asking "how do I handle a
// flaky serial port" has no idea what the skill was called, and a title-only search would
// find nothing. The ranking puts a name match first, then a title, then a summary, then the
// body, so a document that is about the topic outranks one that merely mentions it.
func (l *Library) Search(query string, limit int) ([]Skill, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil, errors.New("the query is empty")
	}
	// The query is matched WORD BY WORD, not as one literal phrase.
	//
	// The model is told to describe the work in plain language, and a description is a
	// sentence: "flash a board over usb" appears verbatim in no document, while the words do.
	// A substring search over the whole phrase would answer that query with nothing, which is
	// the worst possible answer — it tells the model the library has nothing when it has the
	// procedure it needs. Matching per word and ranking by how many matched finds it.
	words := queryWords(needle)
	if limit <= 0 {
		limit = 10
	}

	all, err := l.index()
	if err != nil {
		return nil, err
	}

	type scored struct {
		skill   Skill
		tier    int // which field matched: 0 name, 1 title, 2 summary, 3 body
		first   int // index of the earliest query word that matched, for an intra-tier tie
		matched int // how many of the query's words appear in the document
		// value is the long-term verdict on this skill, and it is the LAST tie-break.
		value   float64
		hasHist bool
	}
	var hits []scored
	for _, s := range all {
		body, err := l.body(s)
		if err != nil {
			// A document that cannot be read is skipped rather than failing the search: one
			// unreadable file must not make the whole library unusable.
			continue
		}
		s.Body = body

		// Two numbers decide the order, and both are small integers so the comparison is
		// obvious: which field matched (a name beats a title beats a summary beats the body),
		// and how much of the query the document covers.
		tier, first := 4, 0
		switch {
		case anyWordMatches(s.Name, words):
			tier, first = 0, fieldMatch(s.Name, words)
		case anyWordMatches(s.Title, words):
			tier, first = 1, fieldMatch(s.Title, words)
		case anyWordMatches(s.Summary, words):
			tier, first = 2, fieldMatch(s.Summary, words)
		case anyWordMatches(body, words):
			tier, first = 3, fieldMatch(body, words)
		}
		if tier == 4 {
			// No word of the query appears anywhere: not a match.
			continue
		}
		h := scored{skill: s, tier: tier, first: first, matched: countMatches(s, words)}
		if l.Scorer != nil {
			h.value, h.hasHist = l.Scorer.Value(s.Name)
		}
		hits = append(hits, h)
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].tier != hits[j].tier {
			return hits[i].tier < hits[j].tier
		}
		// Within a field, the document that covered more of the query is the one being asked
		// for; an earlier matching word is a weaker signal and only breaks a remaining tie.
		if hits[i].matched != hits[j].matched {
			return hits[i].matched > hits[j].matched
		}
		if hits[i].first != hits[j].first {
			return hits[i].first < hits[j].first
		}
		// Only now does the accumulated value matter, and only between skills that are
		// otherwise equal.
		//
		// Relevance comes first on purpose: a skill that has always worked is still the wrong
		// answer to a question it is not about, and letting the score outrank the text would
		// make the search return whatever has been used most — the failure mode of every
		// popularity ranking. The score is a tie-break, which is exactly the case where the
		// words cannot tell two candidates apart and experience can.
		if hits[i].value != hits[j].value {
			return hits[i].value > hits[j].value
		}
		return hits[i].skill.Name < hits[j].skill.Name
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Skill, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.skill)
	}
	return out, nil
}

// Get returns one skill by name.
func (l *Library) Get(name string) (Skill, error) {
	n := Name(name)
	if n == "" {
		return Skill{}, fmt.Errorf("the skill name is empty")
	}
	p := l.path(n)
	if _, err := os.Stat(p); err != nil {
		// Not on disk: the embedded procedure of that name is the next answer, and only when
		// the library serves them. A name that is in neither is genuinely not found.
		if s, ok, berr := l.builtin(n); berr != nil {
			return Skill{}, berr
		} else if ok {
			return s, nil
		}
		return Skill{}, fmt.Errorf("%w: %q", ErrNotFound, n)
	}
	body, err := l.read(p)
	if err != nil {
		return Skill{}, err
	}
	s := parse(n, p, body)
	s.Body = body
	return s, nil
}

// Save writes a skill, creating or replacing it. It returns the skill as stored.
//
// Replacing is allowed on purpose: a procedure that turned out to be wrong is worse than no
// procedure, and the model has to be able to correct it. The alternative — append-only — is
// how a library accumulates the mistakes it was meant to prevent.
func (l *Library) Save(name, body string) (Skill, error) {
	n := Name(name)
	if n == "" {
		return Skill{}, errors.New("the skill name is empty")
	}
	if strings.TrimSpace(body) == "" {
		return Skill{}, errors.New("the skill body is empty")
	}
	if l.MaxFileBytes > 0 && len(body) > l.MaxFileBytes {
		return Skill{}, fmt.Errorf("the skill is %d bytes and the limit is %d: a procedure this size belongs in a data file, or it needs splitting", len(body), l.MaxFileBytes)
	}
	if !utf8.ValidString(body) {
		return Skill{}, errors.New("the skill body is not valid UTF-8")
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return Skill{}, fmt.Errorf("could not create the skills directory %s: %w", l.Dir, err)
	}

	// Written atomically: a session that dies mid-write must not leave a half document that
	// the next search reads as a procedure.
	tmp, err := createTemp(l.Dir, "."+n+".*.tmp")
	if err != nil {
		return Skill{}, fmt.Errorf("could not create the skill file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op once the rename has happened

	if _, err := writeTemp(tmp, body); err != nil {
		tmp.Close()
		return Skill{}, fmt.Errorf("could not write the skill: %w", err)
	}
	if err := closeTemp(tmp); err != nil {
		return Skill{}, fmt.Errorf("could not close the skill file: %w", err)
	}
	if err := renameFile(tmp.Name(), l.path(n)); err != nil {
		return Skill{}, fmt.Errorf("could not install the skill: %w", err)
	}

	s := parse(n, l.path(n), body)
	s.Body = body
	return s, nil
}

// builtinPrefix marks the path of an embedded document. It is not a filesystem path, so a
// document carrying it must never be handed to the disk.
const builtinPrefix = "builtin:"

// body returns a document's text, from the binary for an embedded one and from disk otherwise.
//
// The distinction is made on the path rather than on whether the body looks loaded, because an
// embedded document that happened to be empty would then be read from the disk under a path
// that does not exist — and the search would silently drop it.
func (l *Library) body(s Skill) (string, error) {
	if strings.HasPrefix(s.Path, builtinPrefix) {
		return s.Body, nil
	}
	// The read goes through the seam, which is how the vanished-document branch is exercised.
	return readBody(l, s.Path)
}

// List returns every skill's headings, without the bodies: it is what the model reads to
// decide what to look up, and loading every document to answer it would cost the whole
// library in context.
func (l *Library) List() ([]Skill, error) { return l.index() }

// index reads the directory and parses the headings of each document.
func (l *Library) index() ([]Skill, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			// An empty library is not a failure: it is where everyone starts.
			return nil, nil
		}
		return nil, fmt.Errorf("could not read the skills directory %s: %w", l.Dir, err)
	}

	var out []Skill
	seen := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		// A temporary file from an interrupted write is not a skill.
		if strings.HasPrefix(name, ".") {
			continue
		}
		p := filepath.Join(l.Dir, e.Name())
		body, err := l.read(p)
		if err != nil {
			continue
		}
		out = append(out, parse(name, p, body))
		seen[name] = true
	}

	// The embedded procedures come last, minus the ones a document here already named. The
	// document wins: a user who wrote their own is correcting the shipped one, and a library
	// that listed both would offer the model two answers of unequal quality under one name.
	if l.Builtins {
		built, err := builtinSkills()
		if err != nil {
			return nil, err
		}
		for name, s := range built {
			if seen[name] {
				continue
			}
			out = append(out, s)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// read loads a document, refusing anything past the cap rather than pulling it into memory
// and from there into the context.
func (l *Library) read(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if l.MaxFileBytes > 0 && info.Size() > int64(l.MaxFileBytes) {
		return "", fmt.Errorf("%s is %d bytes, past the %d-byte limit", path, info.Size(), l.MaxFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not valid UTF-8", path)
	}
	return string(data), nil
}

// parse extracts the title and the summary from a document.
//
// The title is the first heading and the summary is the first non-heading, non-empty line
// that follows it. Nothing more elaborate: a skill is prose the model wrote, and parsing it
// like a structured file would break on the first one that deviated.
func parse(name, path, body string) Skill {
	s := Skill{Name: name, Path: path}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if s.Title == "" {
				s.Title = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			}
			continue
		}
		// The first prose line is the summary, whether it follows a heading or stands on its
		// own: a document the model wrote without a heading is still a skill, and it needs a
		// line in the index like any other.
		s.Summary = trimmed
		break
	}
	if s.Title == "" {
		s.Title = name
	}
	if s.Summary == "" {
		s.Summary = "(no summary)"
	}
	return s
}

// queryWords splits a query into the words worth matching.
//
// Words shorter than three characters are dropped: "a", "of", "to" and "in" appear in every
// document, so matching them would rank the whole library equally and the ranking would carry
// no information. Two-character technical terms do suffer from this, which is the price of a
// rule that is otherwise right; a query about "go" is better phrased with the word around it.
func queryWords(lowered string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(lowered, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_'
	}) {
		if len([]rune(w)) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

// forms returns the spellings of a query word that count as the same word.
//
// This is not a stemmer, and deliberately so: a real one is a large table of rules and
// exceptions, and this is a lookup tool. It covers the case that actually happens — the model
// describes the work as "widgets" and the document says "widget" — with two rules, a trailing
// "s" and a trailing "es". Matching is only needed in that one direction, because "widget" is
// already a substring of "widgets": the query is the side that carries the inflection.
//
// The cost is a false positive on words that merely begin alike ("series" against "serial"),
// which can move a document up the order. That is a ranking error the model sees through when
// it reads the summaries, and the alternative — finding nothing for a plural — is a miss it
// cannot see through at all.
func forms(w string) []string {
	switch {
	case strings.HasSuffix(w, "es") && len(w) > 4:
		return []string{w, w[:len(w)-2]}
	case strings.HasSuffix(w, "s") && len(w) > 3:
		return []string{w, w[:len(w)-1]}
	}
	return []string{w}
}

// containsWord reports whether a lowered field contains the word in any of its forms.
func containsWord(loweredField, w string) bool {
	for _, f := range forms(w) {
		if strings.Contains(loweredField, f) {
			return true
		}
	}
	return false
}

// fieldMatch returns the index of the first query word the field contains, or -1 for none.
// The index is used to break ties: a field matching the first word the user thought of is
// closer to the intent than one matching the last.
func fieldMatch(field string, words []string) int {
	lowered := strings.ToLower(field)
	for i, w := range words {
		if containsWord(lowered, w) {
			return i
		}
	}
	return -1
}

// anyWordMatches reports whether any word at all appears in the field.
func anyWordMatches(field string, words []string) bool { return fieldMatch(field, words) >= 0 }

// countMatches is how much of the query a document covers, which is what to sort by once the
// field is equal: the skill that mentions four of the five words is the one being asked for.
func countMatches(s Skill, words []string) int {
	hay := strings.ToLower(s.Name + " " + s.Title + " " + s.Summary + " " + s.Body)
	n := 0
	for _, w := range words {
		if containsWord(hay, w) {
			n++
		}
	}
	return n
}
