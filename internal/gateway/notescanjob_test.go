package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
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
	if p.Result == nil || p.Result.Mapped != 1 || p.Result.Documented == 0 {
		t.Errorf("finished without the manual: %+v", p)
	}
	if p.Done != 1 || p.Total != 1 || p.Error != "" {
		t.Errorf("finished: %+v", p)
	}
}

func TestRescanRepeatsTheLastScanInTheBackground(t *testing.T) {
	srv, _ := archServer(t, nil)
	first := httptest.NewRequest(http.MethodPost, "/v1/notes/scan?again=1", nil)
	if q := srv.scanQuery(first); q.Get(scanBackground) != "1" {
		t.Errorf("a rescan before any scan still runs in the background: %v", q)
	}
	noteCall(t, srv, http.MethodPost, "/v1/notes/scan?modules=7", "")
	q := srv.scanQuery(httptest.NewRequest(http.MethodPost, "/v1/notes/scan?again=1&modules=1", nil))
	if q.Get("modules") != "7" {
		t.Errorf("a rescan keeps the last scan's options: %v", q)
	}
	if w := noteCall(t, srv, http.MethodPost, "/v1/notes/scan?again=1", ""); w.Code != http.StatusAccepted {
		t.Fatalf("rescan: %d %s", w.Code, w.Body)
	}
	if p := waitScan(t, srv); p.Result == nil {
		t.Errorf("the rescan finished without a result: %+v", p)
	}
}

func TestScanInTheBackgroundRunsOneAtATimeAndReportsFailures(t *testing.T) {
	var tr scanTracker
	if !tr.start() || tr.start() {
		t.Error("a second scan waits for the first")
	}
	tr.update(func(p *scanProgress) { p.Total = 3 })
	tr.finish(noteScan{}, os.ErrPermission)
	got := tr.get()
	if got.Running || got.Error == "" {
		t.Errorf("failed: %+v", got)
	}
	if got.Done != 3 || got.Result != nil {
		t.Errorf("failed with a result: %+v", got)
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

// upstreamCommit adds a commit to the project's origin, which its main branch does not have yet.
func upstreamCommit(t *testing.T, upstream, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(upstream, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, upstream, "add", "-A")
	mustGit(t, upstream, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", file)
}

func TestNewCodeOnTheMainBranchScansTheNotesAgain(t *testing.T) {
	ws := t.TempDir()
	notesDir := filepath.Join(ws, "notes")
	svc := &fakeService{}
	svc.cfg.Sandbox.DiskAccess = config.DiskFull
	srv := newTestServer(t, svc, func(o *Options) {
		o.WorkspaceDir = ws
		o.NotesDir = notesDir
		o.ProjectDir = filepath.Join(ws, "projects")
		o.SessionDir = filepath.Join(ws, "sessions")
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	local, upstream := usersRepo(t)
	if err := os.Remove(filepath.Join(local, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	// The manual reads the checkout, so it is on the main branch that new code arrives.
	mustGit(t, local, "checkout", "-q", "main")
	if code, out := createProject(t, srv, `{"title":"Bridges","path":"`+local+`"}`); code != http.StatusCreated {
		t.Fatalf("creating: %d %v", code, out)
	}
	p := srv.projectOf(onlyProjectID(t, srv))
	ctx := context.Background()

	// A vault never scanned is the person's: new code writes nothing in it.
	if w := srv.refreshMain(ctx, p, true); w != "" {
		t.Fatal(w)
	}
	srv.scanJob.wait()
	if _, err := os.Stat(notesDir); !os.IsNotExist(err) {
		t.Fatalf("the notes were written without a scan: %v", err)
	}

	// Once scanned, new code on main scans again with the options the person chose.
	w := noteCall(t, srv, http.MethodPost, "/v1/notes/scan?modules=5", "")
	if res := decodeScan(t, w.Body.Bytes(), w.Code); res.Mapped != 1 {
		t.Fatalf("scan: %+v", res)
	}
	if got := srv.scanJob.options().Get("modules"); got != "5" {
		t.Errorf("options kept: %q", got)
	}
	upstreamCommit(t, upstream, "newer.txt")
	if w := srv.refreshMain(ctx, p, true); w != "" {
		t.Fatal(w)
	}
	srv.scanJob.wait()
	if got := srv.scanJob.get(); got.Result == nil || got.Result.Mapped != 1 {
		t.Errorf("no scan after new code: %+v", got)
	}
	if !strings.Contains(readNote(t, notesDir, "Projects/Bridges - Manual/Start here.md"), "newer.txt") {
		t.Error("the manual does not follow the new code")
	}

	// While a scan runs, new code waits for the next one.
	srv.scanJob.start()
	upstreamCommit(t, upstream, "newest.txt")
	if w := srv.refreshMain(ctx, p, true); w != "" {
		t.Fatal(w)
	}
	srv.scanJob.wait()
	if !srv.scanJob.get().Running {
		t.Error("a second scan started over the first")
	}
	srv.scanJob.finish(noteScan{}, nil)
}
