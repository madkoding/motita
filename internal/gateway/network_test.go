package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/tailscale"
)

// fakeTailscale is a scripted Tailscale that records what it was asked to do.
type fakeTailscale struct {
	mu       sync.Mutex
	status   tailscale.Status
	serveErr error
	served   []string
	unserved int
}

func (f *fakeTailscale) Detect(_ context.Context, _ string) tailscale.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeTailscale) Serve(_ context.Context, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.served = append(f.served, target)
	if f.serveErr == nil {
		f.status.Serving = true
	}
	return f.serveErr
}

func (f *fakeTailscale) Unserve(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unserved++
	if f.serveErr == nil {
		f.status.Serving = false
	}
	return f.serveErr
}

func sendNetwork(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(method, srv.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func decodeNetwork(t *testing.T, w *httptest.ResponseRecorder) networkView {
	t.Helper()
	var v networkView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func networkConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTheExposureIsSavedOnlyWithAConfirmationAndAppliesAtRestart: the page sees what is bound now
// and what the file says, a change without confirm is refused, and a confirmed one is written to
// the file while the running socket stays where it is.
func TestTheExposureIsSavedOnlyWithAConfirmationAndAppliesAtRestart(t *testing.T) {
	path := networkConfig(t, "gateway:\n  enabled: true\n  listen: \"127.0.0.1:0\"\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })

	v := decodeNetwork(t, get(t, srv, "/v1/network", testToken))
	if v.Mode != config.ExposureLocal || v.ActiveMode != config.ExposureLocal || v.RestartNeeded || !v.CanSave ||
		v.Listen != "127.0.0.1:0" || v.ActiveListen != srv.Addr() || v.Tailscale.Installed {
		t.Fatalf("GET = %+v", v)
	}

	for _, body := range []string{`{"mode":"lan"}`, `{"mode":"lan","confirm":false}`} {
		if w := sendNetwork(t, srv, http.MethodPut, "/v1/network", body); w.Code != http.StatusBadRequest ||
			!strings.Contains(w.Body.String(), "confirm") {
			t.Errorf("%s = %d %s, want 400", body, w.Code, w.Body.String())
		}
	}
	if config.ListenChosen(path) && mustLoad(t, path).Gateway.Exposure() != config.ExposureLocal {
		t.Fatal("an unconfirmed change reached the file")
	}
	for _, body := range []string{`{"mode":"custom","confirm":true}`, `{"mode":"","confirm":true}`, `nope`} {
		if w := sendNetwork(t, srv, http.MethodPut, "/v1/network", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, w.Code)
		}
	}

	v = decodeNetwork(t, sendNetwork(t, srv, http.MethodPut, "/v1/network", `{"mode":" LAN ","confirm":true}`))
	if v.Mode != config.ExposureLAN || v.ActiveMode != config.ExposureLocal || !v.RestartNeeded || v.Listen != "0.0.0.0:0" {
		t.Fatalf("PUT = %+v", v)
	}
	if got := mustLoad(t, path).Gateway; got.Exposure() != config.ExposureLAN {
		t.Fatalf("the file says %+v", got)
	}
}

func mustLoad(t *testing.T, path string) config.Config {
	t.Helper()
	cfg, err := config.LoadWithoutKey(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTheExposureNeedsAFileAndReportsWhatFailed(t *testing.T) {
	bare := newTestServer(t, &fakeService{}, func(o *Options) {
		o.AllowRules = []string{"lan", "!any"}
	})
	v := decodeNetwork(t, get(t, bare, "/v1/network", testToken))
	if v.CanSave || v.RestartNeeded || v.Mode != v.ActiveMode {
		t.Fatalf("without a file the saved view is the running one: %+v", v)
	}
	if w := sendNetwork(t, bare, http.MethodPut, "/v1/network", `{"mode":"all","confirm":true}`); w.Code != http.StatusConflict {
		t.Errorf("no file = %d, want 409", w.Code)
	}

	path := networkConfig(t, "gateway:\n  enabled: true\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	oldSet := setGatewayExposure
	setGatewayExposure = func(string, string) error { return errors.New("read-only filesystem") }
	w := sendNetwork(t, srv, http.MethodPut, "/v1/network", `{"mode":"all","confirm":true}`)
	setGatewayExposure = oldSet
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "read-only") {
		t.Errorf("a failed save = %d %s", w.Code, w.Body.String())
	}

	oldLoad := loadConfig
	loadConfig = func(string) (config.Config, error) { return config.Config{}, errors.New("broken yaml") }
	w = get(t, srv, "/v1/network", testToken)
	loadConfig = oldLoad
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "broken yaml") {
		t.Errorf("an unreadable file = %d %s", w.Code, w.Body.String())
	}
}

// TestTailscaleIsPublishedAndItsNameAccepted: turning the publication on serves the bound address
// and makes the gateway answer to its tailnet name; turning it off removes it again.
func TestTailscaleIsPublishedAndItsNameAccepted(t *testing.T) {
	ts := &fakeTailscale{status: tailscale.Status{Installed: true, Running: true, State: "Running",
		DNSName: "box.tail1.ts.net", URL: "https://box.tail1.ts.net"}}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Tailscale = ts })

	// The name is learned at start, in the background.
	deadline := time.Now().Add(2 * time.Second)
	for !srv.knowsHost("box.tail1.ts.net") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !srv.knowsHost("box.tail1.ts.net") {
		t.Fatal("the tailnet name was not learned at start")
	}

	v := decodeNetwork(t, sendNetwork(t, srv, http.MethodPost, "/v1/network/tailscale", `{"enable":true}`))
	if !v.Tailscale.Serving || len(ts.served) != 1 || ts.served[0] != tailscale.Target(srv.Addr()) {
		t.Fatalf("serve: %+v %v", v.Tailscale, ts.served)
	}
	v = decodeNetwork(t, sendNetwork(t, srv, http.MethodPost, "/v1/network/tailscale", `{"enable":false}`))
	if v.Tailscale.Serving || ts.unserved != 1 {
		t.Fatalf("unserve: %+v", v.Tailscale)
	}

	ts.mu.Lock()
	ts.serveErr = errors.New("tailscale: Access denied: serve config denied")
	ts.mu.Unlock()
	w := sendNetwork(t, srv, http.MethodPost, "/v1/network/tailscale", `{"enable":true}`)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "Access denied") {
		t.Errorf("a failed serve = %d %s", w.Code, w.Body.String())
	}
	if w := sendNetwork(t, srv, http.MethodPost, "/v1/network/tailscale", `nope`); w.Code != http.StatusBadRequest {
		t.Errorf("a malformed body = %d", w.Code)
	}
}

func TestTailscaleMustBeRunningToPublish(t *testing.T) {
	for name, opt := range map[string]func(*Options){
		"not looked for": func(*Options) {},
		"stopped": func(o *Options) {
			o.Tailscale = &fakeTailscale{status: tailscale.Status{Installed: true, State: "Stopped"}}
		},
	} {
		srv := newTestServer(t, &fakeService{}, opt)
		if w := sendNetwork(t, srv, http.MethodPost, "/v1/network/tailscale", `{"enable":true}`); w.Code != http.StatusConflict {
			t.Errorf("%s: %d, want 409", name, w.Code)
		}
	}
}

func TestAddHostIgnoresAnEmptyName(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.addHost(" ")
	if srv.knowsHost("") {
		t.Fatal("an empty name was added")
	}
}

// TestTheGatewayRestartsToApplyASavedExposure: only with a confirmation, only where it can, and
// the answer reaches the page before the process stops.
func TestTheGatewayRestartsToApplyASavedExposure(t *testing.T) {
	old := restartDelay
	restartDelay = 0
	t.Cleanup(func() { restartDelay = old })

	bare := newTestServer(t, &fakeService{})
	if v := decodeNetwork(t, get(t, bare, "/v1/network", testToken)); v.CanRestart {
		t.Fatal("a gateway without Restart says it can restart")
	}
	if w := sendNetwork(t, bare, http.MethodPost, "/v1/network/restart", `{"confirm":true}`); w.Code != http.StatusConflict {
		t.Errorf("no Restart = %d, want 409", w.Code)
	}

	restarted := make(chan struct{}, 1)
	path := networkConfig(t, "gateway:\n  enabled: true\n  listen: \"0.0.0.0:0\"\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ConfigPath = path
		o.Restart = func() { restarted <- struct{}{} }
	})
	for _, body := range []string{`{}`, `{"confirm":false}`, `nope`} {
		if w := sendNetwork(t, srv, http.MethodPost, "/v1/network/restart", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, w.Code)
		}
	}

	oldLoad := loadConfig
	loadConfig = func(string) (config.Config, error) { return config.Config{}, errors.New("broken yaml") }
	w := sendNetwork(t, srv, http.MethodPost, "/v1/network/restart", `{"confirm":true}`)
	loadConfig = oldLoad
	if w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable file = %d, want 500", w.Code)
	}
	select {
	case <-restarted:
		t.Fatal("restarted although the configuration could not be read")
	default:
	}

	w = sendNetwork(t, srv, http.MethodPost, "/v1/network/restart", `{"confirm":true}`)
	var v networkView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusAccepted ||
		!v.CanRestart || v.Mode != config.ExposureAll || !v.RestartNeeded {
		t.Fatalf("restart = %d %s", w.Code, w.Body.String())
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Restart was not called")
	}
}
