package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitScan follows the background scan until it ends and answers its last progress.
func waitScan(t *testing.T, srv *Server) scanProgress {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		w := noteCall(t, srv, http.MethodGet, "/v1/notes/scan", "")
		var p scanProgress
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &p) != nil {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if !p.Running {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatal("the scan never ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestScanRunsInTheBackgroundAndSaysHowFarItGot(t *testing.T) {
	srv, _ := archServer(t, nil)
	w := noteCall(t, srv, http.MethodPost, "/v1/notes/scan?background=1", "")
	var started scanProgress
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &started) != nil {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	p := waitScan(t, srv)
	if p.Result == nil || p.Result.Mapped != 1 || p.Result.Documented == 0 || p.Done != 1 || p.Total != 1 ||
		p.Error != "" {
		t.Errorf("finished: %+v", p)
	}
}

func TestScanInTheBackgroundRunsOneAtATimeAndReportsFailures(t *testing.T) {
	var tr scanTracker
	if !tr.start() || tr.start() {
		t.Error("a second scan waits for the first")
	}
	tr.update(func(p *scanProgress) { p.Total = 3 })
	tr.finish(noteScan{}, os.ErrPermission)
	if got := tr.get(); got.Running || got.Error == "" || got.Done != 3 || got.Result != nil {
		t.Errorf("failed: %+v", got)
	}
	var none *scanTracker
	none.update(func(*scanProgress) { t.Error("a nil tracker records nothing") })

	srv, _, ws := scanServer(t, nil)
	if err := os.RemoveAll(filepath.Join(ws, "projects")); err != nil {
		t.Fatal(err)
	}
	noteCall(t, srv, http.MethodPost, "/v1/notes/scan?background=1", "")
	if p := waitScan(t, srv); p.Error == "" {
		t.Errorf("unreadable projects: %+v", p)
	}
}
