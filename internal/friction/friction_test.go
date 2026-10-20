package friction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/i18n"
)

var epoch = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func memory(t *testing.T) *Journal {
	t.Helper()
	j, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	j.Now = func() time.Time { return epoch }
	return j
}

func TestRecordScrubsShortensAndStamps(t *testing.T) {
	j := memory(t)
	secret := "sk-" + strings.Repeat("a", 30)
	long := strings.Repeat("é", textLimit+50)
	if err := j.Record(Entry{Kind: KindError, Text: "deploy\nfailed with " + secret, Ask: long, Note: "  spaced   out ", Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	got := j.Entries()
	if len(got) != 1 {
		t.Fatalf("entries = %d", len(got))
	}
	e := got[0]
	if strings.Contains(e.Text, secret) || !strings.Contains(e.Text, "[REDACTED]") || strings.Contains(e.Text, "\n") {
		t.Errorf("text not scrubbed to one line: %q", e.Text)
	}
	if n := len([]rune(e.Ask)); n != textLimit+1 || !strings.HasSuffix(e.Ask, "…") {
		t.Errorf("ask not cut: %d runes", n)
	}
	if e.Note != "spaced out" || !e.At.Equal(epoch) {
		t.Errorf("note/at = %q %v", e.Note, e.At)
	}
}

func TestRecordKeepsAGivenTime(t *testing.T) {
	j := memory(t)
	at := epoch.Add(-time.Hour)
	_ = j.Record(Entry{Kind: KindRetry, Text: "x", At: at})
	if !j.Entries()[0].At.Equal(at) {
		t.Error("a time that was given was replaced")
	}
}

func TestRelatedCountsOneTurnOfOneSession(t *testing.T) {
	j := memory(t)
	for _, e := range []Entry{
		{Kind: KindError, Session: "a", Turn: 2}, {Kind: KindRetry, Session: "a", Turn: 2},
		{Kind: KindFlagged, Session: "a", Turn: 2}, {Kind: KindDenied, Session: "a", Turn: 3},
		{Kind: KindDenied, Session: "b", Turn: 2},
	} {
		_ = j.Record(e)
	}
	if got := j.Related("a", 2); got != 2 {
		t.Errorf("related = %d, want 2", got)
	}
	var none *Journal
	if none.Related("a", 2) != 0 {
		t.Error("a nil journal relates nothing")
	}
}

func TestNilJournalDropsEverything(t *testing.T) {
	var j *Journal
	if err := j.Record(Entry{Kind: KindError}); err != nil || j.Entries() != nil || j.Clear() != nil {
		t.Error("a nil journal must be a quiet no-op")
	}
}

func TestJournalSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "friction", "journal.jsonl")
	j, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{KindError, KindDenied} {
		if err := j.Record(Entry{Kind: k, Text: k}); err != nil {
			t.Fatal(err)
		}
	}
	again, err := Open(path)
	if err != nil || len(again.Entries()) != 2 {
		t.Fatalf("reopened: %v %v", again.Entries(), err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v: the journal is private", info.Mode().Perm())
	}
}

func TestOpenSkipsDamagedLinesAndTrims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.jsonl")
	var b strings.Builder
	b.WriteString("not json\n{\"text\":\"no kind\"}\n")
	for i := 0; i < MaxEntries+5; i++ {
		b.WriteString(`{"kind":"error","text":"e"}` + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := Open(path)
	if err != nil || len(j.Entries()) != MaxEntries {
		t.Fatalf("entries = %d, err = %v", len(j.Entries()), err)
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "file"), nil, 0o600)
	if _, err := Open(filepath.Join(dir, "file", "j.jsonl")); err == nil { // a file where the folder should be
		t.Error("an unreachable journal opened")
	}
	if _, err := Open(dir); err == nil { // a directory cannot be read as a file
		t.Error("a directory opened as a journal")
	}
	long := filepath.Join(dir, "long.jsonl")
	if err := os.WriteFile(long, []byte(strings.Repeat("x", 2<<20)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(long); err == nil { // a line past the scanner's limit
		t.Error("an oversized line did not fail")
	}
}

func TestRecordTrimsTheOldest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.jsonl")
	j, _ := Open(path)
	for i := 0; i < MaxEntries+3; i++ {
		if err := j.Record(Entry{Kind: KindRetry, Text: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(j.Entries()); n != MaxEntries {
		t.Fatalf("held %d", n)
	}
	again, _ := Open(path)
	if n := len(again.Entries()); n != MaxEntries {
		t.Fatalf("file holds %d", n)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
}

func TestRecordDiskFailuresKeepTheEntryInMemory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	_ = os.WriteFile(blocker, nil, 0o600)

	// The folder cannot be made: a file is where it should be.
	j := &Journal{path: filepath.Join(blocker, "sub", "j.jsonl"), Now: time.Now}
	if err := j.Record(Entry{Kind: KindError, Text: "x"}); err == nil || len(j.Entries()) != 1 {
		t.Errorf("append, no folder: err = %v, held %d", err, len(j.Entries()))
	}

	// The file cannot be opened: a directory is where it should be.
	asDir := filepath.Join(dir, "asdir")
	_ = os.Mkdir(asDir, 0o700)
	j = &Journal{path: asDir, Now: time.Now}
	if err := j.Record(Entry{Kind: KindError, Text: "x"}); err == nil {
		t.Error("append onto a directory did not fail")
	}

	// The same three failures on the trimming rewrite.
	full := func(path string) *Journal {
		j := &Journal{path: path, Now: time.Now, entries: make([]Entry, MaxEntries)}
		return j
	}
	if err := full(filepath.Join(blocker, "sub", "j.jsonl")).Record(Entry{Kind: KindError}); err == nil {
		t.Error("rewrite, no folder, did not fail")
	}
	if err := full(filepath.Join(dir, "gone", "..", "asdir")).Record(Entry{Kind: KindError}); err == nil {
		t.Error("rewrite onto a directory did not fail")
	}
	// A temporary file that cannot be written: a directory sits at its name.
	target := filepath.Join(dir, "j2.jsonl")
	_ = os.Mkdir(target+".tmp", 0o700)
	if err := full(target).Record(Entry{Kind: KindError}); err == nil {
		t.Error("rewrite with the temporary name taken did not fail")
	}
}

func TestRecordReportsAWriteThatFails(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full to write into")
	}
	j := &Journal{path: "/dev/full", Now: time.Now}
	if err := j.Record(Entry{Kind: KindError, Text: "x"}); err == nil {
		t.Error("a write that fails was reported as kept")
	}
}

func TestRewriteWithNoPathIsQuiet(t *testing.T) {
	j := &Journal{Now: time.Now, entries: make([]Entry, MaxEntries)}
	if err := j.Record(Entry{Kind: KindError}); err != nil {
		t.Error(err)
	}
}

func TestClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.jsonl")
	j, _ := Open(path)
	_ = j.Record(Entry{Kind: KindError, Text: "x"})
	if err := j.Clear(); err != nil || len(j.Entries()) != 0 {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the file stayed")
	}
	if err := j.Clear(); err != nil { // already gone: nothing to do
		t.Error(err)
	}
	if err := memory(t).Clear(); err != nil {
		t.Error(err)
	}
	// A file that cannot be removed: a non-empty directory sits at its name.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "keep"), nil, 0o600)
	bad := &Journal{path: dir, Now: time.Now}
	if err := bad.Clear(); err == nil {
		t.Error("clearing an unremovable journal did not fail")
	}
}

func TestSummarize(t *testing.T) {
	day := 24 * time.Hour
	entries := []Entry{
		{At: epoch.Add(-10 * day), Kind: KindError, Text: "too old"},
		{At: epoch.Add(day), Kind: KindError, Text: "in the future"},
		{At: epoch.Add(-day), Kind: KindRetry, Text: "attempt 1/3 failed"},
		{At: epoch.Add(-2 * day), Kind: KindRetry, Text: "Attempt 2/3 failed"},
		{At: epoch.Add(-3 * day), Kind: KindRetry, Text: "attempt 3/3 failed"},
		{At: epoch.Add(-day), Kind: KindError, Text: "boom"},
		{At: epoch.Add(-day), Kind: KindError, Text: "boom"},
		{At: epoch.Add(-day), Kind: KindDenied, Text: "rm -rf x"},
		{At: epoch.Add(-3 * day), Kind: KindFlagged, Text: "answer", Note: "wrong file"},
		{At: epoch.Add(-time.Hour), Kind: KindFlagged, Text: "answer", Note: "too long", Ask: "fix it"},
	}
	for i := 0; i < 8; i++ { // more repeated kinds than the cap
		for r := 0; r < 2; r++ {
			entries = append(entries, Entry{At: epoch.Add(-time.Hour), Kind: KindError, Text: strings.Repeat("z", i+1)})
		}
	}
	for i := 0; i < maxFlagged+2; i++ {
		entries = append(entries, Entry{At: epoch.Add(-time.Duration(i+2) * time.Hour), Kind: KindFlagged})
	}
	s := Summarize(entries, epoch, WeekDays)
	if s.Counts[KindRetry] != 3 || s.Counts[KindDenied] != 1 || s.Counts[KindError] != 2+16 {
		t.Errorf("counts = %v", s.Counts)
	}
	if s.Total != 3+1+18+2+maxFlagged+2 {
		t.Errorf("total = %d", s.Total)
	}
	if len(s.Repeated) != maxRepeated || s.Repeated[0].Count != 3 || s.Repeated[0].Kind != KindRetry {
		t.Errorf("repeated = %+v", s.Repeated)
	}
	if len(s.Flagged) != maxFlagged || s.Flagged[0].Note != "too long" {
		t.Errorf("flagged = %+v", s.Flagged)
	}
	quiet := Summarize(nil, epoch, WeekDays)
	if quiet.Total != 0 || quiet.Repeated == nil || quiet.Flagged == nil || len(quiet.Counts) != len(Kinds) {
		t.Errorf("quiet = %+v", quiet)
	}
}

func TestSummaryText(t *testing.T) {
	entries := []Entry{
		{At: epoch, Kind: KindError, Text: "boom"},
		{At: epoch, Kind: KindError, Text: "boom"},
		{At: epoch, Kind: KindRetry, Text: "again"},
		{At: epoch, Kind: KindRetry, Text: "again"},
		{At: epoch, Kind: KindDenied, Why: WhyPerson, Text: "rm"},
		{At: epoch, Kind: KindDenied, Why: WhyPerson, Text: "rm"},
		{At: epoch, Kind: KindDenied, Why: WhyNobody, Text: "cron"},
		{At: epoch, Kind: KindDenied, Why: WhyNobody, Text: "cron"},
		{At: epoch, Kind: KindFlagged, Note: "wrong", Ask: "do it"},
		{At: epoch, Kind: KindFlagged},
	}
	s := Summarize(entries, epoch, WeekDays)
	en, es := s.Text(i18n.EN), s.Text(i18n.ES)
	for _, want := range []string{
		"In the last 7 days, this got in the way (10 in all):", "Runs that ended with a failure: 2",
		"Steps that had to be tried again: 2", "Actions that were refused: 4", "Answers marked as not good: 2",
		"2x failure: boom", "2x retry: again", "2x refused action: rm (you refused it)", "2x refused action: cron (refused: nobody was there to ask)", "wrong — about: do it", "(no comment)",
	} {
		if !strings.Contains(en, want) {
			t.Errorf("English summary lacks %q:\n%s", want, en)
		}
	}
	for _, want := range []string{
		"En los últimos 7 días, esto se interpuso (10 en total):", "Ejecuciones que terminaron con un fallo: 2",
		"2x fallo: boom", "2x reintento: again", "2x acción rechazada: rm (la rechazaste)",
		"2x acción rechazada: cron (rechazada: no había nadie a quien preguntar)", "wrong — sobre: do it", "(sin comentario)",
		"Se repitió más de una vez:", "Respuestas que marcaste como no buenas:",
	} {
		if !strings.Contains(es, want) {
			t.Errorf("Spanish summary lacks %q:\n%s", want, es)
		}
	}
	if !strings.Contains(Summarize(nil, epoch, WeekDays).Text(i18n.ES), "nada se interpuso") {
		t.Error("the quiet week is not said in Spanish")
	}
	if !strings.Contains(Summarize(nil, epoch, WeekDays).Text(i18n.EN), "Nothing got in the way") {
		t.Error("the quiet week is not said in English")
	}
	// Only the kinds that happened get a line, and a week with no flagged answers has no such list.
	only := Summarize([]Entry{{At: epoch, Kind: KindError, Text: "x"}}, epoch, WeekDays).Text(i18n.EN)
	if strings.Contains(only, "tried again") || strings.Contains(only, "Came up") || strings.Contains(only, "Answers you") {
		t.Errorf("empty sections were drawn:\n%s", only)
	}
}
