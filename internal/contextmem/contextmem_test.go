package contextmem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/memory"
)

func openTest(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "conversation"), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func readEntry(t *testing.T, s *Store, name string) string {
	t.Helper()
	body, _, err := s.mem.Read(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return body
}

func TestOpenCreatesAPrivateFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a", "b")
	s, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Root() == "" || s.Root() == "." {
		t.Fatalf("Root() = %q", s.Root())
	}
	fi, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("archive mode = %o, want 700", got)
	}
	if got, want := s.opts, (Options{DefaultRecallTokens, DefaultRecallHits, DefaultMaxTurns}); got != want {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
}

func TestOpenRefusesAnEmptyFolder(t *testing.T) {
	if _, err := Open("  ", Options{}); err == nil {
		t.Fatal("Open accepted an empty folder")
	}
}

func TestOpenReportsDiskFailures(t *testing.T) {
	oldAbs, oldMk, oldChmod := absPath, mkdirAll, chmod
	t.Cleanup(func() { absPath, mkdirAll, chmod = oldAbs, oldMk, oldChmod })

	absPath = func(string) (string, error) { return "", errors.New("no cwd") }
	if _, err := Open("x", Options{}); err == nil || !strings.Contains(err.Error(), "not a path") {
		t.Fatalf("abs error = %v", err)
	}

	absPath = oldAbs
	mkdirAll = func(string, os.FileMode) error { return errors.New("disk full") }
	if _, err := Open(filepath.Join(t.TempDir(), "x"), Options{}); err == nil || !strings.Contains(err.Error(), "could not create") {
		t.Fatalf("mkdir error = %v", err)
	}

	mkdirAll = oldMk
	chmod = func(string, os.FileMode) error { return errors.New("denied") }
	if _, err := Open(filepath.Join(t.TempDir(), "x"), Options{}); err == nil || !strings.Contains(err.Error(), "could not protect") {
		t.Fatalf("chmod error = %v", err)
	}
}

func TestDirAcceptsOnlyPlainNames(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "ctx")
	if got, want := Dir(root, "s123"), filepath.Join(root, "s123"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "a.b"} {
		if got := Dir(root, id); got != "" {
			t.Errorf("Dir(%q) = %q, want empty", id, got)
		}
	}
	if got := Dir("  ", "s1"); got != "" {
		t.Errorf("Dir with no root = %q", got)
	}
}

func TestNewIDIsRandomAndFallsBack(t *testing.T) {
	a, b := NewID(), NewID()
	if a == "" || a == b {
		t.Fatalf("NewID returned %q and %q", a, b)
	}
	old := randRead
	t.Cleanup(func() { randRead = old })
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	if got := NewID(); !strings.HasPrefix(got, "t") {
		t.Errorf("fallback id = %q", got)
	}
}

func TestDeleteRefusesTheWholeFilesystem(t *testing.T) {
	if err := Delete(""); err != nil {
		t.Errorf("Delete(\"\") = %v", err)
	}
	if err := Delete(string(filepath.Separator)); err == nil {
		t.Error("Delete(/) was allowed")
	}
	old := absPath
	t.Cleanup(func() { absPath = old })
	absPath = func(string) (string, error) { return "", errors.New("no cwd") }
	if err := Delete("x"); err == nil || !strings.Contains(err.Error(), "not a path") {
		t.Errorf("Delete with a bad path = %v", err)
	}
}

func TestDeleteRemovesTheTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "turn"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Delete(root); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the folder survived: %v", err)
	}
}

func TestCloseDeletesAndForgivesNil(t *testing.T) {
	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Errorf("nil Close = %v", err)
	}
	if got := nilStore.Root(); got != "" {
		t.Errorf("nil Root = %q", got)
	}
	s := openTest(t, Options{})
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the archive survived Close: %v", err)
	}
}

func TestSweepKeepsTheKnownAndRemovesTheRest(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"keep", "drop", "also-drop"} {
		if err := os.MkdirAll(filepath.Join(root, id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Sweep(root, map[string]bool{"keep": true, "gone": true}); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "keep")); err != nil {
		t.Errorf("the known archive was removed: %v", err)
	}
	for _, id := range []string{"drop", "also-drop"} {
		if _, err := os.Stat(filepath.Join(root, id)); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "stray.txt")); err != nil {
		t.Errorf("a file was removed: %v", err)
	}
	if err := Sweep(filepath.Join(root, "missing"), nil); err != nil {
		t.Errorf("a missing root is not an error: %v", err)
	}
	if err := Sweep("  ", nil); err != nil {
		t.Errorf("an empty root is not an error: %v", err)
	}
}

func TestSweepReportsDiskFailures(t *testing.T) {
	oldRead, oldRemove := readDir, removeAll
	t.Cleanup(func() { readDir, removeAll = oldRead, oldRemove })

	readDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("denied") }
	if err := Sweep(t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "could not read") {
		t.Fatalf("read error = %v", err)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "orphan"), 0o700); err != nil {
		t.Fatal(err)
	}
	readDir = oldRead
	removeAll = func(string) error { return errors.New("busy") }
	if err := Sweep(root, nil); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("remove error = %v", err)
	}
}

func TestSyncForgivesNil(t *testing.T) {
	var nilStore *Store
	if err := nilStore.Sync([]Turn{{Kind: "chat", User: "one", Agent: "a"}}); err != nil {
		t.Errorf("nil Sync = %v", err)
	}
}

func TestSyncWritesOneEntryPerTurn(t *testing.T) {
	s := openTest(t, Options{})
	date := time.Date(2026, 10, 28, 9, 15, 0, 0, time.UTC)
	turns := []Turn{
		{Kind: "chat", User: "hello", Agent: "hi", Date: date},
		{Kind: "task", User: "fix the parser", Agent: "it works", Report: "Changed: parser.go", Date: date},
		{Kind: "plan", User: "and now?", Agent: "nothing yet", Pending: true, Date: date},
	}
	if err := s.Sync(turns); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	list := s.mem.List()
	var names []string
	for _, e := range list {
		names = append(names, e.Path)
	}
	want := []string{"turn-000001-chat.md", "turn-000002-task.md", "turn-000003-plan.md"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", names, want)
	}

	first := readEntry(t, s, want[0])
	for _, part := range []string{"author: motita", "date: 2026-10-28T09:15:00Z", "# Turn 1 · chat", "## Request", "hello", "## Answer", "hi"} {
		if !strings.Contains(first, part) {
			t.Errorf("turn 1 does not contain %q:\n%s", part, first)
		}
	}
	if strings.Contains(first, "## Report") {
		t.Error("a chat turn carries a report section")
	}

	task := readEntry(t, s, want[1])
	if !strings.Contains(task, "## Report") || !strings.Contains(task, "Changed: parser.go") {
		t.Errorf("the task report is not archived:\n%s", task)
	}
	if !strings.Contains(readEntry(t, s, want[2]), pendingNote) {
		t.Error("a pending turn does not say it is pending")
	}
}

func TestSyncRedactsBeforeWriting(t *testing.T) {
	s := openTest(t, Options{})
	if err := s.Sync([]Turn{{Kind: "chat", User: `"api_key": "super-secret-value"`, Agent: ""}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	body := readEntry(t, s, "turn-000001-chat.md")
	if strings.Contains(body, "super-secret-value") {
		t.Errorf("the secret was written:\n%s", body)
	}
	if !strings.Contains(body, emptyNote) {
		t.Errorf("an empty answer is not marked:\n%s", body)
	}
}

func TestSyncRewritesTheLastTurn(t *testing.T) {
	s := openTest(t, Options{})
	if err := s.Sync([]Turn{{Kind: "task", User: "the request", Agent: "working", Pending: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync([]Turn{{Kind: "task", User: "the request", Agent: "finished"}}); err != nil {
		t.Fatal(err)
	}
	body := readEntry(t, s, "turn-000001-task.md")
	if !strings.Contains(body, "finished") || strings.Contains(body, pendingNote) {
		t.Errorf("the pending text survived the outcome:\n%s", body)
	}

	// A kind that changes renames the file instead of leaving two copies of one turn.
	if err := s.Sync([]Turn{{Kind: "plan", User: "the request", Agent: "finished"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "turn-000001-task.md")); !os.IsNotExist(err) {
		t.Errorf("the old kind's file survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "turn-000001-plan.md")); err != nil {
		t.Errorf("the renamed file is missing: %v", err)
	}
}

func TestSyncDoesNotRewriteFinishedTurns(t *testing.T) {
	s := openTest(t, Options{})
	if err := s.Sync([]Turn{{Kind: "chat", User: "one", Agent: "a"}, {Kind: "chat", User: "two", Agent: "b"}}); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(s.Root(), "turn-000001-chat.md")
	before, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Sync([]Turn{{Kind: "chat", User: "one", Agent: "a"}, {Kind: "chat", User: "two", Agent: "changed"}}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a finished turn was rewritten")
	}
}

func TestSyncPrunesTheOldestTurns(t *testing.T) {
	s := openTest(t, Options{MaxTurns: 2})
	var turns []Turn
	for i := 0; i < 5; i++ {
		turns = append(turns, Turn{Kind: "chat", User: "turn", Agent: "answer"})
	}
	if err := s.Sync(turns); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	list := s.mem.List()
	if len(list) != 2 {
		t.Fatalf("kept %d entries, want 2", len(list))
	}
	for _, e := range list {
		seq, _, ok := parseName(e.Path)
		if !ok || seq < 4 {
			t.Errorf("kept the wrong entry %q", e.Path)
		}
	}
}

func TestSyncReportsAWriteFailureAndKeepsGoing(t *testing.T) {
	s := openTest(t, Options{})
	old := writeEntry
	t.Cleanup(func() { writeEntry = old })
	calls := 0
	writeEntry = func(m *memory.Store, p, body string, create bool) (memory.Entry, error) {
		calls++
		if strings.Contains(p, "000001") {
			return memory.Entry{}, errors.New("disk full")
		}
		return old(m, p, body, create)
	}
	err := s.Sync([]Turn{{Kind: "chat", User: "one", Agent: "a"}, {Kind: "chat", User: "two", Agent: "b"}})
	if err == nil || !strings.Contains(err.Error(), "could not archive turn 1") {
		t.Fatalf("Sync error = %v", err)
	}
	if calls != 2 {
		t.Errorf("writes = %d, want 2", calls)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "turn-000002-chat.md")); err != nil {
		t.Errorf("the turn after the failure was not written: %v", err)
	}
}

func TestRecallAnswersFromTheArchive(t *testing.T) {
	s := openTest(t, Options{})
	turns := []Turn{
		{Kind: "chat", User: "what about the payroll script?", Agent: "it runs at dawn", Date: time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)},
		{Kind: "task", User: "fix the deployment", Agent: "the pipeline is green", Date: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)},
		{Kind: "plan", User: "and the payroll report?", Agent: "it is scheduled", Date: time.Date(2026, 10, 22, 10, 30, 0, 0, time.UTC)},
	}
	if err := s.Sync(turns); err != nil {
		t.Fatal(err)
	}

	block, n := s.Recall(context.Background(), "payroll", 0)
	if n == 0 {
		t.Fatal("no passage was recalled")
	}
	if !strings.Contains(block, "payroll") {
		t.Errorf("the passage does not carry the query:\n%s", block)
	}
	if !strings.Contains(block, "Turn 1 · chat") || !strings.Contains(block, "2026-10-20 08:00") {
		t.Errorf("the passage does not identify its turn:\n%s", block)
	}
	if !strings.Contains(block, "background data") {
		t.Errorf("the block does not say it is data:\n%s", block)
	}
}

func TestRecallExcludesTheLiveWindow(t *testing.T) {
	s := openTest(t, Options{RecallHits: 2})
	var turns []Turn
	for i := 0; i < 4; i++ {
		turns = append(turns, Turn{Kind: "chat", User: "payroll payroll", Agent: "payroll"})
	}
	if err := s.Sync(turns); err != nil {
		t.Fatal(err)
	}
	if _, n := s.Recall(context.Background(), "payroll", 4); n != 0 {
		t.Errorf("recalled %d passages, all of them still in the window", n)
	}
	block, n := s.Recall(context.Background(), "payroll", 2)
	if n != 2 {
		t.Fatalf("recalled %d passages, want 2", n)
	}
	if strings.Contains(block, "Turn 3 ·") || strings.Contains(block, "Turn 4 ·") {
		t.Errorf("the live turns were recalled:\n%s", block)
	}
}

func TestRecallRespectsTheLimits(t *testing.T) {
	s := openTest(t, Options{RecallHits: 1})
	for i := 0; i < 3; i++ {
		if err := s.Sync([]Turn{{Kind: "chat", User: "payroll", Agent: "payroll"}}); err != nil {
			t.Fatal(err)
		}
	}
	// Sync is idempotent by position, so write the three turns together.
	var turns []Turn
	for i := 0; i < 3; i++ {
		turns = append(turns, Turn{Kind: "chat", User: "payroll", Agent: "payroll"})
	}
	if err := s.Sync(turns); err != nil {
		t.Fatal(err)
	}
	if _, n := s.Recall(context.Background(), "payroll", 0); n != 1 {
		t.Errorf("recalled %d passages, want the configured 1", n)
	}

	tiny := openTest(t, Options{RecallTokens: 5})
	if err := tiny.Sync(turns); err != nil {
		t.Fatal(err)
	}
	if block, n := tiny.Recall(context.Background(), "payroll", 0); n != 0 || block != "" {
		t.Errorf("a budget too small returned %d passages", n)
	}
}

func TestRecallCapsTheSearchAtTheMemoryLimit(t *testing.T) {
	s := openTest(t, Options{RecallHits: 30})
	var turns []Turn
	for i := 0; i < 40; i++ {
		turns = append(turns, Turn{Kind: "chat", User: "payroll", Agent: "payroll"})
	}
	if err := s.Sync(turns); err != nil {
		t.Fatal(err)
	}
	block, n := s.Recall(context.Background(), "payroll", 5)
	if n == 0 || n > 30 {
		t.Fatalf("recalled %d passages, want between 1 and %d", n, 30)
	}
	if !strings.Contains(block, "background data") {
		t.Errorf("the block does not say it is data:\n%s", block)
	}
}

func TestRecallIgnoresWhatItCannotPlace(t *testing.T) {
	s := openTest(t, Options{})
	if err := s.Sync([]Turn{{Kind: "chat", User: "payroll", Agent: "payroll"}}); err != nil {
		t.Fatal(err)
	}
	// A file the archive did not write has no turn, so it cannot be dated or excluded.
	if err := os.WriteFile(filepath.Join(s.Root(), "notes.md"), []byte("payroll"), 0o600); err != nil {
		t.Fatal(err)
	}
	block, n := s.Recall(context.Background(), "payroll", 0)
	if n == 0 {
		t.Fatal("the real turn was not recalled")
	}
	if strings.Contains(block, "notes") {
		t.Errorf("a file with no turn was recalled:\n%s", block)
	}
}

func TestRecallForgivesAnEmptyQuestionOrArchive(t *testing.T) {
	var nilStore *Store
	if block, n := nilStore.Recall(context.Background(), "x", 0); block != "" || n != 0 {
		t.Error("a nil store recalled something")
	}
	s := openTest(t, Options{})
	if block, n := s.Recall(context.Background(), "   ", 0); block != "" || n != 0 {
		t.Error("an empty query recalled something")
	}
	if block, n := s.Recall(context.Background(), "anything", 0); block != "" || n != 0 {
		t.Error("an empty archive recalled something")
	}
}

func TestRecallTruncatesAPassageToTheBudget(t *testing.T) {
	s := openTest(t, Options{RecallTokens: 400})
	long := strings.Repeat("payroll numbers and the schedule of the run ", 40)
	if err := s.Sync([]Turn{{Kind: "task", User: "payroll", Agent: long}}); err != nil {
		t.Fatal(err)
	}
	block, n := s.Recall(context.Background(), "payroll", 0)
	if n != 1 {
		t.Fatalf("recalled %d passages, want 1", n)
	}
	if len(block) > 400*3+len(blockHeader) {
		t.Errorf("the passage is longer than the budget: %d bytes", len(block))
	}
}

func TestLongTurnsAreCutToTheEntryLimit(t *testing.T) {
	s := openTest(t, Options{})
	long := strings.Repeat("x", 300_000)
	if err := s.Sync([]Turn{{Kind: "task", User: "big", Agent: long}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	body := readEntry(t, s, "turn-000001-task.md")
	if !strings.Contains(body, "truncated") {
		t.Error("the long turn was not marked as truncated")
	}
	if len(body) > 192<<10 {
		t.Errorf("the entry is %d bytes, past the memory's limit", len(body))
	}
}

func TestNamesAndHeadings(t *testing.T) {
	if got := fileName(12, "Task!"); got != "turn-000012-task.md" {
		t.Errorf("fileName = %q", got)
	}
	if got := kindOf(""); got != "chat" {
		t.Errorf("kindOf(\"\") = %q", got)
	}
	if got := kindOf("  Plan 2 "); got != "plan2" {
		t.Errorf("kindOf = %q", got)
	}
	seq, kind, ok := parseName("turn-000012-task.md")
	if !ok || seq != 12 || kind != "task" {
		t.Errorf("parseName = %d, %q, %v", seq, kind, ok)
	}
	for _, bad := range []string{"notes.md", "turn-x-task.md", "turn-000001.md", "turn-0-task.md", "turn-000001-.md"} {
		if _, _, ok := parseName(bad); ok {
			t.Errorf("parseName(%q) accepted", bad)
		}
	}
	if got := when(time.Time{}); got != "undated" {
		t.Errorf("when(zero) = %q", got)
	}
	if got := when(time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)); got != "2026-01-02 03:04" {
		t.Errorf("when = %q", got)
	}
}

func TestCutKeepsWholeRunes(t *testing.T) {
	if got := cut("hello", 100, "!"); got != "hello" {
		t.Errorf("cut below the limit = %q", got)
	}
	if got := cut("hello", 3, "!!!"); got != "" {
		t.Errorf("cut with no room = %q", got)
	}
	if got := cut("hola mundo", 10, ".."); got != "hola mundo" {
		t.Errorf("cut at the limit = %q", got)
	}
	if got := cut("hola mundo", 8, ".."); got != "hola m.." {
		t.Errorf("cut = %q", got)
	}
	if got := cutChars("ñññ", 4); got != "ññ" {
		t.Errorf("cutChars split a rune: %q", got)
	}
	if got := cutChars("ñññ", 5); got != "ññ" {
		t.Errorf("cutChars split a rune: %q", got)
	}
	if got := cutChars("abc", 0); got != "" {
		t.Errorf("cutChars with no room = %q", got)
	}
	if got := cutChars("abc", 10); got != "abc" {
		t.Errorf("cutChars = %q", got)
	}
}

func TestRenderUsesNowWhenTheTurnHasNoDate(t *testing.T) {
	body := render(1, Turn{Kind: "chat", User: "x", Agent: "y"})
	if !strings.Contains(body, time.Now().UTC().Format("2006-01-02")) {
		t.Errorf("no date was stamped:\n%s", body)
	}
}
