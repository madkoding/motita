package tui

import (
	"bytes"
	"context"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
)

// The next turn is built from the configuration, so the setting reaches it there.
func TestSetAskUnconfinedRunsReachesTheConfiguration(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, nil, logx.Global())
	if !r.Config().Sandbox.AskUnconfinedRuns {
		t.Fatal("asking is the default")
	}
	r.SetAskUnconfinedRuns(false)
	if r.Config().Sandbox.AskUnconfinedRuns {
		t.Error("the change must reach the configuration")
	}
}

// The next report is stamped from the configuration, so the setting reaches it there.
func TestSetAutoApplySuggestionsReachesTheConfiguration(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, nil, logx.Global())
	if r.Config().Agent.AutoApplySuggestions {
		t.Fatal("off is the default")
	}
	r.SetAutoApplySuggestions(true)
	if !r.Config().Agent.AutoApplySuggestions {
		t.Error("the change must reach the configuration")
	}
}

// The next turn's agent is built from the configuration, so the desktop choice reaches it there.
func TestSetDesktopControlReachesTheConfiguration(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, nil, logx.Global())
	if r.Config().Agent.DesktopControl {
		t.Fatal("off is the default")
	}
	r.SetDesktopControl(true)
	if !r.Config().Agent.DesktopControl {
		t.Error("the change must reach the configuration")
	}
}

// The next command is planned from the configuration, so the setting reaches it there.
func TestSetApproveAllCommandsReachesTheConfiguration(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, nil, logx.Global())
	if r.Config().Agent.ApproveAllCommands {
		t.Fatal("asking is the default")
	}
	r.SetApproveAllCommands(true)
	if !r.Config().Agent.ApproveAllCommands {
		t.Error("the change must reach the configuration")
	}
}

// A turn already running follows a change made in the settings: the agent it built reads the
// choice from the runner at each command, not from the copy it was built with.
func TestApproveAllReachesTheAgentOfARunningTurn(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, nil, logx.Global())
	ag := agent.New(r.Config(), logx.Global(), &llm.Client{}, nil, nil)
	ag.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) { return "", false, 0, nil }
	asked := 0
	ag.SetApprover(func(context.Context, agent.ApprovalRequest) (bool, error) { asked++; return false, nil })
	r.installLiveApproveAll(ag)
	if _, _, err := ag.RunCommand(context.Background(), "sudo ls"); err == nil || asked != 1 {
		t.Fatalf("before the change sudo is asked about: asked %d, err %v", asked, err)
	}
	r.SetApproveAllCommands(true)
	if _, _, err := ag.RunCommand(context.Background(), "sudo ls"); err != nil || asked != 1 {
		t.Errorf("after the change the same agent runs it unasked: asked %d, err %v", asked, err)
	}
}
