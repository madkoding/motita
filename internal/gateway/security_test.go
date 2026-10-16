package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// diskService is the fake that also takes a disk access, like the production runner.
type diskService struct {
	fakeService
	modes []string
}

func (d *diskService) SetDiskAccess(mode string) { d.modes = append(d.modes, mode) }

func decodeSecurity(t *testing.T, w *httptest.ResponseRecorder) securityView {
	t.Helper()
	var v securityView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func withConfinement(t *testing.T, available bool) {
	t.Helper()
	old := confinementAvailable
	confinementAvailable = func() bool { return available }
	t.Cleanup(func() { confinementAvailable = old })
}

// TestTheDiskAccessIsWorkspaceUntilAConfirmedChangeAndReachesEverySession: the default is the
// workspace, the whole disk is refused without confirm, a confirmed change is saved in the file and
// handed to the open conversations and to the ones started after it, and going back needs nothing.
func TestTheDiskAccessIsWorkspaceUntilAConfirmedChangeAndReachesEverySession(t *testing.T) {
	withConfinement(t, true)
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	svc := &diskService{}
	srv := newTestServer(t, svc, func(o *Options) {
		o.ConfigPath = path
		o.NewService = func() (Service, error) { return &diskService{}, nil }
	})

	v := decodeSecurity(t, get(t, srv, "/v1/security", testToken))
	if v.DiskAccess != config.DiskWorkspace || !v.CanSave || !v.Enforced {
		t.Fatalf("GET = %+v", v)
	}

	assertDiskAccessRefusals(t, srv, path, svc)

	v = decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"disk_access":" Full ","confirm":true}`))
	if v.DiskAccess != config.DiskFull || !mustLoad(t, path).Sandbox.FullDisk() {
		t.Fatalf("PUT full = %+v", v)
	}
	if len(svc.modes) != 1 || svc.modes[0] != config.DiskFull {
		t.Errorf("the open session takes the change: %v", svc.modes)
	}

	w := sendNetwork(t, srv, http.MethodPost, "/v1/sessions", `{}`)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	conv := srv.sessions[created.ID]
	if conv == nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if got := conv.svc.(*diskService).modes; len(got) != 1 || got[0] != config.DiskFull {
		t.Errorf("a new session starts with the saved access: %v", got)
	}

	v = decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"disk_access":"workspace"}`))
	if v.DiskAccess != config.DiskWorkspace || mustLoad(t, path).Sandbox.FullDisk() {
		t.Fatalf("going back to the workspace needs no confirmation: %+v", v)
	}
}

// assertDiskAccessRefusals checks that the whole disk is refused without confirm, that a value
// that is not an access is refused, and that neither reaches the file or a session.
func assertDiskAccessRefusals(t *testing.T, srv *Server, path string, svc *diskService) {
	t.Helper()
	for _, body := range []string{`{"disk_access":"full"}`, `{"disk_access":"full","confirm":false}`} {
		if w := sendNetwork(t, srv, http.MethodPut, "/v1/security", body); w.Code != http.StatusBadRequest ||
			!strings.Contains(w.Body.String(), "confirm") {
			t.Errorf("%s = %d %s, want 400", body, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"disk_access":"everything","confirm":true}`, `{"disk_access":""}`, `nope`} {
		if w := sendNetwork(t, srv, http.MethodPut, "/v1/security", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, w.Code)
		}
	}
	if mustLoad(t, path).Sandbox.FullDisk() || len(svc.modes) != 0 {
		t.Fatal("a refused change reached the file or a session")
	}
}

// TestTheDiskAccessSaysWhenTheKernelDoesNotConfineCommands: without Landlock, with the write
// confinement off, or inside a chroot, the limit holds for file actions only, and the view says so.
func TestTheDiskAccessSaysWhenTheKernelDoesNotConfineCommands(t *testing.T) {
	for name, c := range map[string]struct {
		kernel bool
		yaml   string
	}{
		"no landlock":     {false, "sandbox:\n  kind: none\n"},
		"confinement off": {true, "sandbox:\n  kind: none\n  confine_writes: false\n"},
		"chroot":          {true, "sandbox:\n  kind: chroot\n  root: /srv/jail\n"},
	} {
		withConfinement(t, c.kernel)
		srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, c.yaml) })
		if v := decodeSecurity(t, get(t, srv, "/v1/security", testToken)); v.Enforced {
			t.Errorf("%s: the commands are not confined: %+v", name, v)
		}
	}
}

func TestTheDiskAccessNeedsAFileAndReportsWhatFailed(t *testing.T) {
	withConfinement(t, true)
	running := config.Default()
	running.Sandbox.DiskAccess = config.DiskFull
	bare := newTestServer(t, &fakeService{cfg: running})
	v := decodeSecurity(t, get(t, bare, "/v1/security", testToken))
	if v.CanSave || v.DiskAccess != config.DiskFull {
		t.Fatalf("without a file the view is the running configuration: %+v", v)
	}
	w := sendNetwork(t, bare, http.MethodPut, "/v1/security", `{"disk_access":"workspace"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("no file = %d, want 409", w.Code)
	}

	path := networkConfig(t, "sandbox:\n  kind: none\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	oldSet := setDiskAccess
	setDiskAccess = func(config.Tx, string) error { return errors.New("read-only filesystem") }
	w = sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"disk_access":"workspace"}`)
	setDiskAccess = oldSet
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "read-only") {
		t.Errorf("a failed save = %d %s", w.Code, w.Body.String())
	}

	oldLoad := loadConfig
	loadConfig = func(string) (config.Config, error) { return config.Config{}, errors.New("broken yaml") }
	w = get(t, srv, "/v1/security", testToken)
	svc := &diskService{}
	srv.startingDiskAccess(svc)
	loadConfig = oldLoad
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "broken yaml") {
		t.Errorf("an unreadable file = %d %s", w.Code, w.Body.String())
	}
	if len(svc.modes) != 0 {
		t.Errorf("an unreadable file hands a new session nothing: %v", svc.modes)
	}
	bare.startingDiskAccess(svc)
	if len(svc.modes) != 0 {
		t.Errorf("without a file a new session keeps the running configuration: %v", svc.modes)
	}
}

// scopeService is the fake that also reports its sandbox, like the production runner.
type scopeService struct {
	fakeService
}

func (scopeService) SandboxScope() (string, []string) { return "/t/homes/shop", []string{"/w"} }

// A session's scope says where it works and what its sandbox keeps from its commands; a service
// with no sandbox to describe answers the folder alone, and a session that does not exist is 404.
func TestTheSessionScopeIsWhereItWorksAndWhatIsKeptFromIt(t *testing.T) {
	srv := newTestServer(t, &scopeService{})
	w := get(t, srv, "/v1/sessions/default/scope", testToken)
	var v sessionScope
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if v.Home != "/t/homes/shop" || len(v.PrivateRoots) != 1 || v.PrivateRoots[0] != "/w" {
		t.Errorf("scope: %+v", v)
	}
	bare := newTestServer(t, &fakeService{})
	w = get(t, bare, "/v1/sessions/default/scope", testToken)
	if !strings.Contains(w.Body.String(), `"private_roots":[]`) || !strings.Contains(w.Body.String(), `"home":""`) {
		t.Errorf("no sandbox: %s", w.Body.String())
	}
	if w := get(t, bare, "/v1/sessions/nope/scope", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown session: %d", w.Code)
	}
}
