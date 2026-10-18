package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/devbox"
)

// The agent saves a box's screenshots where the gateway looks for them when the run ends.
func TestBoxScreenshotsLandWhereThePreviewsAreRead(t *testing.T) {
	if agent.PreviewDir != PreviewDir {
		t.Fatalf("agent saves in %q, the gateway reads %q", agent.PreviewDir, PreviewDir)
	}
}

// A gateway that starts removes the service containers an earlier process left behind.
func TestStartingTheGatewaySweepsOrphanBoxes(t *testing.T) {
	swept := make(chan struct{}, 1)
	old := sweepBoxes
	t.Cleanup(func() { sweepBoxes = old })
	sweepBoxes = func(context.Context) int { swept <- struct{}{}; return 0 }
	(&Server{}).loadPersistedSessions()
	select {
	case <-swept:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep did not run")
	}
}

func boxCall(t *testing.T, srv *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(method, srv.BaseURL()+path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestTheBoxesCanBeListedAndStopped(t *testing.T) {
	oldA, oldR, oldS := boxActive, boxRemove, boxStop
	t.Cleanup(func() { boxActive, boxRemove, boxStop = oldA, oldR, oldS })
	var active []devbox.Info
	var activeErr, removeErr error
	var removed string
	boxActive = func(context.Context) (string, []devbox.Info, error) { return "podman", active, activeErr }
	boxRemove = func(_ context.Context, ref string) error { removed = ref; return removeErr }
	boxStop = func(context.Context) int { return 3 }
	srv := newTestServer(t, &fakeService{})

	w := boxCall(t, srv, http.MethodGet, "/v1/boxes")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `"boxes":[]`) || !strings.Contains(body, `"runtime":"podman"`) {
		t.Fatalf("an empty list is [], not null: %d %s", w.Code, w.Body)
	}
	active = []devbox.Info{{ID: "abc", Name: "motita-box-x-1", Image: "nginx", Status: "Up", Created: "now"}}
	w = boxCall(t, srv, http.MethodGet, "/v1/boxes")
	var view boxesView
	err := json.Unmarshal(w.Body.Bytes(), &view)
	if err != nil || len(view.Boxes) != 1 || view.Boxes[0].Name != "motita-box-x-1" {
		t.Fatalf("%v %s", err, w.Body)
	}
	activeErr = errors.New("daemon down")
	if w = boxCall(t, srv, http.MethodGet, "/v1/boxes"); w.Code != http.StatusBadGateway {
		t.Fatalf("%d", w.Code)
	}

	if w = boxCall(t, srv, http.MethodDelete, "/v1/boxes/abc"); w.Code != http.StatusNoContent || removed != "abc" {
		t.Fatalf("%d %q", w.Code, removed)
	}
	removeErr = devbox.ErrNotOurs
	if w = boxCall(t, srv, http.MethodDelete, "/v1/boxes/other"); w.Code != http.StatusNotFound {
		t.Fatalf("%d", w.Code)
	}
	removeErr = errors.New("busy")
	if w = boxCall(t, srv, http.MethodDelete, "/v1/boxes/abc"); w.Code != http.StatusBadGateway {
		t.Fatalf("%d", w.Code)
	}
	w = boxCall(t, srv, http.MethodDelete, "/v1/boxes")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"removed":3`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
