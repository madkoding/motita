package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// autoService records which mode each turn was run in, and the text it was given.
func autoService(got *[]string) *fakeService {
	return &fakeService{
		plan: func(_ context.Context, p string, _ func(string, ...any)) (string, error) {
			*got = append(*got, "plan:"+p)
			return "planned", nil
		},
		task: func(_ context.Context, t string, _ func(string, ...any)) (string, error) {
			*got = append(*got, "task:"+t)
			return "done", nil
		},
	}
}

// Auto mode runs a question as a plan and an order as a task, and the first line of each stream
// says which mode it picked, so the person can see the guess before the agent acts on it.
func TestAutoRunPicksTheModeAndSaysWhich(t *testing.T) {
	for _, tc := range []struct{ body, want, note string }{
		{`{"task":"how does the scheduler work?"}`, "plan:how does the scheduler work?", "mode: plan (question)"},
		{`{"task":"fix the flaky test"}`, "task:fix the flaky test", `mode: task (action \"fix\")`},
		{`{"task":"/plan add a cache"}`, "plan:add a cache", "mode: plan (/plan)"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var got []string
			srv := newTestServer(t, autoService(&got))
			events := withoutPreamble(t, collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/run"), tc.body))
			if len(events) < 2 || events[0].Event != EventProgress || !strings.Contains(events[0].Data, tc.note) {
				t.Fatalf("the stream must open with the mode note %q: %+v", tc.note, events)
			}
			if events[len(events)-1].Event != EventDone {
				t.Errorf("the run did not finish: %+v", events)
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("ran %v, want %q", got, tc.want)
			}
		})
	}
}

func TestAutoRunRefusesWhatItCannotServe(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	path := sessionPath(srv, DefaultSession, "/run")
	for _, body := range []string{`not json`, `{"task":"  "}`, `{"task":"/plan"}`} {
		if w := post(t, srv, path, body, testToken); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, w.Code)
		}
	}
	c := srv.sessions[DefaultSession]
	if !c.takeRunSlot() {
		t.Fatal("the slot of an idle session was taken")
	}
	if w := post(t, srv, path, `{"task":"fix it"}`, testToken); w.Code != http.StatusConflict {
		t.Errorf("a busy session: status %d, want 409", w.Code)
	}
	c.releaseRunSlot()
	c.merged = true
	if w := post(t, srv, path, `{"task":"fix it"}`, testToken); w.Code != http.StatusConflict {
		t.Errorf("an integrated session: status %d, want 409", w.Code)
	}
}

// modelService is a fakeService with auto mode's model call.
type modelService struct {
	*fakeService
	answer string
	asked  int
}

func (m *modelService) CompleteIntent(context.Context, string, string) (string, error) {
	m.asked++
	return m.answer, nil
}

// When the rules have nothing, the service's model decides; when they do, it is not asked.
func TestAutoRunAsksTheModelOnlyWhenTheRulesHaveNothing(t *testing.T) {
	var got []string
	svc := &modelService{fakeService: autoService(&got), answer: `{"kind":"plan"}`}
	srv := newTestServer(t, svc)
	path := sessionPath(srv, DefaultSession, "/run")

	events := withoutPreamble(t, collect(t, srv, http.MethodPost, path, `{"task":"the retry policy"}`))
	if len(events) == 0 || !strings.Contains(events[0].Data, "mode: plan (model)") {
		t.Fatalf("the model's decision was not announced: %+v", events)
	}
	collect(t, srv, http.MethodPost, path, `{"task":"fix the retry policy"}`)
	if svc.asked != 1 {
		t.Errorf("the model was asked %d times, want 1", svc.asked)
	}
	if strings.Join(got, ",") != "plan:the retry policy,task:fix the retry policy" {
		t.Errorf("ran %v", got)
	}
}

// Sending the same message again with the other mode is logged as a correction of the guess.
func TestAutoRunLogsACorrection(t *testing.T) {
	var got []string
	sink := newLogSink(t)
	srv := newTestServer(t, autoService(&got), func(o *Options) { o.Log = sink.log })
	path := sessionPath(srv, DefaultSession, "/run")
	collect(t, srv, http.MethodPost, path, `{"task":"how does the cache work?"}`)
	collect(t, srv, http.MethodPost, path, `{"task":"/task how does the cache work?"}`)
	logged := strings.Join(sink.lines(t), "\n")
	if !strings.Contains(logged, "auto mode corrected") || !strings.Contains(logged, `"to":"task"`) {
		t.Errorf("the correction was not logged:\n%s", logged)
	}
}

func TestInfoWithoutALoggerDoesNothing(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.opts.Log = nil
	srv.info("nothing to write to")
}

// agent.auto_mode_rules_only keeps the model out of it: what the rules cannot place runs as a task.
func TestAutoRunRulesOnlyNeverAsksTheModel(t *testing.T) {
	var got []string
	base := autoService(&got)
	base.cfg.Agent.AutoModeRulesOnly = true
	svc := &modelService{fakeService: base, answer: `{"kind":"plan"}`}
	srv := newTestServer(t, svc)
	collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/run"), `{"task":"the retry policy"}`)
	if svc.asked != 0 || strings.Join(got, ",") != "task:the retry policy" {
		t.Errorf("asked %d, ran %v", svc.asked, got)
	}
}

// The mode note is the turn's first recorded step, so a reloaded conversation still shows it.
func TestAutoRunRecordsTheModeAsTheFirstStep(t *testing.T) {
	var got []string
	srv := newTestServer(t, autoService(&got))
	collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/run"), `{"task":"how does it work?"}`)
	cps := checkpointsOf(t, srv)
	if len(cps) != 1 || len(cps[0].Steps) == 0 || cps[0].Steps[0].Text != "mode: plan (question)" {
		t.Fatalf("checkpoints = %+v", cps)
	}
}
