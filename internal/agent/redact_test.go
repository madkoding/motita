package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
)

// TestCommandOutputIsRedactedBeforeTheModelReadsIt: what a command prints goes into
// the prompt sent to the provider and into the transcript, so a token it printed is
// masked on the way, in the agent loop and in the tool-call path of plan and chat.
func TestCommandOutputIsRedactedBeforeTheModelReadsIt(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	const secret = "ghp_abcdefghijklmnopqrstuvwxyz0123"
	e.agent.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		return "origin https://x:" + secret + "@github.com/o/r.git (fetch)\n", false, 0, nil
	}

	output, err := e.agent.runActions(context.Background(), []Command{{Command: "cat .git/config"}}, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(output, secret) || !strings.Contains(output, "[REDACTED]") {
		t.Errorf("the agent loop hands the model a secret: %q", output)
	}

	output, _, err = e.agent.RunCommand(context.Background(), "cat .git/config")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(output, secret) || !strings.Contains(output, "[REDACTED]") {
		t.Errorf("the tool-call path hands the model a secret: %q", output)
	}
}
