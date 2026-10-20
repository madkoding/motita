package evals

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// run builds a run of the current tasks; passes says which ones passed.
func run(id string, passes []bool, steps int, cost float64, known bool) Run {
	r := Run{ID: id, CasesHash: CasesHash()}
	for i, c := range Cases() {
		r.Results = append(r.Results, CaseResult{ID: c.ID, Pass: passes[i]})
	}
	for _, p := range passes {
		if p {
			r.Summary.Passed++
		}
	}
	r.Summary.Total = len(passes)
	r.Summary.Steps, r.Summary.CostUSD, r.Summary.CostKnown = steps, cost, known
	return r
}

var (
	all   = []bool{true, true, true, true, true}
	fewer = []bool{true, true, true, true, false}
	swap  = []bool{false, true, true, true, true}
)

func TestCompare(t *testing.T) {
	base := run("a", all, 10, 1, true)
	other := run("x", all, 10, 1, true)
	other.CasesHash = "other"
	cases := []struct {
		name string
		prev *Run
		cur  Run
		want string
	}{
		{"no earlier run", nil, base, StatusFirst},
		{"other set of tasks", &other, base, StatusFirst},
		{"same", &base, run("b", all, 11, 1.1, true), StatusSame},
		{"fewer pass", &base, run("b", fewer, 10, 1, true), StatusWorse},
		{"one fixed, one broken", &base, run("b", swap, 10, 1, true), StatusWorse},
		{"more pass", &run0, run("b", all, 10, 1, true), StatusBetter},
		{"more rounds", &base, run("b", all, 13, 1, true), StatusWorse},
		{"costs more", &base, run("b", all, 10, 1.4, true), StatusWorse},
		{"fewer rounds", &base, run("b", all, 8, 1, true), StatusBetter},
		{"costs less", &base, run("b", all, 10, 0.5, true), StatusBetter},
		{"cost unknown now", &base, run("b", all, 10, 5, false), StatusSame},
	}
	for _, c := range cases {
		if got := Compare(c.prev, c.cur).Status; got != c.want {
			t.Errorf("%s: status = %s, want %s", c.name, got, c.want)
		}
	}
}

var run0 = run("z", fewer, 10, 1, true)

func TestCompareNamesWhatChanged(t *testing.T) {
	prev := run("a", fewer, 10, 1, true)
	v := Compare(&prev, run("b", swap, 13, 1.5, true))
	if !reflect.DeepEqual(v.Regressed, []string{"create-file"}) {
		t.Errorf("regressed %v", v.Regressed)
	}
	if !reflect.DeepEqual(v.Fixed, []string{"sort-lines"}) {
		t.Errorf("fixed %v", v.Fixed)
	}
	if v.Against != "a" || v.DeltaSteps != 3 || v.DeltaCostUSD != 0.5 {
		t.Errorf("verdict = %+v", v)
	}
	if !v.CostUp || !v.StepsUp {
		t.Errorf("more steps and more cost should be flagged: %+v", v)
	}
}

func TestStale(t *testing.T) {
	last := run("a", all, 1, 1, true)
	last.Provider, last.Model, last.PromptHash = "p", "m", "h"
	other := last
	other.CasesHash = "x"
	cases := []struct {
		last                        *Run
		provider, model, hash, want string
	}{
		{nil, "p", "m", "h", "never"},
		{&other, "p", "m", "h", "cases"},
		{&last, "q", "m", "h", "model"},
		{&last, "p", "n", "h", "model"},
		{&last, "p", "m", "i", "prompt"},
		{&last, "p", "m", "h", ""},
	}
	for i, c := range cases {
		if got := Stale(c.last, c.provider, c.model, c.hash); got != c.want {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
}

func TestHashesChangeWithTheirInput(t *testing.T) {
	again := PromptsHash("a")
	if PromptsHash("a") == PromptsHash("b") || PromptsHash("a") != again {
		t.Error("the prompts hash should follow the prompts")
	}
}

func TestStoreKeepsTheLastRunsOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "evals.json")
	s := OpenStore(path)
	if s.Last() != nil || len(s.Runs()) != 0 {
		t.Fatal("a new store is empty")
	}
	for i := 0; i < keepRuns+3; i++ {
		if err := s.Add(Run{ID: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.Runs()) != keepRuns || s.Last().ID != string(rune('a'+keepRuns+2)) {
		t.Errorf("kept %d runs, last %v", len(s.Runs()), s.Last())
	}
	again := OpenStore(path)
	if len(again.Runs()) != keepRuns || again.Last().ID != s.Last().ID {
		t.Error("the runs should survive a restart")
	}
}

func TestStoreWithoutAFileKeepsRunsInMemory(t *testing.T) {
	s := OpenStore("")
	if err := s.Add(Run{ID: "a"}); err != nil || s.Last().ID != "a" {
		t.Errorf("err %v, last %v", err, s.Last())
	}
}

func TestStoreSurvivesAnUnreadableFileAndReportsAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "evals.json")
	if err := os.WriteFile(bad, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := OpenStore(bad)
	if len(s.Runs()) != 0 {
		t.Error("a file that is not JSON starts empty")
	}
	// A file in the way of the folder makes the write fail; the run stays in memory.
	blocked := OpenStore(filepath.Join(bad, "evals.json"))
	if err := blocked.Add(Run{ID: "a"}); err == nil || blocked.Last().ID != "a" {
		t.Errorf("err %v, last %v", err, blocked.Last())
	}
	// A directory where the temp file goes makes the write fail too.
	if err := os.Mkdir(filepath.Join(dir, "x.json.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := OpenStore(filepath.Join(dir, "x.json")).Add(Run{ID: "a"}); err == nil {
		t.Error("a temp file that cannot be written is an error")
	}
}
