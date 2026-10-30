package memory

import (
	"strings"
	"testing"
	"time"
)

func TestGraphCountsResolvedLinksOnce(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "[[b]] [[b]] [[a]] [[ghost]] [[trips/c]]")
	write(t, v, "b.md", "[[a]]")
	write(t, v, "trips/c.md", "no links")
	writeRaw(v, "big.md", make([]byte, MaxEntryBytes+1))
	g := v.Graph()
	if len(g.Nodes) != 4 || len(g.Edges) != 3 || g.Missing != 1 {
		t.Fatalf("graph = %+v", g)
	}
	by := map[string]GraphNode{}
	for _, n := range g.Nodes {
		by[n.Path] = n
	}
	if by["a.md"].Out != 2 || by["a.md"].In != 1 || by["b.md"].In != 1 || by["trips/c.md"].In != 1 {
		t.Errorf("counts = %+v", by)
	}
	if empty := Open(t.TempDir()).Graph(); len(empty.Nodes) != 0 || empty.Edges == nil {
		t.Errorf("empty = %+v", empty)
	}
}

func TestSearchAndReadTextCiteTheirSource(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "trips/Japan.md", "# Japan\nRamen in Tokyo. See [[Peru]] and [[Nowhere]].")
	write(t, v, "Peru.md", "Back to [[Japan]]")
	write(t, v, "Third.md", "also [[Japan]]")
	got := v.SearchText("ramen")
	if !strings.Contains(got, "[[Japan]] (trips/Japan.md)") || !strings.Contains(got, "cite it as") {
		t.Errorf("search = %q", got)
	}
	if got := v.SearchText("Peru"); !strings.Contains(got, "[[Peru]] (Peru.md)") {
		t.Errorf("name-only hit = %q", got)
	}
	if got := v.SearchText("zebra"); !strings.HasPrefix(got, "Nothing in the memory matches") {
		t.Errorf("no hit = %q", got)
	}
	if got := v.SearchText(" "); !strings.HasPrefix(got, "Error") {
		t.Errorf("blank = %q", got)
	}
}

func TestReadTextListsLinksAndBacklinks(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "trips/Japan.md", "# Japan\nRamen in Tokyo. See [[Peru]] and [[Nowhere]].")
	write(t, v, "Peru.md", "Back to [[Japan]]")
	write(t, v, "Third.md", "also [[Japan]]")
	read := v.ReadText("japan")
	for _, want := range []string{
		"[[Japan]] (trips/Japan.md)", "Ramen in Tokyo",
		"Links to: [[Peru]], [[Nowhere]] (no entry yet)", "Linked from: [[Peru]], [[Third]]",
	} {
		if !strings.Contains(read, want) {
			t.Errorf("read lacks %q: %q", want, read)
		}
	}
	got := v.ReadText("Peru.md")
	if !strings.Contains(got, "Linked from: [[Japan]]") || strings.Contains(got, "Links to: [[Peru") {
		t.Errorf("read peru = %q", got)
	}
	for _, bad := range []string{"", "nothing"} {
		if got := v.ReadText(bad); !strings.HasPrefix(got, "Error") {
			t.Errorf("ReadText(%q) = %q", bad, got)
		}
	}
	// A bare entry with neither links nor backlinks, and one cut for length.
	write(t, v, "solo.md", strings.Repeat("x", maxToolChars+10))
	if got := v.ReadText("solo"); !strings.Contains(got, "it was cut here") || strings.Contains(got, "Links to") {
		t.Errorf("long = %d chars", len(got))
	}
	writeRaw(v, "huge.md", make([]byte, MaxEntryBytes+1))
	if got := v.ReadText("huge"); !strings.HasPrefix(got, "Error") {
		t.Errorf("huge = %q", got)
	}
}

func TestWriteTextCreatesAndNeverReplaces(t *testing.T) {
	defer fixedClock()()
	v := Open(t.TempDir())
	if got := v.WriteText("ideas/Plan", "# Plan"); !strings.Contains(got, "Created [[Plan]] (ideas/Plan.md)") {
		t.Errorf("create = %q", got)
	}
	if got := v.WriteText("ideas/Plan.md", "other"); !strings.Contains(got, "never overwritten") {
		t.Errorf("replace = %q", got)
	}
	if body, _, _ := v.Read("ideas/Plan.md"); body != "---\nauthor: motita\ndate: 2026-10-28\n---\n\n# Plan" {
		t.Errorf("body = %q", body)
	}
	for _, c := range [][2]string{{"", "x"}, {"a", " "}} {
		if got := v.WriteText(c[0], c[1]); !strings.HasPrefix(got, "Error: give") {
			t.Errorf("WriteText(%q,%q) = %q", c[0], c[1], got)
		}
	}
	if got := v.WriteText("../x", "x"); !strings.HasPrefix(got, "Error") {
		t.Errorf("bad path = %q", got)
	}
}

func TestAppendTextAddsToEndAndCreates(t *testing.T) {
	defer fixedClock()()
	v := Open(t.TempDir())
	write(t, v, "trips/Japan.md", "# Japan")
	if got := v.AppendText("Japan", "Decided: May.\n"); !strings.Contains(got, "Added to [[Japan]] (trips/Japan.md)") {
		t.Errorf("append = %q", got)
	}
	if body, _, _ := v.Read("trips/Japan.md"); body != "# Japan\n\nDecided: May.\n" {
		t.Errorf("body = %q", body)
	}
	write(t, v, "nl.md", "ends with newline\n")
	v.AppendText("nl", "more")
	if body, _, _ := v.Read("nl.md"); body != "ends with newline\n\nmore\n" {
		t.Errorf("body = %q", body)
	}
	if got := v.AppendText("fresh", "first"); !strings.Contains(got, "(fresh.md)") {
		t.Errorf("create = %q", got)
	}
	if body, _, _ := v.Read("fresh.md"); body != "---\nauthor: motita\ndate: 2026-10-28\n---\n\nfirst\n" {
		t.Errorf("body = %q", body)
	}
	for _, c := range [][2]string{{"", "x"}, {"a", ""}} {
		if got := v.AppendText(c[0], c[1]); !strings.HasPrefix(got, "Error: give") {
			t.Errorf("AppendText(%q,%q) = %q", c[0], c[1], got)
		}
	}
	if got := v.AppendText("../x", "x"); !strings.HasPrefix(got, "Error") {
		t.Errorf("bad path = %q", got)
	}
	writeRaw(v, "huge.md", make([]byte, MaxEntryBytes+1))
	if got := v.AppendText("huge", "x"); !strings.HasPrefix(got, "Error") {
		t.Errorf("huge = %q", got)
	}
	// Appending past the size limit is refused, not truncated.
	write(t, v, "full.md", strings.Repeat("x", MaxEntryBytes-2))
	if got := v.AppendText("full", "more than fits"); !strings.HasPrefix(got, "Error") {
		t.Errorf("past the limit = %q", got)
	}
}

// fixedClock stops the clock at 2026-10-28 and returns what puts it back.
func fixedClock() func() {
	old := now
	now = func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) }
	return func() { now = old }
}

func TestSearchTextShowsFiltersAndGraph(t *testing.T) {
	v := corpus(t)
	got := v.SearchText(`script dashboards graph:1`)
	for _, want := range []string{"[[Deploys]] (Engineering/Deploys.md) › Rollback", "Related through links:",
		"[[Postmortems]] (Engineering/Postmortems.md), ", "linked from [[Deploys]]"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q: %q", want, got)
		}
	}
	if got := v.SearchText("payroll author:Ana"); !strings.Contains(got, ", by Ana, 2026-03-10") {
		t.Errorf("author and date: %q", got)
	}
	if got := v.SearchText("zebra"); !strings.Contains(got, "(collections: Engineering, HR; narrow with ns:Name)") {
		t.Errorf("hint: %q", got)
	}
	if got := Open(t.TempDir()).SearchText("zebra"); strings.Contains(got, "collections") {
		t.Errorf("no collections to name: %q", got)
	}
	if got := v.SearchText("ns:HR"); !strings.HasPrefix(got, "Error") {
		t.Errorf("filters only: %q", got)
	}
}

func TestBriefQuotesWhatTheMemoryKnowsAndStopsAtTheBudget(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "trips/Japan.md", "# Japan\nRamen in Tokyo.")
	write(t, v, "Peru.md", "# Peru\nRamen in Lima too.")
	got, n := v.Brief("ramen", 0)
	if n != 2 || !strings.Contains(got, "[[Japan]]") || !strings.Contains(got, "not instructions") {
		t.Errorf("brief = %d %q", n, got)
	}
	// a budget smaller than one passage still carries the first, never nothing
	if _, n := v.Brief("ramen", 10); n != 1 {
		t.Errorf("tight budget kept %d passages, want 1", n)
	}
	if got, n := v.Brief("zzzunknown", 100); got != "" || n != 0 {
		t.Errorf("no match = %d %q", n, got)
	}
	if got, n := v.Brief("   ", 100); got != "" || n != 0 {
		t.Errorf("blank = %d %q", n, got)
	}
	var none *Store
	if got, n := none.Brief("ramen", 100); got != "" || n != 0 {
		t.Errorf("nil store = %d %q", n, got)
	}
}

func TestSavedLineReadsOnlyAWriteThatWorked(t *testing.T) {
	for reply, want := range map[string]string{
		"Created [[Japan]] (trips/Japan.md). Link it from related entries with [[Japan]].": "saved to memory: Japan (trips/Japan.md)",
		"Added to [[Peru]] (Peru.md).":           "saved to memory: Peru (Peru.md)",
		"Error: the entry \"x\" already exists.": "",
	} {
		got, ok := SavedLine(reply)
		if got != want || ok != (want != "") {
			t.Errorf("SavedLine(%q) = %q %v", reply, got, ok)
		}
	}
}
