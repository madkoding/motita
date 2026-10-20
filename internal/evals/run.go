package evals

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Outcome is what a Runner reports about one task.
type Outcome struct {
	Pass       bool
	Steps      int
	Tokens     int64
	CostUSD    float64
	CostKnown  bool
	DurationMS int64
	Reason     string
}

// Runner runs one task in dir, which already holds the task's files.
type Runner interface {
	Run(ctx context.Context, c Case, dir string) (Outcome, error)
}

// Options say what a run is of and how to follow it.
type Options struct {
	Provider, Model, PromptHash string
	// Progress is told before each task starts: how many are done, of how many, and which comes next.
	Progress func(done, total int, next string)
	// Now is the clock, replaceable in a test.
	Now func() time.Time
}

// Execute runs every task, each in a folder of its own that is removed afterwards, and returns the
// run. A task that could not run at all counts as failed and says why; if none could, there is no
// run (ErrNoAnswer). A cancelled context stops the run without one.
func Execute(ctx context.Context, r Runner, cases []Case, op Options) (Run, error) {
	now := time.Now
	if op.Now != nil {
		now = op.Now
	}
	run := Run{
		ID: now().UTC().Format("20060102T150405Z"), At: now().UTC(),
		Provider: op.Provider, Model: op.Model, PromptHash: op.PromptHash, CasesHash: CasesHash(),
	}
	broken := 0
	for i, c := range cases {
		if op.Progress != nil {
			op.Progress(i, len(cases), c.ID)
		}
		res := runCase(ctx, r, c)
		if err := ctx.Err(); err != nil {
			return Run{}, err
		}
		if res.Error != "" {
			broken++
		}
		run.Results = append(run.Results, res)
	}
	if op.Progress != nil {
		op.Progress(len(cases), len(cases), "")
	}
	if broken == len(cases) {
		return Run{}, ErrNoAnswer
	}
	run.Summary = summarise(run.Results)
	return run, nil
}

// runCase runs one task in a throwaway folder.
func runCase(ctx context.Context, r Runner, c Case) CaseResult {
	res := CaseResult{ID: c.ID}
	dir, err := os.MkdirTemp("", "motita-eval-")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer os.RemoveAll(dir)
	for name, body := range c.Files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	out, err := r.Run(ctx, c, dir)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Pass, res.Steps, res.Tokens = out.Pass, out.Steps, out.Tokens
	res.CostUSD, res.CostKnown, res.DurationMS, res.Reason = out.CostUSD, out.CostKnown, out.DurationMS, out.Reason
	return res
}

// summarise adds the tasks up. The cost is known only when every task that ran has a price.
func summarise(results []CaseResult) Summary {
	s := Summary{Total: len(results), CostKnown: true}
	for _, r := range results {
		if r.Pass {
			s.Passed++
		}
		s.Steps += r.Steps
		s.Tokens += r.Tokens
		s.CostUSD += r.CostUSD
		s.DurationMS += r.DurationMS
		s.CostKnown = s.CostKnown && (r.CostKnown || r.Error != "")
	}
	return s
}
