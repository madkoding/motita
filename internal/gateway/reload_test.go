package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// reloadingService is a service that can apply the configuration file as it is now.
type reloadingService struct {
	*fakeService
	err     error
	reloads int
	after   config.Config
}

func (r *reloadingService) ReloadConfig(context.Context) error {
	r.reloads++
	if r.err != nil {
		return r.err
	}
	r.cfg = r.after
	return nil
}

// /config in the terminal writes a new setup and then asks the gateway to apply it: the next turn
// must run with the new provider, not with the one the gateway started with.
func TestReloadAppliesTheNewSetup(t *testing.T) {
	after := config.Default()
	after.LLM.Provider, after.LLM.Model, after.LLM.APIKey = "anthropic", "claude-sonnet-5-5", "sk-secret"
	svc := &reloadingService{fakeService: &fakeService{cfg: config.Default()}, after: after}
	srv := newTestServer(t, svc)

	w := post(t, srv, sessionPath(srv, DefaultSession, "/config/reload"), "", testToken)
	if w.Code != http.StatusOK || svc.reloads != 1 {
		t.Fatalf("status = %d, reloads = %d: %s", w.Code, svc.reloads, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-secret") {
		t.Fatal("the answer must never carry the key")
	}
	var v configView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Provider != "anthropic" || v.Model != "claude-sonnet-5-5" || !v.APIKeyPresent {
		t.Errorf("view = %+v", v)
	}
}

func TestReloadReportsAFileThatDoesNotLoad(t *testing.T) {
	svc := &reloadingService{fakeService: &fakeService{}, err: errors.New("invalid YAML")}
	srv := newTestServer(t, svc)
	w := post(t, srv, sessionPath(srv, DefaultSession, "/config/reload"), "", testToken)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "invalid YAML") {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

// A service that cannot reload says so, rather than pretending the new setup was applied.
func TestReloadOnAServiceThatCannot(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := post(t, srv, sessionPath(srv, DefaultSession, "/config/reload"), "", testToken)
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "restart") {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

// The client asks for the reload and drops its cached view, so the interface draws the new setup.
func TestTheClientReloadsAndForgetsTheCachedView(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/config/reload") {
			calls++
			writeJSON(w, http.StatusOK, configView{Provider: "anthropic"})
			return
		}
		writeJSON(w, http.StatusOK, configView{Provider: "anthropic", Model: "m"})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, testToken)
	c.cfg, c.hasCfg = config.Default(), true
	if err := c.ReloadConfig(context.Background()); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	if calls != 1 {
		t.Errorf("the gateway must be asked once, got %d", calls)
	}
	if got := c.Config().LLM.Provider; got != "anthropic" {
		t.Errorf("the view must be read again after a reload, got %q", got)
	}
}
