package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/updater"
)

// updateGateway answers both update endpoints. check is the JSON for /v1/update/check, and run is
// the raw body of /v1/update/run so a test can send a stream that is malformed or cut short.
func updateGateway(t *testing.T, status int, check any, run string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status >= http.StatusBadRequest {
			writeError(w, status, "no")
			return
		}
		switch r.URL.Path {
		case "/v1/update/check":
			writeJSON(w, http.StatusOK, check)
		case "/v1/update/run":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, run)
		}
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, testToken)
}

func sseEvent(evt updater.ProgressEvent) string {
	return fmt.Sprintf("event: progress\ndata: {\"stage\":%q,\"message\":%q,\"version\":%q}\n\n", evt.Stage, evt.Message, evt.Version)
}

func TestUpdateAvailableReportsTheGatewaysAnswer(t *testing.T) {
	c := updateGateway(t, http.StatusOK, updater.CheckResult{CurrentVersion: "v1.0.0", LatestVersion: "v1.1.0", UpdateAvailable: true}, "")
	cur, latest, ok, err := c.UpdateAvailable(context.Background())
	if err != nil || cur != "v1.0.0" || latest != "v1.1.0" || !ok {
		t.Fatalf("got %q %q %v %v", cur, latest, ok, err)
	}
}

// TestAFailedCheckIsAnErrorNotUpToDate: "nobody could look" must not read as "nothing to install".
func TestAFailedCheckIsAnErrorNotUpToDate(t *testing.T) {
	c := updateGateway(t, http.StatusOK, updater.CheckResult{CurrentVersion: "v1.0.0", Error: "could not reach GitHub"}, "")
	_, _, ok, err := c.UpdateAvailable(context.Background())
	if err == nil || ok || !strings.Contains(err.Error(), "GitHub") {
		t.Fatalf("a failed check must be an error, got ok=%v err=%v", ok, err)
	}
}

func TestUpdateAvailableSurfacesARefusal(t *testing.T) {
	c := updateGateway(t, http.StatusUnauthorized, nil, "")
	if _, _, _, err := c.UpdateAvailable(context.Background()); err == nil {
		t.Fatal("a refused request must be an error")
	}
}

func TestRunUpdateReportsEachStageOnceAndReturnsTheVersion(t *testing.T) {
	run := sseEvent(updater.ProgressEvent{Stage: "checking", Message: "Checking"}) +
		sseEvent(updater.ProgressEvent{Stage: "downloading", Message: "Downloading v1.1.0"}) +
		sseEvent(updater.ProgressEvent{Stage: "downloading", Message: "Downloaded 50%"}) +
		": a comment line\n" +
		"data: not json\n\n" +
		sseEvent(updater.ProgressEvent{Stage: "restarting", Message: "Restarting", Version: "v1.1.0"}) +
		sseEvent(updater.ProgressEvent{Stage: "done", Message: "Upgrade complete", Version: "v1.1.0"})
	c := updateGateway(t, http.StatusOK, nil, run)

	var stages []string
	version, err := c.RunUpdate(context.Background(), func(m string) { stages = append(stages, m) })
	if err != nil || version != "v1.1.0" {
		t.Fatalf("got %q, %v", version, err)
	}
	want := "Checking|Downloading v1.1.0|Restarting"
	if got := strings.Join(stages, "|"); got != want {
		t.Errorf("stages = %q, want %q (one line per stage, not per percent)", got, want)
	}
}

func TestRunUpdateReturnsTheGatewaysError(t *testing.T) {
	c := updateGateway(t, http.StatusOK, nil, sseEvent(updater.ProgressEvent{Stage: "error", Message: "checksum mismatch"}))
	if _, err := c.RunUpdate(context.Background(), func(string) {}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got %v", err)
	}
}

// TestRunUpdateCutShortIsNotSuccess: a connection that dropped halfway is not an upgrade.
func TestRunUpdateCutShortIsNotSuccess(t *testing.T) {
	c := updateGateway(t, http.StatusOK, nil, sseEvent(updater.ProgressEvent{Stage: "downloading", Message: "Downloading"}))
	if _, err := c.RunUpdate(context.Background(), func(string) {}); err == nil {
		t.Fatal("a stream that ends before done must be an error")
	}
}

func TestRunUpdateRefusedAndUnreachable(t *testing.T) {
	c := updateGateway(t, http.StatusUnauthorized, nil, "")
	if _, err := c.RunUpdate(context.Background(), func(string) {}); err == nil {
		t.Error("a refused request must be an error")
	}
	dead := NewClient("http://127.0.0.1:1", testToken)
	if _, err := dead.RunUpdate(context.Background(), func(string) {}); err == nil {
		t.Error("an unreachable gateway must be an error")
	}
	if _, err := (&Client{baseURL: "http://bad host"}).RunUpdate(context.Background(), func(string) {}); err == nil {
		t.Error("an unbuildable request must be an error")
	}
}

func TestRunUpdateReadErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = fmt.Fprint(w, "data: {")
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, testToken)
	if _, err := c.RunUpdate(context.Background(), func(string) {}); err == nil {
		t.Fatal("a body cut mid-line must be an error")
	}
}
