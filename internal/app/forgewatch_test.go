package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// watchHost answers GitHub by exact path, each from a list of answers given one per reading of the
// pull request: the reading advances when the pull request itself is read for its state.
type watchHost struct {
	pulls    []string // GET /repos/o/r/pulls/12, one per reading
	checks   []string // check runs of commit abc, one per reading
	comments []string // issue comments, one per reading
	reviews  []string // reviews, one per reading
	lines    []string // line comments, one per reading
	reads    int
	fail     bool
}

func pick(list []string, i int) string {
	if len(list) == 0 {
		return "[]"
	}
	return list[min(i, len(list)-1)]
}

func (h *watchHost) Do(req *http.Request) (*http.Response, error) {
	body, status := "", 200
	path := strings.TrimPrefix(req.URL.Path, "/repos/o/r")
	i := max(h.reads-1, 0)
	switch {
	case h.fail:
		body, status = `{"message":"down"}`, 500
	case req.URL.Path == "/repos/o/r/pulls" || req.URL.Path == "/repos/o/r/pulls/":
		body = `[{"number":12,"html_url":"u","head":{"ref":"feat/x"},"base":{"ref":"main"}}]`
	case path == "/pulls/12":
		body = pick(h.pulls, i)
	case path == "/commits/abc/check-runs":
		body = pick(h.checks, i)
	case path == "/commits/abc/status":
		body = `{"statuses":[]}`
	case path == "/issues/12/comments":
		body = pick(h.comments, i)
	case path == "/pulls/12/reviews":
		body = pick(h.reviews, i)
	case path == "/pulls/12/comments":
		body = pick(h.lines, i)
	case path == "/actions/jobs/7/logs":
		body = "FAIL TestX"
	default:
		body, status = `{"message":"no route"}`, 404
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

// countingHost counts the readings: the first starts with the pull request's state, and each ends
// with its line comments, the last request of a reading.
type countingHost struct{ *watchHost }

func (c countingHost) Do(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/pulls/12") && c.watchHost.reads == 0 {
		c.watchHost.reads = 1
	}
	resp, err := c.watchHost.Do(req)
	if strings.HasSuffix(req.URL.Path, "/pulls/12/comments") {
		c.watchHost.reads++
	}
	return resp, err
}

const (
	prOpen    = `{"state":"open","merged":false,"head":{"sha":"abc"},"mergeable_state":"clean"}`
	prDirty   = `{"state":"open","merged":false,"head":{"sha":"abc"},"mergeable_state":"dirty"}`
	prMerged  = `{"state":"closed","merged":true,"merge_commit_sha":"m1","head":{"sha":"abc"}}`
	prClosed  = `{"state":"closed","merged":false,"head":{"sha":"abc"}}`
	runsGoing = `{"check_runs":[{"id":7,"name":"test","status":"in_progress"}]}`
	runsGreen = `{"check_runs":[{"id":7,"name":"test","status":"completed","conclusion":"success"}]}`
	runsRed   = `{"check_runs":[{"id":7,"name":"test","status":"completed","conclusion":"failure","html_url":"j"}]}`
	runsNone  = `{"check_runs":[]}`
)

func (f *forgeFixture) watch(h *watchHost, args ...string) int {
	forgeHTTP = countingHost{h}
	return f.run(append([]string{"pr", "watch"}, args...)...)
}

func TestForgeWatchReturnsOnAFailedCI(t *testing.T) {
	f := newForge(t, true)
	h := &watchHost{pulls: []string{prOpen}, checks: []string{runsGoing, runsGoing, runsRed}}
	if code := f.watch(h); code != RunError {
		t.Fatalf("code %d out %q err %q", code, f.out.String(), f.errs.String())
	}
	for _, want := range []string{"o/r#12: the CI failed.", "failure   test", "FAIL TestX"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("missing %q in\n%s", want, f.out.String())
		}
	}
	if h.reads < 3 {
		t.Errorf("it read %d times", h.reads)
	}
}

func TestForgeWatchPassedMergedAndClosed(t *testing.T) {
	f := newForge(t, true)
	if code := f.watch(&watchHost{pulls: []string{prOpen}, checks: []string{runsGreen}}, "12"); code != Success ||
		!strings.Contains(f.out.String(), "the CI passed") {
		t.Fatalf("pass: code %d out %q", code, f.out.String())
	}
	// Waiting for the merge goes on past the pass.
	h := &watchHost{pulls: []string{prOpen, prOpen, prMerged}, checks: []string{runsGreen}}
	if code := f.watch(h, "--until-merged"); code != Success || !strings.Contains(f.out.String(), "was merged") {
		t.Fatalf("merged: code %d out %q", code, f.out.String())
	}
	if code := f.watch(&watchHost{pulls: []string{prClosed}}); code != Success ||
		!strings.Contains(f.out.String(), "closed without being merged") {
		t.Fatalf("closed: code %d out %q", code, f.out.String())
	}
}

func TestForgeWatchReviewsAndConflicts(t *testing.T) {
	f := newForge(t, true)
	h := &watchHost{
		pulls:  []string{prOpen},
		checks: []string{runsGreen},
		comments: []string{`[{"id":1,"body":"old","user":{"login":"a"}}]`,
			`[{"id":1,"body":"old","user":{"login":"a"}},{"id":2,"body":"rename\nthis","html_url":"c2","user":{"login":"b"}}]`},
		reviews: []string{`[]`, `[{"id":3,"body":"no","state":"CHANGES_REQUESTED"}]`},
		lines:   []string{`[]`, `[{"id":4,"body":"here","path":"a.go"}]`},
	}
	if code := f.watch(h, "--until-merged"); code != RunError {
		t.Fatalf("review: code %d out %q", code, f.out.String())
	}
	for _, want := range []string{"3 new comment(s)", "- [line on a.go]\n  here", "- [comment by @b] c2",
		"  rename\n  this", "- [review changes_requested]\n  no"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("missing %q in\n%s", want, f.out.String())
		}
	}
	if strings.Contains(f.out.String(), "old") {
		t.Error("a comment from before the watch is not news")
	}
	h = &watchHost{pulls: []string{prOpen, prDirty}, checks: []string{runsGoing}}
	if code := f.watch(h); code != RunError || !strings.Contains(f.out.String(), "conflicts with its base") {
		t.Fatalf("conflict: code %d out %q", code, f.out.String())
	}
}

func TestForgeWatchNoCIAndTimeout(t *testing.T) {
	f := newForge(t, true)
	if code := f.watch(&watchHost{pulls: []string{prOpen}, checks: []string{runsNone}}); code != forgeCIPending ||
		!strings.Contains(f.out.String(), "no CI reported") {
		t.Fatalf("no ci: code %d out %q", code, f.out.String())
	}
	// No CI while waiting for the merge is not the end; a later merge is.
	h := &watchHost{pulls: []string{prOpen, prOpen, prOpen, prOpen, prOpen, prOpen, prMerged}, checks: []string{runsNone}}
	if code := f.watch(h, "--until-merged"); code != Success || !strings.Contains(f.out.String(), "was merged") {
		t.Fatalf("no ci then merged: code %d out %q", code, f.out.String())
	}
	// A timeout says so and what the CI was doing.
	forgeSleep = func(time.Duration) <-chan time.Time { return time.After(5 * time.Millisecond) }
	going := &watchHost{pulls: []string{prOpen}, checks: []string{runsGoing}}
	if code := f.watch(going, "--timeout", "30ms"); code != forgeCIPending ||
		!strings.Contains(f.out.String(), "nothing new after 30ms (CI: pending)") {
		t.Fatalf("timeout: code %d out %q", code, f.out.String())
	}
	if code := f.watch(&watchHost{fail: true}, "12", "--timeout", "1ns"); code != forgeCIPending ||
		!strings.Contains(f.out.String(), "(CI: none)") {
		t.Fatalf("timeout before any reading: code %d out %q", code, f.out.String())
	}
}

func TestForgeWatchErrors(t *testing.T) {
	f := newForge(t, true)
	if code := f.run("pr", "watch", "--bogus"); code != ConfigError {
		t.Errorf("code %d", code)
	}
	if code := f.run("pr", "watch", "x"); code != ConfigError {
		t.Errorf("code %d", code)
	}
	// A host that keeps failing ends the watch with its error.
	if code := f.watch(&watchHost{fail: true}, "12"); code != RunError || !strings.Contains(f.errs.String(), "down") {
		t.Errorf("code %d err %q", code, f.errs.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	forgeHTTP = countingHost{&watchHost{pulls: []string{prOpen}}}
	op := Options{Args: []string{"forge", "pr", "watch", "12"}, Out: &f.out, Err: &f.errs, BaseCtx: ctx}
	if code := Run(op); code != RunError {
		t.Errorf("cancelled: code %d", code)
	}
	if code := newForge(t, false).run("pr", "watch"); code != RunError {
		t.Errorf("not connected: code %d", code)
	}
}
