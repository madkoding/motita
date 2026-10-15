package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// unconfinedService is the fake that also takes the unconfined-runs choice, like the runner.
type unconfinedService struct {
	fakeService
	asks []bool
}

func (u *unconfinedService) SetAskUnconfinedRuns(ask bool) { u.asks = append(u.asks, ask) }

// TestAskingBeforeUnconfinedRunsIsOnUntilAConfirmedChange: the default asks, turning it off is
// refused without confirm, a confirmed change is saved and reaches the open conversations and the
// ones started after it, and turning it back on needs nothing.
func TestAskingBeforeUnconfinedRunsIsOnUntilAConfirmedChange(t *testing.T) {
	withConfinement(t, false)
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	svc := &unconfinedService{}
	srv := newTestServer(t, svc, func(o *Options) {
		o.ConfigPath = path
		o.NewService = func() (Service, error) { return &unconfinedService{}, nil }
	})
	if v := decodeSecurity(t, get(t, srv, "/v1/security", testToken)); !v.AskUnconfinedRuns {
		t.Fatalf("GET = %+v, want asking", v)
	}
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"ask_unconfined_runs":false}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "confirm") {
		t.Fatalf("unconfirmed = %d %s, want 400", w.Code, w.Body.String())
	}
	if !mustLoad(t, path).Sandbox.AskUnconfinedRuns || len(svc.asks) != 0 {
		t.Fatal("a refused change reached the file or a session")
	}

	v := decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security",
		`{"ask_unconfined_runs":false,"confirm":true}`))
	if v.AskUnconfinedRuns || mustLoad(t, path).Sandbox.AskUnconfinedRuns || v.DiskAccess != "workspace" {
		t.Fatalf("PUT off = %+v", v)
	}
	if len(svc.asks) != 1 || svc.asks[0] {
		t.Errorf("the open session takes the change: %v", svc.asks)
	}

	w = sendNetwork(t, srv, http.MethodPost, "/v1/sessions", `{}`)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	conv := srv.sessions[created.ID]
	if conv == nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if got := conv.svc.(*unconfinedService).asks; len(got) != 1 || got[0] {
		t.Errorf("a new session starts with the saved choice: %v", got)
	}

	v = decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"ask_unconfined_runs":true}`))
	if !v.AskUnconfinedRuns || !mustLoad(t, path).Sandbox.AskUnconfinedRuns {
		t.Fatalf("turning it back on needs no confirmation: %+v", v)
	}
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("an empty change = %d, want 400", w.Code)
	}
}

// Both choices travel in one request, and a save that fails is reported.
func TestBothSecurityChoicesInOneRequestAndAFailedSave(t *testing.T) {
	withConfinement(t, true)
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	v := decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security",
		`{"disk_access":"full","ask_unconfined_runs":false,"confirm":true}`))
	if v.DiskAccess != "full" || v.AskUnconfinedRuns {
		t.Fatalf("PUT both = %+v", v)
	}
	old := setAskUnconfinedRuns
	setAskUnconfinedRuns = func(string, bool) error { return errors.New("read-only filesystem") }
	t.Cleanup(func() { setAskUnconfinedRuns = old })
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"ask_unconfined_runs":true}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "read-only") {
		t.Errorf("a failed save = %d %s, want 500", w.Code, w.Body.String())
	}
}
