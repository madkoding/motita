package evals

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// scripted is a Runner with an answer per task.
type scripted struct {
	outcomes map[string]Outcome
	errs     map[string]error
	dirs     []string
	cancel   context.CancelFunc
}

func (s *scripted) Run(_ context.Context, c Case, dir string) (Outcome, error) {
	s.dirs = append(s.dirs, dir)
	for name := range c.Files {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return Outcome{}, err
		}
	}
	if s.cancel != nil {
		s.cancel()
	}
	if err := s.errs[c.ID]; err != nil {
		return Outcome{}, err
	}
	return s.outcomes[c.ID], nil
}

func TestExecuteRunsEveryTaskInAThrowawayFolder(t *testing.T) {
	r := &scripted{outcomes: map[string]Outcome{
		"create-file": {Pass: true, Steps: 1, Tokens: 100, CostUSD: 0.5, CostKnown: true, DurationMS: 5},
		"fix-typo":    {Pass: false, Steps: 3, Tokens: 300, CostUSD: 1, CostKnown: true, Reason: "typo left"},
	}, errs: map[string]error{"count-lines": errors.New("boom")}}
	var seen []string
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	run, err := Execute(context.Background(), r, Cases(), Options{
		Provider: "p", Model: "m", PromptHash: "h", Now: func() time.Time { return at },
		Progress: func(done, total int, next string) { seen = append(seen, next) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "20261020T120000Z" || run.Provider != "p" || run.PromptHash != "h" || run.CasesHash != CasesHash() {
		t.Errorf("run = %+v", run)
	}
	s := run.Summary
	if s.Passed != 1 || s.Total != 5 || s.Steps != 4 {
		t.Errorf("summary = %+v", s)
	}
	if s.Tokens != 400 || s.CostUSD != 1.5 || s.DurationMS != 5 {
		t.Errorf("summary = %+v", s)
	}
	if s.CostKnown {
		t.Error("tasks that ran without a price leave the cost unknown")
	}
	if run.Results[1].Reason != "typo left" || run.Results[2].Error != "boom" {
		t.Errorf("results = %+v", run.Results)
	}
	if len(seen) != 6 || seen[0] != "create-file" || seen[5] != "" {
		t.Errorf("progress = %v", seen)
	}
	for _, d := range r.dirs {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("%s should be removed", d)
		}
	}
}

func TestExecuteKnowsTheCostWhenEveryPriceIsKnown(t *testing.T) {
	out := map[string]Outcome{}
	for _, c := range Cases() {
		out[c.ID] = Outcome{Pass: true, CostKnown: true}
	}
	run, err := Execute(context.Background(), &scripted{outcomes: out}, Cases(), Options{})
	if err != nil || !run.Summary.CostKnown || run.Summary.Passed != 5 {
		t.Errorf("run %+v, err %v", run.Summary, err)
	}
}

func TestExecuteStoresNothingWhenNoTaskCouldRun(t *testing.T) {
	errs := map[string]error{}
	for _, c := range Cases() {
		errs[c.ID] = errors.New("down")
	}
	if _, err := Execute(context.Background(), &scripted{errs: errs}, Cases(), Options{}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("err = %v", err)
	}
}

func TestExecuteStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := Execute(ctx, &scripted{cancel: cancel}, Cases(), Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestExecuteReportsAFolderItCannotPrepare(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	_, err := Execute(context.Background(), &scripted{}, Cases(), Options{})
	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("err = %v", err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	// A task whose file cannot be written (its folder is not there) is reported the same way.
	res := runCase(context.Background(), &scripted{}, Case{ID: "x", Files: map[string]string{"a/b": "x"}})
	if res.Error == "" {
		t.Error("a file that cannot be written is an error")
	}
}
