package gateway

import (
	"encoding/json"
	"errors"
	"github.com/madkoding/motita/internal/config"
	"net/http"
	"strings"
	"testing"
)

// isolatingService is the fake that also takes the network isolation, like the runner.
type isolatingService struct {
	fakeService
	isolate []bool
}

func (s *isolatingService) SetIsolateNetwork(on bool) { s.isolate = append(s.isolate, on) }

// withNetworkIsolation chooses whether this machine can run a command without a network.
func withNetworkIsolation(t *testing.T, available bool) {
	t.Helper()
	old := networkAvailable
	networkAvailable = func() bool { return available }
	t.Cleanup(func() { networkAvailable = old })
}

// TestTheNetworkIsolationIsOnUntilAConfirmedChange: the default isolates, turning it off is
// refused without confirm, a confirmed change is saved and reaches the open conversations and the
// ones started after it, and turning it back on needs nothing.
func TestTheNetworkIsolationIsOnUntilAConfirmedChange(t *testing.T) {
	withNetworkIsolation(t, true)
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	svc := &isolatingService{}
	srv := newTestServer(t, svc, func(o *Options) {
		o.ConfigPath = path
		o.NewService = func() (Service, error) { return &isolatingService{}, nil }
	})
	if v := decodeSecurity(t, get(t, srv, "/v1/security", testToken)); !v.IsolateNetwork || !v.NetworkEnforced {
		t.Fatalf("GET = %+v, want isolating", v)
	}
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"isolate_network":false}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "confirm") {
		t.Fatalf("unconfirmed = %d %s, want 400", w.Code, w.Body.String())
	}
	if !mustLoad(t, path).Sandbox.IsolateNetwork || len(svc.isolate) != 0 {
		t.Fatal("a refused change reached the file or a session")
	}

	v := decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security",
		`{"isolate_network":false,"confirm":true}`))
	if v.IsolateNetwork || mustLoad(t, path).Sandbox.IsolateNetwork || !v.AskUnconfinedRuns {
		t.Fatalf("PUT off = %+v", v)
	}
	if len(svc.isolate) != 1 || svc.isolate[0] {
		t.Errorf("the open session takes the change: %v", svc.isolate)
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
	if got := conv.svc.(*isolatingService).isolate; len(got) != 1 || got[0] {
		t.Errorf("a new session starts with the saved choice: %v", got)
	}

	v = decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"isolate_network":true}`))
	if !v.IsolateNetwork || !mustLoad(t, path).Sandbox.IsolateNetwork {
		t.Fatalf("turning it back on needs no confirmation: %+v", v)
	}
}

// A machine that cannot isolate says so, and a save that fails is reported.
func TestTheNetworkIsolationReportsTheMachineAndAFailedSave(t *testing.T) {
	withNetworkIsolation(t, false)
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	if v := decodeSecurity(t, get(t, srv, "/v1/security", testToken)); v.NetworkEnforced {
		t.Fatalf("GET = %+v, want not enforced", v)
	}
	old := setIsolateNetwork
	setIsolateNetwork = func(config.Tx, bool) error { return errors.New("read-only filesystem") }
	t.Cleanup(func() { setIsolateNetwork = old })
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"isolate_network":true}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "read-only") {
		t.Errorf("a failed save = %d %s, want 500", w.Code, w.Body.String())
	}
}

// Where the machine cannot isolate, the view says why and what would fix it; where it can, neither
// is sent.
func TestTheViewSaysWhyTheNetworkCannotBeIsolated(t *testing.T) {
	oldReason, oldHint := networkReason, networkHint
	networkReason = func() string { return "restricted" }
	networkHint = func() string { return "run sudo motita sandbox apparmor install" }
	t.Cleanup(func() { networkReason, networkHint = oldReason, oldHint })
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	srv := newTestServer(t, &isolatingService{}, func(o *Options) { o.ConfigPath = path })

	withNetworkIsolation(t, false)
	if v := decodeSecurity(t, get(t, srv, "/v1/security", testToken)); v.NetworkEnforced ||
		v.NetworkReason != "restricted" || !strings.Contains(v.NetworkHint, "apparmor install") {
		t.Errorf("unavailable: %+v", v)
	}
	withNetworkIsolation(t, true)
	if v := decodeSecurity(t, get(t, srv, "/v1/security", testToken)); v.NetworkReason != "" || v.NetworkHint != "" {
		t.Errorf("available: %+v", v)
	}
}
