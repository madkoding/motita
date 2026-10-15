package tui

import (
	"bytes"
	"testing"

	"github.com/madkoding/motita/internal/config"
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
