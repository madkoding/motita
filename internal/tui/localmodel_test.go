package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/i18n"
	"github.com/madkoding/motita/internal/localmodel"
)

// localRunner is a runner whose gateway has the local model; it answers st and records the steps.
type localRunner struct {
	*fakeRunner
	st      localmodel.Status
	err     error
	actions []string
}

func (r *localRunner) LocalModel(_ context.Context, action string) (localmodel.Status, error) {
	r.actions = append(r.actions, action)
	return r.st, r.err
}

func localTUI(r *localRunner) *TUI {
	tu := newFakeTUI("", r)
	tu.Width, tu.Height = 110, 30
	return tu
}

func ready(over func(*localmodel.Status)) localmodel.Status {
	st := localmodel.Status{ModelID: localmodel.ModelID, ModelName: localmodel.ModelLabel, Size: localmodel.ModelSize,
		Eligible: true, GPU: &localmodel.GPU{Name: "RTX 3060", TotalMB: 12288}}
	if over != nil {
		over(&st)
	}
	return st
}

func TestLocalWithoutAGatewaySaysSo(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	commandActions["/local"](tu, context.Background(), "")
	if !strings.Contains(messagesOf(tu), "not attached to a gateway") {
		t.Errorf("messages = %q", messagesOf(tu))
	}
}

func TestLocalReportsEveryState(t *testing.T) {
	cases := []struct {
		name string
		st   localmodel.Status
		want string
	}{
		{"no gpu", ready(func(s *localmodel.Status) { s.GPU, s.Eligible = nil, false }), "GPU: none found"},
		{"small", ready(func(s *localmodel.Status) { s.GPU.TotalMB, s.Eligible = 2048, false }), "at least 4 GB"},
		{"not installed", ready(nil), "/local install downloads it (3.3 GB)"},
		{"runtime", ready(func(s *localmodel.Status) { s.Task = localmodel.Task{Running: true, Phase: "runtime"} }),
			"the runtime (llama.cpp): 0%"},
		{"model", ready(func(s *localmodel.Status) {
			s.Task = localmodel.Task{Running: true, Phase: "model", Done: 30, Total: 120}
		}), "the model: 25%"},
		{"stopped", ready(func(s *localmodel.Status) {
			s.ModelPresent, s.RuntimePresent = true, true
			s.Task.Error, s.Server.Error = "cancelled", "stopped"
		}), "Installed · stopped"},
		{"loading", ready(func(s *localmodel.Status) {
			s.ModelPresent, s.RuntimePresent, s.Server.Starting = true, true, true
		}), "Loading the model"},
		{"running", ready(func(s *localmodel.Status) {
			s.ModelPresent, s.RuntimePresent = true, true
			s.Server = localmodel.Server{Running: true, Port: 8471, Context: 16384, Error: "could not be saved"}
		}), "16384 tokens of context · port 8471. Pick it with /models qwen3-8b-gsq-q2_k"},
	}
	for _, c := range cases {
		r := &localRunner{fakeRunner: &fakeRunner{}, st: c.st}
		tu := localTUI(r)
		commandActions["/local"](tu, context.Background(), "")
		if got := messagesOf(tu); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q lacks %q", c.name, got, c.want)
		}
	}
	r := &localRunner{fakeRunner: &fakeRunner{}, st: ready(func(s *localmodel.Status) {
		s.ModelPresent, s.RuntimePresent, s.Task.Error = true, true, "404"
		s.Server.Error = "out of memory"
	})}
	tu := localTUI(r)
	tu.runLocal(context.Background(), "")
	if got := messagesOf(tu); !strings.Contains(got, "Last download: 404") || !strings.Contains(got, "Server: out of memory") {
		t.Errorf("errors: %q", got)
	}
}

func TestLocalStepsAndRefusals(t *testing.T) {
	r := &localRunner{fakeRunner: &fakeRunner{}, st: ready(nil)}
	tu := localTUI(r)
	for _, arg := range []string{"install", "START", "stop", "cancel", "delete confirm"} {
		tu.runLocal(context.Background(), arg)
	}
	if strings.Join(r.actions, ",") != "install,start,stop,cancel,delete" {
		t.Errorf("actions = %v", r.actions)
	}
	if !strings.Contains(messagesOf(tu), "Deleted.") {
		t.Error("delete says it deleted")
	}
	r.actions = nil
	tu.runLocal(context.Background(), "delete")
	tu.runLocal(context.Background(), "explode")
	if len(r.actions) != 0 {
		t.Errorf("a delete without confirm and an unknown step reach nothing: %v", r.actions)
	}
	got := messagesOf(tu)
	if !strings.Contains(got, "/local delete confirm") || !strings.Contains(got, "usage: /local") {
		t.Errorf("messages = %q", got)
	}
	r.err = errors.New("busy now")
	tu.runLocal(context.Background(), "start")
	if !strings.Contains(messagesOf(tu), "local: busy now") {
		t.Error("a refusal is shown")
	}
}

func TestLocalSpeaksSpanish(t *testing.T) {
	r := &localRunner{fakeRunner: &fakeRunner{}, st: ready(nil)}
	tu := localTUI(r)
	tu.Lang = i18n.ES
	tu.runLocal(context.Background(), "")
	if got := messagesOf(tu); !strings.Contains(got, "No instalado") || !strings.Contains(got, "Modelo local") {
		t.Errorf("messages = %q", got)
	}
}
