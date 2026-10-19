package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

func scanServer(t *testing.T, turns []agent.DialogueTurn) (*Server, string, string) {
	t.Helper()
	ws := t.TempDir()
	notesDir := filepath.Join(ws, "notes")
	srv, _ := startServer(t, Options{
		Token: testToken, WorkspaceDir: ws, NotesDir: notesDir,
		ProjectDir: filepath.Join(ws, "projects"), SessionDir: filepath.Join(ws, "sessions"),
		Service: &fakeService{transcript: turns},
	})
	return srv, notesDir, ws
}

func runScan(t *testing.T, srv *Server) noteScan {
	t.Helper()
	w := noteCall(t, srv, http.MethodPost, "/v1/notes/scan", "")
	var out noteScan
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("scan: %d %s", w.Code, w.Body)
	}
	return out
}

func readNote(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestScanWritesANotePerProjectAndSessionAndIsRepeatable(t *testing.T) {
	turns := []agent.DialogueTurn{
		{User: "fix the build [[x]]", Agent: "done, key sk-abcdefghijklmnopqrstuvwxyz0123456789"},
		{User: "and the docs", Agent: "ok"},
	}
	srv, dir, ws := scanServer(t, turns)
	proj := filepath.Join(ws, "app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "README.md"), []byte("# App\nIt does things."), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Project{
		{ID: "p1aaaaaaaaaa", Title: "App", Dir: proj, GitURL: "https://example.com/a.git", MainBranch: "main",
			Description: "The app", Created: time.Now()},
		{ID: "p2bbbbbbbbbb", Title: "App", Dir: filepath.Join(ws, "none"), Created: time.Now().Add(time.Second)},
	} {
		if err := srv.projects.save(p); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := srv.lookup(DefaultSession)
	c.stateMu.Lock()
	c.title, c.projectID, c.lastUsed = "Fix the build", "p1aaaaaaaaaa", time.Now()
	c.stateMu.Unlock()

	got := runScan(t, srv)
	if !got.Ran || got.Projects != 2 || got.Sessions != 1 || got.Created != 3 || got.Skipped != 0 {
		t.Fatalf("first scan = %+v", got)
	}
	session := readNote(t, dir, sessionNoteName("Fix the build", c.id))
	for _, want := range []string{"[[Projects/App]]", "Turns: 2", "> fix the build [ [x]]", "**Motita**"} {
		if !strings.Contains(session, want) {
			t.Errorf("session note lacks %q:\n%s", want, session)
		}
	}
	if strings.Contains(session, "sk-abcdefghij") {
		t.Errorf("a secret reached a note:\n%s", session)
	}
	project := readNote(t, dir, "Projects/App.md")
	for _, want := range []string{
		"https://example.com/a.git", "Main branch: main", "> The app", "### Sessions", "### README", "It does things.",
	} {
		if !strings.Contains(project, want) {
			t.Errorf("project note lacks %q:\n%s", want, project)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Projects", "App (p2bbbbbb).md")); err != nil {
		t.Errorf("a second project with the same title is told apart: %v", err)
	}

	// The person writes in a scanned note and the project changes; their text stays.
	mine := strings.Replace(project, "# App\n", "# App\nmy own thoughts\n", 1)
	if err := os.WriteFile(filepath.Join(dir, "Projects", "App.md"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.projects.update("p1aaaaaaaaaa", func(p *Project) { p.Description = "The new app" }); err != nil {
		t.Fatal(err)
	}
	if again := runScan(t, srv); again.Created != 0 || again.Unchanged != 2 || again.Updated != 1 {
		t.Errorf("second scan = %+v", again)
	}
	if body := readNote(t, dir, "Projects/App.md"); !strings.Contains(body, "my own thoughts") {
		t.Errorf("the person's text was lost:\n%s", body)
	}
}

func TestScanSkipsWhatHasNothingToSay(t *testing.T) {
	srv, _, _ := scanServer(t, nil)
	if got := runScan(t, srv); got.Sessions != 0 || got.Skipped != 1 || got.Created != 0 {
		t.Errorf("an empty session is skipped: %+v", got)
	}
	// A title that leaves no usable file name is skipped.
	srv2, dir, _ := scanServer(t, []agent.DialogueTurn{{User: "hi"}})
	c, _ := srv2.lookup(DefaultSession)
	c.stateMu.Lock()
	c.title = "..."
	c.stateMu.Unlock()
	if got := runScan(t, srv2); got.Sessions != 0 || got.Skipped != 1 {
		t.Errorf("no usable name: %+v", got)
	}
	// A note the vault cannot write (a folder is in its place) is counted, not fatal.
	c.stateMu.Lock()
	c.title = "Hi"
	c.stateMu.Unlock()
	if err := os.MkdirAll(filepath.Join(dir, sessionNoteName("Hi", c.id)), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := runScan(t, srv2); got.Sessions != 1 || got.Skipped != 1 || got.Created != 0 {
		t.Errorf("a refused note: %+v", got)
	}
	// Only the most recent sessions are written.
	old := scanMaxSessions
	defer func() { scanMaxSessions = old }()
	scanMaxSessions = 1
	if _, err := srv2.newSession(&fakeService{transcript: []agent.DialogueTurn{{User: "other"}}}); err != nil {
		t.Fatal(err)
	}
	if got := runScan(t, srv2); got.Sessions != 1 {
		t.Errorf("the cap: %+v", got)
	}
	scanMaxSessions = old
	// A conversation with no service has nothing to read.
	c.svc = nil
	if got := runScan(t, srv2); got.Skipped != 1 {
		t.Errorf("no service: %+v", got)
	}
}

func TestScanFailsWithoutAVaultAndWhenProjectsCannotBeRead(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if w := noteCall(t, srv, http.MethodPost, "/v1/notes/scan", ""); w.Code != http.StatusNotImplemented {
		t.Errorf("no vault: %d", w.Code)
	}
	srv2, _, ws := scanServer(t, nil)
	if err := os.RemoveAll(filepath.Join(ws, "projects")); err != nil {
		t.Fatal(err)
	}
	if w := noteCall(t, srv2, http.MethodPost, "/v1/notes/scan", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("unreadable projects: %d", w.Code)
	}
}

func TestSessionNoteNameAndQuote(t *testing.T) {
	if got := sessionNoteName("  ", "s0123456789abcdef"); got != "Sessions/Session (01234567).md" {
		t.Errorf("untitled = %q", got)
	}
	if got := sessionNoteName("T", "sab"); got != "Sessions/T (ab).md" {
		t.Errorf("short id = %q", got)
	}
	if got := quote("a\nb <!-- motita:scan:end --> [[c]]", 100); got != "> a\n> b  [ [c]]" {
		t.Errorf("quote = %q", got)
	}
}

func TestScanOnFirstUseRunsOnceAndOnlyInAnEmptyVault(t *testing.T) {
	srv, dir, _ := scanServer(t, []agent.DialogueTurn{{User: "hi", Agent: "hello"}})
	first := noteCall(t, srv, http.MethodPost, "/v1/notes/scan?first=1", "")
	if !strings.Contains(first.Body.String(), `"ran":true`) {
		t.Fatalf("an empty, never scanned vault is filled: %s", first.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, scanMarker)); err != nil {
		t.Errorf("the scan leaves its marker: %v", err)
	}
	// Emptied by the person afterwards: not filled again by itself, but a scan they ask for still runs.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	again := noteCall(t, srv, http.MethodPost, "/v1/notes/scan?first=1", "")
	if !strings.Contains(again.Body.String(), `"ran":false`) {
		t.Errorf("a vault scanned before is left alone: %s", again.Body)
	}
	if !runScan(t, srv).Ran {
		t.Error("a scan asked for always runs")
	}

	// A vault that already has notes is not touched by the first-use scan.
	srv2, dir2, _ := scanServer(t, []agent.DialogueTurn{{User: "hi"}})
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "mine.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := noteCall(t, srv2, http.MethodPost, "/v1/notes/scan?first=1", "")
	if !strings.Contains(w.Body.String(), `"ran":false`) {
		t.Errorf("a vault with notes: %s", w.Body)
	}
}
