package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// planService is a service that keeps a pinned plan.
type planService struct {
	fakeService
	plan string
}

func (p *planService) PinnedPlan() string     { return p.plan }
func (p *planService) SetPinnedPlan(t string) { p.plan = strings.TrimSpace(t) }

func TestThePinnedPlanIsReadChangedAndDropped(t *testing.T) {
	svc := &planService{plan: "1. a\n2. b\n3. c"}
	srv := newTestServer(t, svc)
	path := sessionPath(srv, DefaultSession, "/plan")

	if got := body(t, get(t, srv, path, srv.Token())); got != `{"plan":"1. a\n2. b\n3. c"}`+"\n" {
		t.Errorf("read = %q", got)
	}
	w := send(t, srv, http.MethodPut, path, srv.Token(), `{"plan":" 1. x\n2. y\n3. z "}`)
	if w.Code != http.StatusOK || svc.plan != "1. x\n2. y\n3. z" || !strings.Contains(w.Body.String(), "1. x") {
		t.Errorf("change = %d %q, kept %q", w.Code, w.Body.String(), svc.plan)
	}
	if w := send(t, srv, http.MethodPut, path, srv.Token(), `{"plan":""}`); w.Code != http.StatusOK || svc.plan != "" {
		t.Errorf("drop = %d, kept %q", w.Code, svc.plan)
	}
	if w := send(t, srv, http.MethodPut, path, srv.Token(), `{`); w.Code != http.StatusBadRequest {
		t.Errorf("a broken body answered %d", w.Code)
	}
	if w := get(t, srv, path, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the plan is behind the token, answered %d", w.Code)
	}
}

func TestAServiceWithoutAPlanSaysSo(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	path := sessionPath(srv, DefaultSession, "/plan")
	if w := get(t, srv, path, srv.Token()); w.Code != http.StatusNotImplemented {
		t.Errorf("read answered %d", w.Code)
	}
	if w := send(t, srv, http.MethodPut, path, srv.Token(), `{"plan":"x"}`); w.Code != http.StatusNotImplemented {
		t.Errorf("change answered %d", w.Code)
	}
}
