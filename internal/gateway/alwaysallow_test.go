package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// askingService asks the gateway about every command of cmds and records the answers.
func approvalAskingService(cmds []string, answers *[]bool) *fakeService {
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		for _, cmd := range cmds {
			ok, err := svc.approver(ctx, agent.ApprovalRequest{Command: cmd, Reason: "r", Rule: "shell"})
			if err != nil {
				return "", err
			}
			*answers = append(*answers, ok)
		}
		return "ran", nil
	}
	return svc
}

func startTask(t *testing.T, srv *Server) *bufio.Reader {
	t.Helper()
	url := srv.BaseURL() + sessionPath(srv, DefaultSession, "/task")
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"task":"x"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return bufio.NewReader(resp.Body)
}

// eventsUntilApproval reads the stream up to the next approval question, keeping what came
// before it.
func eventsUntilApproval(t *testing.T, br *bufio.Reader) ([]event, approvalEvent) {
	t.Helper()
	var seen []event
	name := ""
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended before an approval was asked: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		if strings.HasPrefix(line, "event: ") {
			name = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if name != EventApproval {
			seen = append(seen, event{Event: name, Data: data})
			continue
		}
		var ask approvalEvent
		if err := json.Unmarshal([]byte(data), &ask); err != nil {
			t.Fatalf("approval payload = %q: %v", line, err)
		}
		return seen, ask
	}
}

// TestAlwaysAllowingAShapeAnswersItInLaterRuns: "always allow this here" answers the same shape
// of command without asking, in this run and in a later one, and says so; another shape is
// still asked about. The decision survives a restart of the gateway.
func TestAlwaysAllowingAShapeAnswersItInLaterRuns(t *testing.T) {
	dir := t.TempDir()
	var answers []bool
	svc := approvalAskingService([]string{"npm install", "npm install --save x", "npm test"}, &answers)
	srv := newTestServer(t, svc, func(o *Options) { o.SessionDir = dir })

	br := startTask(t, srv)
	ask := waitForApproval(t, br)
	if ask.Command != "npm install" {
		t.Fatalf("asked about %q first", ask.Command)
	}
	w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"),
		fmt.Sprintf(`{"id":%q,"approve":true,"scope":"project"}`, ask.ID), testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("answer = %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(headerAlwaysAllow); got != "kept" {
		t.Errorf("header = %q, want kept", got)
	}
	before, second := eventsUntilApproval(t, br)
	if second.Command != "npm test" {
		t.Fatalf("the second question must be about the other shape, got %q", second.Command)
	}
	post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"),
		fmt.Sprintf(`{"id":%q,"approve":false}`, second.ID), testToken)
	readEvents(t, br)
	said := false
	for _, e := range before {
		said = said || (e.Event == EventProgress && strings.Contains(string(e.Data), "always allowed in this project") &&
			strings.Contains(string(e.Data), "npm install --save x"))
	}
	if !said || !slices.Equal(answers, []bool{true, true, false}) {
		t.Fatalf("said=%v answers=%v", said, answers)
	}
	if srv.sessions[DefaultSession].status().AutoApprove {
		t.Error("always allowing one shape is not allowing every command in the session")
	}
	if _, err := os.Stat(filepath.Join(dir, approvalsFile)); err != nil {
		t.Fatalf("the decision must be written: %v", err)
	}

	// A new gateway over the same directory, and a typed shell line of that shape, needs no yes.
	answers = nil
	again := newTestServer(t, approvalAskingService([]string{"npm install y"}, &answers),
		func(o *Options) { o.SessionDir = dir })
	br = startTask(t, again)
	for _, e := range readEvents(t, br) {
		if e.Event == EventApproval {
			t.Fatalf("asked again after a restart: %s", e.Data)
		}
	}
	if len(answers) != 1 || !answers[0] {
		t.Fatalf("answers = %v", answers)
	}
}

// askingShell asks about every typed line and records it.
type askingShell struct {
	fakeService
	asked []string
}

func (p *askingShell) RunShell(ctx context.Context, cmd string, approve agent.Approver) (string, int, error) {
	p.asked = append(p.asked, cmd)
	ok, _ := approve(ctx, agent.ApprovalRequest{Command: cmd, Reason: "r", Rule: "shell"})
	if !ok {
		return "", 0, nil
	}
	return "ran " + cmd, 0, nil
}

// The typed shell line keeps and honours the decision as the agent's commands do.
func TestAShellLineCanBeAlwaysAllowedToo(t *testing.T) {
	svc := &askingShell{}
	srv := newTestServer(t, svc)
	path := sessionPath(srv, DefaultSession, "/shell")
	w := post(t, srv, path, `{"command":"git push"}`, testToken)
	if !strings.Contains(w.Body.String(), `"needs_approval":true`) {
		t.Fatalf("first: %s", w.Body.String())
	}
	w = post(t, srv, path, `{"command":"git push","approved":true,"scope":"project"}`, testToken)
	if !strings.Contains(w.Body.String(), "ran git push") {
		t.Fatalf("approved: %s", w.Body.String())
	}
	if got := w.Header().Get(headerAlwaysAllow); got != "kept" {
		t.Errorf("header = %q, want kept", got)
	}
	w = post(t, srv, path, `{"command":"git push --tags"}`, testToken)
	if got := w.Header().Get(headerAlwaysAllow); got != "" {
		t.Errorf("a plain run says nothing about always allow: %q", got)
	}
	if !strings.Contains(w.Body.String(), "ran git push --tags") {
		t.Fatalf("the same shape must run without a yes: %s", w.Body.String())
	}
	if len(svc.asked) != 3 {
		t.Errorf("asked %v", svc.asked)
	}
}

// A store that cannot be read is replaced by an empty one, with a warning, and the gateway
// still answers; a session outside a project is scoped by its workspace.
func TestABadApprovalFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, approvalsFile), []byte("not json"), 0o600)
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.SessionDir = dir })
	c := srv.sessions[DefaultSession]
	if srv.alwaysAllowed(c, "shell", "git push") {
		t.Fatal("an empty store allows nothing")
	}
	if !strings.HasPrefix(approvalScope(c), "workspace:") {
		t.Errorf("scope = %q", approvalScope(c))
	}
	c.projectID = "p1"
	if approvalScope(c) != "project:p1" {
		t.Errorf("scope = %q", approvalScope(c))
	}
	// Nothing to remember in a blank line, and a store that cannot write is only warned about.
	if got := srv.rememberApproval(c, "shell", "   "); got != "none" {
		t.Errorf("a blank line has no shape to keep: %q", got)
	}
	_ = os.Remove(filepath.Join(dir, approvalsFile))
	_ = os.Mkdir(filepath.Join(dir, approvalsFile+".tmp"), 0o700)
	srv.rememberApproval(c, "shell", "git push")
	if !srv.alwaysAllowed(c, "shell", "git push --force") {
		t.Fatal("remembered in memory even when the file could not be written")
	}
}
