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
