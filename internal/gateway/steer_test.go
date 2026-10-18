package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// steerProbe is a queueProbe whose task in flight takes messages.
type steerProbe struct {
	queueProbe
	taken []string
}

func (p *steerProbe) Steer(text string) bool {
	if !strings.HasPrefix(text, "steer") {
		return false
	}
	p.taken = append(p.taken, text)
	return true
}

// A message sent to a task in flight is handed to it and shown as a step; one the task does not
// take is queued, and one sent to an idle session starts a run.
func TestSteerHandsTheMessageToTheTaskInFlight(t *testing.T) {
	rec := &queueRec{started: make(chan struct{}, 4), release: make(chan struct{})}
	probe := &steerProbe{queueProbe: queueProbe{rec: rec}}
	srv := newTestServer(t, probe, func(o *Options) {
		o.SessionDir = t.TempDir()
		o.NewService = func() (Service, error) { return probe, nil }
	})
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	t.Cleanup(func() { close(rec.release) })
	base := "/v1/sessions/" + a.ID

	for _, body := range []string{`{"task":"  "}`, `not json`} {
		if w := post(t, srv, base+"/steer", body, testToken); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", body, w.Code)
		}
	}
	merged := newSessionFor(t, srv)
	srv.conversationOf(merged.ID).setMerged("abc")
	if w := post(t, srv, "/v1/sessions/"+merged.ID+"/steer", `{"task":"x"}`, testToken); w.Code != http.StatusConflict {
		t.Fatalf("merged: %d", w.Code)
	}
	w := post(t, srv, base+"/steer", `{"task":"idle-start"}`, testToken)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"started":true`) {
		t.Fatalf("steer when idle: %d %s", w.Code, w.Body.String())
	}
	waitUntil(t, "the session to go idle", func() bool { return !srv.conversationOf(a.ID).isRunning() })

	abandon := startInBackground(t, srv, a.ID, "/task", `{"task":"block-1"}`)
	defer abandon()
	waitBlocked(t, rec)
	w = post(t, srv, base+"/steer", `{"task":"steer: also this"}`, testToken)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"steered":true`) {
		t.Fatalf("steer while busy: %d %s", w.Code, w.Body.String())
	}
	if len(probe.taken) != 1 || probe.taken[0] != "steer: also this" {
		t.Fatalf("taken %v", probe.taken)
	}
	cps := srv.conversationOf(a.ID).checkpointRecords()
	found := false
	for _, cp := range cps {
		for _, st := range cp.Steps {
			found = found || strings.Contains(st.Text, "you: steer: also this")
		}
	}
	if !found {
		t.Errorf("the message must be a step of the turn: %+v", cps)
	}
	w = post(t, srv, base+"/steer", `{"task":"not taken"}`, testToken)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"queued":true`) {
		t.Fatalf("a message the task does not take is queued: %d %s", w.Code, w.Body.String())
	}
}
