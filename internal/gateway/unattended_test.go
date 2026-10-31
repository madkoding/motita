package gateway

import (
	"context"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/schedule"
)

// TestAScheduledRunReachesTheAgentAsUnattended: a run started for a schedule has nobody to answer a
// question, and the agent is told so through the context; a run a person started is not marked.
func TestAScheduledRunReachesTheAgentAsUnattended(t *testing.T) {
	marked := make(chan bool, 2)
	srv, c, svc := newTitleServer(t, func(context.Context, string) string { return "title" })
	defer waitForNoRun(t, srv)
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		marked <- agent.Unattended(ctx)
		return "ok", nil
	}

	rn, ok := srv.startDetachedRun(c, "scheduled", schedule.KindTask, "", srv.unattendedApprover("s1"))
	if !ok {
		t.Fatal("the scheduled run did not start")
	}
	<-rn.done
	if !<-marked {
		t.Error("a scheduled run must reach the agent as unattended")
	}

	rn, ok = srv.startDetachedRun(c, "typed", schedule.KindTask, "", srv.approverFactory(c))
	if !ok {
		t.Fatal("the person's run did not start")
	}
	<-rn.done
	if <-marked {
		t.Error("a run a person started must not be unattended")
	}
}
