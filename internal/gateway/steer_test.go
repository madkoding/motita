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

// steerExpect posts a message to the steer endpoint and checks the code and a fragment of the
// answer.
func steerExpect(t *testing.T, srv *Server, path, body string, code int, fragment string) {
	t.Helper()
	w := post(t, srv, path, body, testToken)
	if w.Code != code || !strings.Contains(w.Body.String(), fragment) {
		t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
	}
}

// stepMentions says whether a step of the session's checkpoints carries text.
func stepMentions(srv *Server, id, text string) bool {
	for _, cp := range srv.conversationOf(id).checkpointRecords() {
		for _, st := range cp.Steps {
			if strings.Contains(st.Text, text) {
				return true
			}
		}
	}
	return false
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

	steerExpect(t, srv, base+"/steer", `{"task":"  "}`, http.StatusBadRequest, "")
	steerExpect(t, srv, base+"/steer", `not json`, http.StatusBadRequest, "")
	merged := newSessionFor(t, srv)
	srv.conversationOf(merged.ID).setMerged("abc")
	steerExpect(t, srv, "/v1/sessions/"+merged.ID+"/steer", `{"task":"x"}`, http.StatusConflict, "")
	steerExpect(t, srv, base+"/steer", `{"task":"idle-start"}`, http.StatusAccepted, `"started":true`)
	waitUntil(t, "the session to go idle", func() bool { return !srv.conversationOf(a.ID).isRunning() })

	abandon := startInBackground(t, srv, a.ID, "/task", `{"task":"block-1"}`)
	defer abandon()
	waitBlocked(t, rec)
	steerExpect(t, srv, base+"/steer", `{"task":"steer: also this"}`, http.StatusAccepted, `"steered":true`)
	if len(probe.taken) != 1 || probe.taken[0] != "steer: also this" {
		t.Fatalf("taken %v", probe.taken)
	}
	if !stepMentions(srv, a.ID, "you: steer: also this") {
		t.Errorf("the message must be a step of the turn")
	}
	steerExpect(t, srv, base+"/steer", `{"task":"not taken"}`, http.StatusAccepted, `"queued":true`)
}
