// Package anchor implements Layer A: the deterministic validator.
//
// It does not reason and it does not call any LLM: it runs real checks (commands,
// invariants, expected output) over the agent's result and returns PASS/FAIL with
// structured records. If this layer cannot run, the result is FAIL, never an
// optimistic PASS.
package anchor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
)

// Result is the anchor's verdict.
type Result struct {
	Pass       bool       `json:"pass"`
	Checks     []CheckLog `json:"checks"`
	DurationMS int64      `json:"duration_ms"`
	Reason     string     `json:"reason"`
}

// CheckLog is the structured record of one check.
type CheckLog struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Exit       int    `json:"exit"`
	Pass       bool   `json:"pass"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	Truncated  bool   `json:"output_truncated,omitempty"`
}

// Anchor validates results according to the configuration.
type Anchor struct {
	cfg     config.Anchor
	dir     string // working directory the checks run in
	sandbox *sandbox.Sandbox
	log     *logx.Logger
}

// New builds an anchor. The sandbox is optional: when it is nil, the checks run
// directly on the system.
//
// Design note: the anchor is the authority, so by default it runs its checks
// OUTSIDE the agent's sandbox. If the sandbox had a flaw, the validator would
// share it, and a validator that fails silently lets bad results through. When a
// sandbox is passed in, it is because the caller decided to isolate validation
// too (for instance with the sandbox in chroot mode).
func New(cfg config.Anchor, dir string, box *sandbox.Sandbox) *Anchor {
	return &Anchor{cfg: cfg, dir: dir, sandbox: box, log: logx.Global()}
}

// Validator is the abstraction, so it can be replaced in tests.
type Validator interface {
	Validate(ctx context.Context) Result
}

// Validate runs every check. It returns PASS only when all of them pass.
func (a *Anchor) Validate(ctx context.Context) Result {
	start := time.Now()
	res := Result{Pass: true, Checks: []CheckLog{}}

	if strings.EqualFold(a.cfg.Kind, "none") || a.cfg.Kind == "" {
		// Without validation there is no possible verification, so PASS cannot
		// be declared. A PASS with no real check would be exactly the failure
		// this architecture exists to prevent.
		res.Pass = false
		res.Reason = "anchor.kind=none: there is no deterministic validation, the result can NOT be taken as verified"
		res.DurationMS = time.Since(start).Milliseconds()
		a.log.Error("anchor disabled: the result cannot be verified", "reason", res.Reason)
		return res
	}

	checks := a.checks()
	for _, c := range checks {
		record := a.runCheck(ctx, c)
		res.Checks = append(res.Checks, record)
		if !record.Pass {
			res.Pass = false
		}
	}

	if res.Pass {
		res.Reason = fmt.Sprintf("%d check(s) passed", len(res.Checks))
	} else {
		var failed []string
		for _, c := range res.Checks {
			if !c.Pass {
				failed = append(failed, c.Name)
			}
		}
		res.Reason = "failed checks: " + strings.Join(failed, ", ")
	}
	res.DurationMS = time.Since(start).Milliseconds()

	fields := []any{"pass", res.Pass, "reason", res.Reason, "duration_ms", res.DurationMS}
	if res.Pass {
		a.log.Info("anchor validation", fields...)
	} else {
		a.log.Warn("anchor validation", fields...)
	}
	return res
}

// checks normalises the configuration into a homogeneous list.
func (a *Anchor) checks() []config.Check {
	list := make([]config.Check, 0, 1+len(a.cfg.Checks))
	if a.cfg.Command != "" {
		list = append(list, config.Check{
			Name:         "main",
			Command:      a.cfg.Command,
			Args:         a.cfg.Args,
			Timeout:      a.cfg.Timeout,
			ExpectExit:   a.cfg.ExpectExit,
			ExpectOutput: a.cfg.ExpectOutput,
		})
	}
	list = append(list, a.cfg.Checks...)
	return list
}

// runCheck runs one check and evaluates its exit code and expected output.
func (a *Anchor) runCheck(ctx context.Context, c config.Check) CheckLog {
	name := c.Name
	if name == "" {
		name = "check"
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	line := strings.TrimSpace(c.Command + " " + strings.Join(c.Args, " "))
	record := CheckLog{Name: name, Command: line}

	start := time.Now()
	var (
		output    string
		exit      int
		runErr    error
		truncated bool
	)

	request := execx.Request{
		Command: c.Command,
		Args:    c.Args,
		Dir:     a.dir,
		Timeout: timeout,
	}
	if a.sandbox != nil {
		output, truncated, exit, runErr = a.sandbox.Run(ctx, request)
	} else {
		output, truncated, exit, runErr = runDirect(ctx, request)
	}

	record.DurationMS = time.Since(start).Milliseconds()
	record.Exit = exit
	record.Output = truncate(output, 4000)
	record.Truncated = truncated

	if runErr != nil {
		record.Pass = false
		record.Error = runErr.Error()
		return record
	}

	want := c.ExpectExit
	if exit != want {
		record.Pass = false
		if record.Error == "" {
			record.Error = fmt.Sprintf("expected exit code %d and got %d", want, exit)
		}
		return record
	}

	if c.ExpectOutput != "" {
		re, err := regexp.Compile(c.ExpectOutput)
		if err != nil {
			record.Pass = false
			record.Error = "expect_output is not a valid regular expression: " + err.Error()
			return record
		}
		if !re.MatchString(output) {
			record.Pass = false
			record.Error = fmt.Sprintf("the output does not match %q", c.ExpectOutput)
			return record
		}
	}

	record.Pass = true
	return record
}

// runDirect is the path without a sandbox (the anchor's checks when the sandbox
// is disabled or does not apply).
func runDirect(ctx context.Context, r execx.Request) (string, bool, int, error) {
	return execx.Run(ctx, r)
}

// truncate limits the size of the output kept in the record.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// JSON serialises the result for the structured record. The result only holds
// plain fields (booleans, numbers, strings and a slice of the same), so the
// marshalling cannot fail.
func (r Result) JSON() string {
	data, _ := json.Marshal(r)
	return string(data)
}
