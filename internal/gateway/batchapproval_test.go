package gateway

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// TestAlwaysAllowingABatchKeepsEachLineItsShape: "always allow" on a round's card keeps each of
// its lines under its own rule, so a later round with the same lines is approved without a card,
// and one with a line of another shape is asked about.
func TestAlwaysAllowingABatchKeepsEachLineItsShape(t *testing.T) {
	batch := func(lines ...string) agent.ApprovalRequest {
		req := agent.ApprovalRequest{Rule: agent.BatchRule}
		for _, l := range lines {
			req.Batch = append(req.Batch, agent.BatchItem{Command: l, Rule: "external-effect"})
			req.Command += l + "\n"
		}
		return req
	}
	var answers []bool
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		for _, req := range []agent.ApprovalRequest{batch("git push", "npm publish"),
			batch("git push origin main", "npm publish --tag x"), batch("git push", "curl x")} {
			ok, err := svc.approver(ctx, req)
			if err != nil {
				return "", err
			}
			answers = append(answers, ok)
		}
		return "ran", nil
	}
	srv := newTestServer(t, svc)
	br := startTask(t, srv)
	ask := waitForApproval(t, br)
	w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"),
		fmt.Sprintf(`{"id":%q,"approve":true,"scope":"project"}`, ask.ID), testToken)
	if w.Code != http.StatusNoContent || w.Header().Get(headerAlwaysAllow) != "kept" {
		t.Fatalf("answer = %d, header %q", w.Code, w.Header().Get(headerAlwaysAllow))
	}
	_, third := eventsUntilApproval(t, br)
	if third.Command != "git push\ncurl x\n" {
		t.Fatalf("only the round with a new shape must be asked about, got %q", third.Command)
	}
	post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"),
		fmt.Sprintf(`{"id":%q,"approve":false}`, third.ID), testToken)
	readEvents(t, br)
	if len(answers) != 3 || !answers[0] || !answers[1] || answers[2] {
		t.Errorf("answers = %v", answers)
	}
	if got := srv.rememberApprovals(srv.sessions[DefaultSession], []agent.BatchItem{{Command: "x", Rule: ""}}); got != "none" {
		t.Errorf("a batch with no shape to keep keeps none: %q", got)
	}
}
