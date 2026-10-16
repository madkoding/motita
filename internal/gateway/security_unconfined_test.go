package gateway

import (
	"encoding/json"
	"errors"
	"github.com/madkoding/motita/internal/config"
	"net/http"
	"os"
	"path/filepath"
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
	setAskUnconfinedRuns = func(config.Tx, bool) error { return errors.New("read-only filesystem") }
	t.Cleanup(func() { setAskUnconfinedRuns = old })
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"ask_unconfined_runs":true}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "read-only") {
		t.Errorf("a failed save = %d %s, want 500", w.Code, w.Body.String())
	}
}

// A change of several keys is all or nothing: when the second cannot be saved (the environment
// overrides it, say) the first is put back in the file and no conversation heard of it.
func TestAFailedSecondKeyLeavesTheFirstUnapplied(t *testing.T) {
	withConfinement(t, true)
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	svc := &diskService{}
	srv := newTestServer(t, svc, func(o *Options) {
		o.ConfigPath = path
		o.NewService = func() (Service, error) { return svc, nil }
	})
	if w := sendNetwork(t, srv, http.MethodPost, "/v1/sessions", `{}`); w.Code >= http.StatusBadRequest {
		t.Fatalf("a session = %d %s", w.Code, w.Body.String())
	}
	heard := len(svc.modes)
	old := setAskUnconfinedRuns
	setAskUnconfinedRuns = func(config.Tx, bool) error { return errors.New("overridden by the environment") }
	t.Cleanup(func() { setAskUnconfinedRuns = old })
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security",
		`{"disk_access":"full","ask_unconfined_runs":false,"confirm":true}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("PUT = %d %s, want 500", w.Code, w.Body.String())
	}
	if body, _ := os.ReadFile(path); strings.Contains(string(body), "full") {
		t.Errorf("the disk access stayed in the file:\n%s", body)
	}
	if len(svc.modes) != heard {
		t.Errorf("a conversation was handed %v of a change that failed", svc.modes[heard:])
	}
}

// When the file cannot be put back either, the answer says so.
func TestAFailedRestoreIsSaid(t *testing.T) {
	withConfinement(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "motita.yaml")
	if err := os.WriteFile(path, []byte("sandbox:\n  kind: none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	old := setAskUnconfinedRuns
	setAskUnconfinedRuns = func(config.Tx, bool) error {
		_ = os.RemoveAll(dir) // nowhere to write the file back
		return errors.New("overridden by the environment")
	}
	t.Cleanup(func() { setAskUnconfinedRuns = old })
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security",
		`{"disk_access":"full","ask_unconfined_runs":false,"confirm":true}`)
	if !strings.Contains(w.Body.String(), "could not be restored") {
		t.Errorf("PUT = %d %s, want the failed restore said", w.Code, w.Body.String())
	}
}

// A configuration file that cannot be read is an error, not a change applied blind.
func TestSecurityNeedsTheFileToBeReadable(t *testing.T) {
	withConfinement(t, true)
	path := filepath.Join(t.TempDir(), "gone.yaml")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"ask_unconfined_runs":true}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("PUT = %d %s, want 500", w.Code, w.Body.String())
	}
}
