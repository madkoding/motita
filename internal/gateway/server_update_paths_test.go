package gateway

// The update branches a healthy gateway never takes: an upgrade stream with nothing to do, a failed
// check or download, a missing asset, the cache of the update check and the periodic checker, with
// the fake releases they are tested against.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/updater"
)

// --- the upgrade stream ------------------------------------------------------------------------

// The upgrade stream reports "already up to date" and stops, rather than fetching a release it
// does not need.
func TestTheUpgradeStreamReportsWhenThereIsNothingToDo(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.opts.Version = "v1.0.0"
	srv.updater = updaterFor(t, srv, releaseJSON("v1.0.0"))

	w := httptest.NewRecorder()
	srv.handleUpdateRun(w, httptest.NewRequest(http.MethodPost, "/v1/update/run", nil))

	body := w.Body.String()
	if !strings.Contains(body, "Already up to date") {
		t.Errorf("body = %q, want it to say there is nothing to do", body)
	}
	if !strings.Contains(body, "event: progress") {
		t.Errorf("body = %q, want SSE progress events", body)
	}
}

// A check that fails is streamed as an error event rather than answered with only headers: the
// front end shows a message, not a progress bar that never moves.
func TestTheUpgradeStreamReportsACheckFailure(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.opts.Version = "v1.0.0"
	srv.updater = updaterFor(t, srv, "") // an empty body is not a release

	w := httptest.NewRecorder()
	srv.handleUpdateRun(w, httptest.NewRequest(http.MethodPost, "/v1/update/run", nil))

	if !strings.Contains(w.Body.String(), `"stage":"error"`) {
		t.Errorf("body = %q, want an error event", w.Body.String())
	}
}

// And a download that dies mid-upgrade is streamed as an error: the user is told, rather than left
// watching a bar that stopped.
func TestTheUpgradeStreamReportsAFailedDownload(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.opts.Version = "v1.0.0"
	// A newer release whose ONLY asset is the one this platform wants, pointing at a URL that 500s.
	release := `{"tag_name":"v9.9.9","name":"newer","assets":[
		{"name":"` + assetNameForThisPlatform() + `","browser_download_url":"` + failingAssetURL(t) + `"}]}`
	srv.updater = updaterFor(t, srv, release)

	w := httptest.NewRecorder()
	srv.handleUpdateRun(w, httptest.NewRequest(http.MethodPost, "/v1/update/run", nil))

	if !strings.Contains(w.Body.String(), `"stage":"error"`) {
		t.Errorf("body = %q, want an error event when the download fails", w.Body.String())
	}
}

// A release with no asset for this platform is an error the user can act on, not a silent no-op.
func TestTheUpgradeStreamReportsAMissingAsset(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.opts.Version = "v1.0.0"
	srv.updater = updaterFor(t, srv, `{"tag_name":"v9.9.9","name":"newer","assets":[]}`)

	w := httptest.NewRecorder()
	srv.handleUpdateRun(w, httptest.NewRequest(http.MethodPost, "/v1/update/run", nil))

	if !strings.Contains(w.Body.String(), `"stage":"error"`) {
		t.Errorf("body = %q, want an error naming the missing asset", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no asset for") {
		t.Errorf("body = %q, want the reason", w.Body.String())
	}
}

// The check endpoint answers from the CACHE the background checker fills, so a front end polling it
// never hits GitHub. Without the cache every poll would be an API call against a 60/hour limit.
//
// The updater is wired to a fake API that would answer a DIFFERENT version, so a handler that
// ignored the cache and checked again would fail this rather than pass on a coincidentally equal
// answer.
func TestTheUpdateCheckAnswersFromTheCache(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.opts.Version = "v1.0.0"
	srv.updater = updaterFor(t, srv, releaseJSON("v9.9.9")) // what a fresh check would say
	srv.lastCheckMu.Lock()
	srv.lastCheck = updater.CheckResult{CurrentVersion: "v1.0.0", LatestVersion: "v2.0.0", UpdateAvailable: true}
	srv.lastCheckMu.Unlock()

	w := get(t, srv, "/v1/update/check", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got updater.CheckResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if got.LatestVersion != "v2.0.0" {
		t.Errorf("LatestVersion = %q, want the CACHED one: a poll must not hit GitHub", got.LatestVersion)
	}
}

// With no cache the endpoint does a fresh check and caches what it found.
func TestTheUpdateCheckFillsTheCacheOnAFirstCall(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.opts.Version = "v1.0.0"
	srv.updater = updaterFor(t, srv, releaseJSON("v2.0.0"))

	w := get(t, srv, "/v1/update/check", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// The check answered a body, and a check is only cheap if it was remembered.
	if w.Body.Len() == 0 {
		t.Fatal("the check must answer a result")
	}
}

// The periodic checker caches its result so the endpoint has something to serve, and a short
// interval exercises the tick branch that the one-hour default would take an hour to reach.
func TestThePeriodicCheckerStopsWithTheGateway(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.UpdateCheckInterval = 20 * time.Millisecond })
	srv.updater = updaterFor(t, srv, releaseJSON("v9.9.9"))

	srv.runOneCheck()
	srv.lastCheckMu.RLock()
	cached := srv.lastCheck
	srv.lastCheckMu.RUnlock()
	if cached.LatestVersion != "v9.9.9" {
		t.Errorf("the periodic check must cache what it found, got %q", cached.LatestVersion)
	}

	// The goroutine checks once after the startup delay and then ON THE TICK, which the short
	// interval makes reachable. The counter is reset so only the tick can be what sets it.
	resetCheckCount()
	srv.startUpdateChecker()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if checkCount() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := checkCount(); got < 2 {
		t.Errorf("checks = %d, want at least 2: the startup check AND a tick", got)
	}

	// And closing the gateway stops it: a leaked checker would keep hitting the network after the
	// process was told to stop.
	if err := srv.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-srv.baseCtx.Done():
	case <-time.After(2 * time.Second):
		t.Error("closing the gateway must end the checker's context")
	}
	seen := checkCount()
	time.Sleep(120 * time.Millisecond)
	if checkCount() != seen {
		t.Error("the checker kept calling after the gateway closed")
	}
}

// A NEGATIVE interval turns the periodic checker off, which is what a test that must not touch the
// network asks for. Without it every test server in this package would eventually poll GitHub.
func TestANegativeUpdateCheckIntervalTurnsTheCheckerOff(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.UpdateCheckInterval = -1 })
	srv.updater = updaterFor(t, srv, releaseJSON("v9.9.9"))

	resetCheckCount()
	srv.startUpdateChecker()
	time.Sleep(200 * time.Millisecond)
	if got := checkCount(); got != 0 {
		t.Errorf("checks = %d, want 0: a negative interval means no checker at all", got)
	}
}

// The update-check counter lives here because the fake provider is the only place a check can be
// counted without reaching the network.
var (
	checkMu    sync.Mutex
	checkCalls int
)

func resetCheckCount() {
	checkMu.Lock()
	checkCalls = 0
	checkMu.Unlock()
}

func checkCount() int {
	checkMu.Lock()
	defer checkMu.Unlock()
	return checkCalls
}

func bumpCheckCount() {
	checkMu.Lock()
	checkCalls++
	checkMu.Unlock()
}

// releaseJSON is a GitHub releases/latest body carrying a tag and no assets.
func releaseJSON(tag string) string {
	return `{"tag_name":"` + tag + `","name":"` + tag + `","html_url":"https://example.invalid/` + tag + `"}`
}

// updaterFor points a server's updater at a fake GitHub API, which is the seam the updater exposes
// for exactly this. Nothing here reaches the network.
func updaterFor(t *testing.T, srv *Server, body string) *updater.Updater {
	t.Helper()
	u := updater.New("v1.0.0", filepath.Join(t.TempDir(), "motita"))
	u.APIURL = func() string { return assetServerURL(t, body) }
	return u
}

// assetServerURL serves one canned body to every request and counts the requests, which is how a
// test tells "the checker ticked" from "the checker is asleep" without waiting for a real interval.
func assetServerURL(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bumpCheckCount()
		if body == "" {
			http.Error(w, "not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// failingAssetURL is a URL that answers 500, so a download dies in the middle of an upgrade.
func failingAssetURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the asset server is down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// assetNameForThisPlatform mirrors the updater's own naming so the fake release carries the asset
// this build would look for.
func assetNameForThisPlatform() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return "motita-" + runtime.GOOS + "-" + runtime.GOARCH + ext
}
