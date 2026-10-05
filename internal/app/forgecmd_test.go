package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/oauth"
)

// fakeHost answers the API of a git host by "METHOD url-prefix".
type fakeHost struct {
	routes map[string]string
	seen   []string
	bodies []string
}

func (f *fakeHost) Do(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.String()
	f.seen = append(f.seen, key)
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		f.bodies = append(f.bodies, string(b))
	}
	for pattern, body := range f.routes {
		if strings.HasPrefix(key, pattern) {
			status := 200
			if rest, ok := strings.CutPrefix(body, "STATUS "); ok {
				code, payload, _ := strings.Cut(rest, " ")
				status = map[string]int{"401": 401, "403": 403, "404": 404, "422": 422, "500": 500}[code]
				body = payload
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		}
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{"message":"no route"}`)), Header: http.Header{}}, nil
}

// forgeFixture connects GitHub in a fresh auth dir, points the commands at a fake git and a
// fake host, and returns what they print.
type forgeFixture struct {
	t      *testing.T
	host   *fakeHost
	remote string
	branch string
	head   string // refs/remotes/origin/HEAD, "" means unset
	out    bytes.Buffer
	errs   bytes.Buffer
}

func newForge(t *testing.T, connected bool) *forgeFixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MOTITA_AUTH_DIR", dir)
	f := &forgeFixture{t: t, host: &fakeHost{routes: map[string]string{}}, remote: "git@github.com:o/r.git", branch: "feat/x", head: "origin/main"}
	if connected {
		s := gitforge.Store{Dir: dir}
		gh := gitforge.Defaults(func(string) string { return "" })[0]
		if err := s.Save(gh, oauth.Token{AccessToken: "gho_tok"}, "octo", false); err != nil {
			t.Fatal(err)
		}
	}
	oldGit, oldHTTP, oldSleep, oldEvery := forgeGit, forgeHTTP, forgeSleep, forgePollEvery
	forgeGit = func(_ context.Context, _ string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "remote get-url origin":
			if f.remote == "" {
				return "", errors.New("no such remote")
			}
			return f.remote, nil
		case "branch --show-current":
			return f.branch, nil
		case "symbolic-ref --short refs/remotes/origin/HEAD":
			if f.head == "" {
				return "", errors.New("not set")
			}
			return f.head, nil
		}
		t.Fatalf("unexpected git %v", args)
		return "", nil
	}
	forgeHTTP = f.host
	forgeSleep = func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		c <- time.Now()
		return c
	}
	t.Cleanup(func() { forgeGit, forgeHTTP, forgeSleep, forgePollEvery = oldGit, oldHTTP, oldSleep, oldEvery })
	return f
}

func (f *forgeFixture) run(args ...string) int {
	f.out.Reset()
	f.errs.Reset()
	op := Options{Args: append([]string{"forge"}, args...), Out: &f.out, Err: &f.errs, BaseCtx: context.Background()}
	return Run(op)
}

const prCreated = `{"number":12,"html_url":"https://github.com/o/r/pull/12"}`

func TestForgePRCreate(t *testing.T) {
	f := newForge(t, true)
	f.host.routes["GET https://api.github.com/repos/o/r/pulls?"] = `[]`
	f.host.routes["POST https://api.github.com/repos/o/r/pulls"] = prCreated
	code := f.run("pr", "create", "--title", "feat(auth): connect to github", "--body", "why")
	if code != Success {
		t.Fatalf("code %d: %s %s", code, f.out.String(), f.errs.String())
	}
	// The number comes WITH its link, ready to be repeated to the user.
	for _, want := range []string{"Pull request #12 opened: https://github.com/o/r/pull/12", "[o/r#12](https://github.com/o/r/pull/12)"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("missing %q in %q", want, f.out.String())
		}
	}
	last := f.host.bodies[len(f.host.bodies)-1]
	if !strings.Contains(last, `"head":"feat/x"`) || !strings.Contains(last, `"base":"main"`) || !strings.Contains(last, `"body":"why"`) {
		t.Errorf("body = %s", last)
	}
}

func TestForgePRCreateRefusesANonSemanticTitleBeforeAnythingElse(t *testing.T) {
	// Not even connected: the title is the first thing checked.
	f := newForge(t, false)
	for _, title := range []string{"", "Update stuff", "feat add"} {
		if code := f.run("pr", "create", "--title", title); code != ConfigError || !strings.Contains(f.errs.String(), "type(scope): description") {
			t.Errorf("title %q: code %d err %q", title, code, f.errs.String())
		}
	}
	if len(f.host.seen) != 0 {
		t.Errorf("the host must not be asked: %v", f.host.seen)
	}
}

func TestForgePRCreateReusesTheOpenPullRequest(t *testing.T) {
	f := newForge(t, true)
	f.host.routes["GET https://api.github.com/repos/o/r/pulls?"] = `[{"number":5,"html_url":"https://github.com/o/r/pull/5","title":"t","head":{"ref":"feat/x"},"base":{"ref":"main"}}]`
	if code := f.run("pr", "create", "--title", "fix: x"); code != Success || !strings.Contains(f.out.String(), "#5 is already open") {
		t.Fatalf("code %d out %q", code, f.out.String())
	}
	for _, s := range f.host.seen {
		if strings.HasPrefix(s, "POST") {
			t.Error("a second pull request must not be created")
		}
	}
}

func TestForgePRCreateInputs(t *testing.T) {
	f := newForge(t, true)
	f.host.routes["GET https://api.github.com/repos/o/r/pulls?"] = `[]`
	f.host.routes["POST https://api.github.com/repos/o/r/pulls"] = prCreated
	// A description from a file and from stdin; a draft; explicit base and head.
	file := filepath.Join(t.TempDir(), "body.md")
	_ = os.WriteFile(file, []byte("from file"), 0o600)
	if code := f.run("pr", "create", "--title", "feat: x", "--body-file", file, "--base", "develop", "--head", "other", "--draft"); code != Success {
		t.Fatalf("code %d: %s", code, f.errs.String())
	}
	last := f.host.bodies[len(f.host.bodies)-1]
	for _, want := range []string{`"body":"from file"`, `"base":"develop"`, `"head":"other"`, `"draft":true`} {
		if !strings.Contains(last, want) {
			t.Errorf("missing %s in %s", want, last)
		}
	}
	op := Options{Args: []string{"forge", "pr", "create", "--title", "feat: x", "--body-file", "-"}, Out: &f.out, Err: &f.errs, Stdin: strings.NewReader("from stdin"), BaseCtx: context.Background()}
	if code := Run(op); code != Success || !strings.Contains(f.host.bodies[len(f.host.bodies)-1], "from stdin") {
		t.Fatalf("code %d: %s", code, f.errs.String())
	}
	if code := f.run("pr", "create", "--title", "feat: x", "--body-file", filepath.Join(t.TempDir(), "missing")); code != ConfigError || !strings.Contains(f.errs.String(), "could not read") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	if code := f.run("pr", "create", "--nonsense"); code != ConfigError {
		t.Errorf("code %d", code)
	}
}

func TestForgePRCreateRefusals(t *testing.T) {
	f := newForge(t, true)
	f.branch = ""
	if code := f.run("pr", "create", "--title", "feat: x"); code != ConfigError || !strings.Contains(f.errs.String(), "no branch") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	f.branch = "main"
	if code := f.run("pr", "create", "--title", "feat: x"); code != ConfigError || !strings.Contains(f.errs.String(), "make a branch") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	// With no origin/HEAD the default is main.
	f.branch, f.head = "main", ""
	if code := f.run("pr", "create", "--title", "feat: x"); code != ConfigError {
		t.Errorf("code %d", code)
	}
	// The default branch is whatever origin says.
	f.branch, f.head = "develop", "origin/develop"
	if code := f.run("pr", "create", "--title", "feat: x"); code != ConfigError {
		t.Errorf("code %d", code)
	}
	f.head = "weird"
	f.branch = "main"
	if code := f.run("pr", "create", "--title", "feat: x"); code != ConfigError {
		t.Errorf("code %d", code)
	}
}

func TestForgePRCreateHostErrorsSayWhatToDo(t *testing.T) {
	f := newForge(t, true)
	f.host.routes["GET https://api.github.com/repos/o/r/pulls?"] = `[]`
	for status, want := range map[string]string{
		"401": "connect again",
		"403": "lack permission",
		"404": "lack permission",
		"422": "git push -u origin feat/x",
		"500": "HTTP 500",
	} {
		f.host.routes["POST https://api.github.com/repos/o/r/pulls"] = "STATUS " + status + ` {"message":"nope"}`
		if code := f.run("pr", "create", "--title", "feat: x"); code != RunError || !strings.Contains(f.errs.String(), want) {
			t.Errorf("%s: code %d err %q", status, code, f.errs.String())
		}
	}
	// A transport error is passed through as it is.
	if !errors.Is(describePRError(io.EOF, "b"), io.EOF) {
		t.Error("a plain error must be returned as it is")
	}
}

func TestForgeRepoResolution(t *testing.T) {
	f := newForge(t, false)
	if code := f.run("pr", "create", "--title", "feat: x"); code != RunError || !strings.Contains(f.errs.String(), "not connected") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	f.remote = ""
	if code := f.run("pr", "create", "--title", "feat: x"); code != RunError || !strings.Contains(f.errs.String(), "no remote named origin") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	f.remote = "https://example.org/a/b.git"
	if code := f.run("pr", "create", "--title", "feat: x"); code != RunError || !strings.Contains(f.errs.String(), "example.org is not a git host") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	f.remote = "nonsense"
	if code := f.run("pr", "create", "--title", "feat: x"); code != RunError || !strings.Contains(f.errs.String(), "not a repository address") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	// A login file that cannot be read is reported, not mistaken for "not connected".
	f.remote = "git@github.com:o/r.git"
	_ = os.WriteFile(oauth.CredentialPath(os.Getenv("MOTITA_AUTH_DIR"), "git-github"), []byte("{not json"), 0o600)
	if code := f.run("pr", "create", "--title", "feat: x"); code != RunError || !strings.Contains(f.errs.String(), "unreadable") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
}

func TestForgeChecks(t *testing.T) {
	f := newForge(t, true)
	f.host.routes["GET https://api.github.com/repos/o/r/pulls?"] = `[{"number":12,"html_url":"u","head":{"ref":"feat/x"},"base":{"ref":"main"}}]`
	f.host.routes["GET https://api.github.com/repos/o/r/pulls/12"] = `{"head":{"sha":"abc"}}`
	f.host.routes["GET https://api.github.com/repos/o/r/commits/abc/status"] = `{"statuses":[]}`
	f.host.routes["GET https://api.github.com/repos/o/r/commits/abc/check-runs"] = `{"check_runs":[{"id":7,"name":"build","status":"completed","conclusion":"success"}]}`
	if code := f.run("pr", "checks"); code != Success || !strings.Contains(f.out.String(), "o/r#12 CI: success") {
		t.Fatalf("code %d out %q", code, f.out.String())
	}
	// pr status is the same report, by number, and never waits.
	f.host.routes["GET https://api.github.com/repos/o/r/commits/abc/check-runs"] = `{"check_runs":[{"id":7,"name":"test","status":"in_progress"}]}`
	if code := f.run("pr", "status", "12"); code != forgeCIPending || !strings.Contains(f.out.String(), "pending") {
		t.Fatalf("code %d out %q", code, f.out.String())
	}
	// No checks at all is not a pass.
	f.host.routes["GET https://api.github.com/repos/o/r/commits/abc/check-runs"] = `{"check_runs":[]}`
	if code := f.run("pr", "checks", "12"); code != forgeCIPending || !strings.Contains(f.out.String(), "CI: none") {
		t.Fatalf("code %d out %q", code, f.out.String())
	}
	// A failure prints the job, its detail and its log.
	f.host.routes["GET https://api.github.com/repos/o/r/commits/abc/check-runs"] = `{"check_runs":[{"id":7,"name":"test","status":"completed","conclusion":"failure","html_url":"https://x/job/7","output":{"title":"2 failed"}},{"id":8,"name":"lint","status":"completed","conclusion":"success"},{"id":9,"name":"docs","status":"completed","conclusion":"failure"}]}`
	f.host.routes["GET https://api.github.com/repos/o/r/actions/jobs/7/logs"] = "FAIL TestX"
	if code := f.run("pr", "checks", "12", "--logs"); code != RunError {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"failure   test - 2 failed (https://x/job/7)", "--- end of the log of test ---", "FAIL TestX"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("missing %q in\n%s", want, f.out.String())
		}
	}
	// Without --logs the logs are not fetched.
	if code := f.run("pr", "checks", "12"); code != RunError || strings.Contains(f.out.String(), "FAIL TestX") {
		t.Errorf("code %d out %q", code, f.out.String())
	}
}

func TestForgeChecksWait(t *testing.T) {
	f := newForge(t, true)
	f.host.routes["GET https://api.github.com/repos/o/r/pulls/12"] = `{"head":{"sha":"abc"}}`
	f.host.routes["GET https://api.github.com/repos/o/r/commits/abc/status"] = `{"statuses":[]}`
	polls := 0
	pending := `{"check_runs":[{"id":7,"name":"test","status":"in_progress"}]}`
	done := `{"check_runs":[{"id":7,"name":"test","status":"completed","conclusion":"success"}]}`
	wait := &sequenceHost{inner: f.host, key: "GET https://api.github.com/repos/o/r/commits/abc/check-runs", bodies: []string{pending, pending, done}, calls: &polls}
	forgeHTTP = wait
	if code := f.run("pr", "checks", "12", "--wait"); code != Success || polls != 3 || !strings.Contains(f.out.String(), "success") {
		t.Fatalf("code %d polls %d out %q", code, polls, f.out.String())
	}
	// A timeout ends the wait with the CI still pending.
	polls = 0
	wait.bodies = []string{pending}
	if code := f.run("pr", "checks", "12", "--wait", "--timeout", "1ns"); code != forgeCIPending || polls != 1 {
		t.Fatalf("code %d polls %d", code, polls)
	}
	// Cancelling the context stops the wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	forgeSleep = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
	op := Options{Args: []string{"forge", "pr", "checks", "12", "--wait"}, Out: &f.out, Err: &f.errs, BaseCtx: ctx}
	if code := Run(op); code != RunError {
		t.Errorf("code %d", code)
	}
}

type sequenceHost struct {
	inner  *fakeHost
	key    string
	bodies []string
	calls  *int
}

func (s *sequenceHost) Do(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.Method+" "+req.URL.String(), s.key) {
		i := *s.calls
		if i >= len(s.bodies) {
			i = len(s.bodies) - 1
		}
		*s.calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(s.bodies[i])), Header: http.Header{}}, nil
	}
	return s.inner.Do(req)
}

func TestForgeChecksErrors(t *testing.T) {
	f := newForge(t, true)
	if code := f.run("pr", "checks", "zero"); code != ConfigError || !strings.Contains(f.errs.String(), "not a pull request number") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	if code := f.run("pr", "checks", "-3"); code != ConfigError && code != RunError {
		t.Errorf("code %d", code)
	}
	if code := f.run("pr", "checks", "--bogus"); code != ConfigError {
		t.Errorf("code %d", code)
	}
	// The host fails while looking for the branch's pull request, and has none.
	if code := f.run("pr", "checks"); code != RunError {
		t.Errorf("code %d", code)
	}
	f.host.routes["GET https://api.github.com/repos/o/r/pulls?"] = `[]`
	if code := f.run("pr", "checks"); code != RunError || !strings.Contains(f.errs.String(), "no open pull request") {
		t.Errorf("code %d: %s", code, f.errs.String())
	}
	// The host fails while reading the CI.
	if code := f.run("pr", "checks", "12"); code != RunError {
		t.Errorf("code %d", code)
	}
	// Not connected.
	g := newForge(t, false)
	if code := g.run("pr", "checks"); code != RunError {
		t.Errorf("code %d", code)
	}
}

func TestForgeStatusAndUsage(t *testing.T) {
	f := newForge(t, false)
	if code := f.run("status"); code != Success || !strings.Contains(f.out.String(), "no git host is connected") {
		t.Errorf("code %d out %q", code, f.out.String())
	}
	f = newForge(t, true)
	if code := f.run("status"); code != Success || !strings.Contains(f.out.String(), "GitHub: connected as octo") {
		t.Errorf("code %d out %q", code, f.out.String())
	}
	for _, args := range [][]string{{}, {"pr"}, {"pr", "merge"}, {"bogus"}} {
		if code := f.run(args...); code != ConfigError || !strings.Contains(f.errs.String(), "Usage: motita forge") {
			t.Errorf("%v: code %d err %q", args, code, f.errs.String())
		}
	}
	if code := f.run("help"); code != Success || !strings.Contains(f.out.String(), "pr create") {
		t.Errorf("code %d", code)
	}
}

func TestForgeGitDefault(t *testing.T) {
	out, err := forgeGit(context.Background(), t.TempDir(), "rev-parse", "--is-inside-work-tree")
	if err == nil || out != "" {
		t.Errorf("an empty directory is not a repository: %q %v", out, err)
	}
	if forgeStore().Dir == "" {
		t.Error("the store must have a directory")
	}
}

func TestGitCredentialSubcommand(t *testing.T) {
	f := newForge(t, true)
	var out, errs bytes.Buffer
	op := Options{Args: []string{"git-credential", "get"}, Out: &out, Err: &errs, Stdin: strings.NewReader("protocol=https\nhost=github.com\n\n"), BaseCtx: context.Background()}
	if code := Run(op); code != Success || out.String() != "username=x-access-token\npassword=gho_tok\n" {
		t.Fatalf("code %d out %q err %q", code, out.String(), errs.String())
	}
	// git also runs it with `store` and `erase`, and with no action at all.
	for _, args := range [][]string{{"git-credential", "store"}, {"git-credential"}} {
		out.Reset()
		op = Options{Args: args, Out: &out, Err: &errs, Stdin: strings.NewReader("host=github.com\n"), BaseCtx: context.Background()}
		if code := Run(op); code != Success || out.String() != "" {
			t.Errorf("%v: code %d out %q", args, code, out.String())
		}
	}
	_ = f
}
