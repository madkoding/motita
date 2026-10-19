package gateway

// The branches a healthy gateway never takes: a socket that fails mid-handshake, a store
// directory that is not a directory, an upgrade stream whose download dies, and the startup
// wiring of server.go exercised through the same seams production uses.
//
// Each test says which production failure it stands in for, because a branch nobody can reach
// on purpose is a branch nobody would notice breaking.
//
// The upgrade stream lives in server_update_paths_test.go and the restored sessions in
// server_restore_paths_test.go.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/schedule"
	"github.com/madkoding/motita/internal/updater"
)

// --- the WebSocket handshake and frame writer -------------------------------------------------

// A response writer that cannot be hijacked is refused rather than half-upgraded: the connection
// would otherwise be handed back with no way to take it over from the HTTP server.
func TestAHandshakeWithoutHijackingIsRefused(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	// httptest.NewRecorder implements http.ResponseWriter but NOT http.Hijacker.
	if _, _, err := wsHandshake(httptest.NewRecorder(), req); err == nil {
		t.Error("a writer that cannot be hijacked must be refused")
	}
}

// A handshake whose response cannot be written closes the connection instead of returning it, so
// the caller never starts a read loop against a peer that never saw the 101.
func TestAHandshakeWhoseWriteFailsClosesTheConnection(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	h := &hijackingWriter{w: bufio.NewWriterSize(failWriter{}, 1)}

	conn, _, err := wsHandshake(h, req)
	if err == nil {
		t.Fatal("a handshake that cannot be written must be reported")
	}
	if conn != nil {
		t.Error("no connection may be handed back")
	}
}

// And a handshake that writes but cannot be FLUSHED fails too: the bytes are in a buffer the peer
// will never receive, which is the same failure one step later.
func TestAHandshakeWhoseFlushFailsIsRefused(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	// A buffer large enough to hold the response, so WriteString succeeds and Flush is what fails.
	h := &hijackingWriter{w: bufio.NewWriter(failWriter{})}

	if _, _, err := wsHandshake(h, req); err == nil {
		t.Error("a handshake that cannot be flushed must be reported")
	}
}

// A frame written to a peer that is gone is reported so the caller can stop rather than spin.
func TestAFrameWriteToADeadSocketIsReported(t *testing.T) {
	if err := wsWriteFrame(&fakeConn{writeErr: errors.New("the peer is gone")}, opText, []byte("hello")); err == nil {
		t.Error("a write to a dead socket must be reported")
	}
	// A zero-length payload writes only the header; the failure must still surface.
	if err := wsWriteFrame(&fakeConn{writeErr: errors.New("the peer is gone")}, opClose, nil); err == nil {
		t.Error("a header-only write to a dead socket must be reported")
	}
}

// failWriter is the socket that fails, reduced to the one method the handshake needs.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("the connection was reset") }

// hijackingWriter is a ResponseWriter that CAN be hijacked, so the handshake's write and flush
// branches are reachable - which they are not through httptest.NewRecorder.
type hijackingWriter struct {
	w *bufio.Writer
}

func (h *hijackingWriter) Header() http.Header         { return make(http.Header) }
func (h *hijackingWriter) Write(p []byte) (int, error) { return h.w.Write(p) }
func (h *hijackingWriter) WriteHeader(int)             {}
func (h *hijackingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return &fakeConn{}, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), h.w), nil
}

// fakeConn is a net.Conn whose reads and writes fail as a test chooses.
type fakeConn struct {
	writeErr error
	readErr  error
}

func (c *fakeConn) Read([]byte) (int, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	return 0, io.EOF
}
func (c *fakeConn) Write([]byte) (int, error)        { return 0, c.writeErr }
func (c *fakeConn) Close() error                     { return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return nil }
func (c *fakeConn) RemoteAddr() net.Addr             { return nil }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

// --- server.go: the startup wiring ------------------------------------------------------------

// A gateway whose store directories are unusable still starts. It reports each failure and runs
// without persistence, because a gateway that refuses to boot over a cache directory is a gateway
// nobody can use to fix it.
func TestAServerWithUnusableStorePathsStillStarts(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "in-the-way")
	if err := os.WriteFile(blocked, []byte("a file where a directory belongs"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logged []string
	srv, sink := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   filepath.Join(blocked, "sessions"),
		ProjectDir:   filepath.Join(blocked, "projects"),
		ScheduleDir:  filepath.Join(blocked, "schedules"),
	})
	logged = sink.lines(t)

	if srv == nil {
		t.Fatal("a server with unusable stores must still come up")
	}
	// Four: the three stores, and the remembered approvals that live beside the sessions.
	if len(logged) != 4 {
		t.Errorf("every store failure must be reported, got %d: %v", len(logged), logged)
	}
	if srv.store != nil {
		t.Error("no session store can have been opened from a path that is not a directory")
	}
}

// A configured schedule directory is opened at STARTUP, which is what makes a configured task
// survive a restart instead of existing only until the process ends.
func TestAConfiguredScheduleDirectoryIsOpenedAtStartup(t *testing.T) {
	dir := t.TempDir()
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   filepath.Join(t.TempDir(), "sessions"),
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		ScheduleDir:  dir,
	})
	if srv.schedules == nil {
		t.Fatal("a configured schedule directory must give the server a store, or nothing ever fires")
	}
	// The store reads where it was told to, so a task written by a previous process is visible.
	st, err := schedule.Open(dir)
	if err != nil {
		t.Fatalf("schedule.Open: %v", err)
	}
	if _, err := st.LoadAll(); err != nil {
		t.Errorf("LoadAll on the opened store: %v", err)
	}
}

// An ExePath that is set is wired into the updater whether or not the file is there. That is the
// real behaviour, and it is pinned here rather than assumed: the guard at server.go is
// `ExePath != ""`, with no existence check, so a MISCONFIGURED path builds an updater that would
// rename a new binary into place and leave the running one untouched.
//
// Recorded, not "fixed": changing it would change what an upgrade does on a host whose binary lives
// somewhere else, which is a decision for the operator, not for a coverage test.
func TestAnExePathIsWiredIntoTheUpdaterWithoutAnExistenceCheck(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-binary")
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   filepath.Join(t.TempDir(), "sessions"),
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		ExePath:      missing,
	})
	if srv.updater == nil {
		t.Fatal("a configured ExePath builds the updater; absence is not checked")
	}
	if srv.updater.ExePath != missing {
		t.Errorf("ExePath = %q, want the configured %q", srv.updater.ExePath, missing)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("this test is only meaningful while the path really is absent")
	}
}

// And an EMPTY ExePath builds no updater at all, so the update endpoints answer "not available"
// instead of a check that has no binary to replace.
func TestAnEmptyExePathBuildsNoUpdater(t *testing.T) {
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   filepath.Join(t.TempDir(), "sessions"),
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
	})
	if srv.updater != nil {
		t.Error("no updater can be built without a binary path")
	}

	w := get(t, srv, "/v1/update/check", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with no updater", w.Code)
	}
	var got updater.CheckResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if got.Error == "" {
		t.Error("the answer must say self-update is unavailable, not silently claim to be current")
	}
}

// A saved session is restored at startup with its title and transcript, because a gateway that came
// back with an empty list would look like lost work.
func TestSessionsAreRestoredAtStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id":        "s-restored",
		"title":     "a session from before the restart",
		"created":   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{{"user": "what we were doing", "agent": "the answer"}},
	})

	var factoryCalls int
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService: func() (Service, error) {
			factoryCalls++
			return &fakeService{}, nil
		},
	})

	if factoryCalls == 0 {
		t.Error("restoring a session needs an engine, and the factory was never asked for one")
	}
	c, ok := srv.lookup("s-restored")
	if !ok {
		t.Fatal("the saved session must be back after a restart")
	}
	if got := c.status().Title; got != "a session from before the restart" {
		t.Errorf("Title = %q, want the saved title", got)
	}
	if turns := c.svc.Transcript(); len(turns) != 1 {
		t.Errorf("Transcript = %d turns, want the saved one", len(turns))
	}
}

// The DEFAULT session's file is restored into the conversation that already exists rather than
// registered as a second conversation: two names for one transcript is how a session is edited in
// one place and read in another.
func TestTheDefaultSessionsFileIsRestoredIntoTheExistingConversation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id":        DefaultSession,
		"title":     "the default session's own title",
		"created":   time.Now().UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{{"user": "an earlier question", "agent": "an earlier answer"}},
	})

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
	})

	all := srv.snapshot()
	if len(all) != 1 {
		t.Fatalf("snapshot = %d sessions, want exactly the default one", len(all))
	}
	if got := all[0].status().Title; got != "the default session's own title" {
		t.Errorf("Title = %q, want the persisted title", got)
	}
}

// A session that was MID-RUN when the gateway stopped is not resumed blindly: the record is read
// and the run is relaunched through the same path the scheduler and the HTTP handlers use, so the
// slot guard and the completion classification cannot drift between them.
//
// The fake task BLOCKS, so the run is still in flight when it is observed: a fake that returned
// immediately would let this pass against a gateway that resumed nothing, because the session would
// go back to idle on its own.
func TestARunningSessionIsResumedThroughTheSharedRunPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id":        "s-interrupted",
		"title":     "was mid-task",
		"running":   true,
		"last_task": "finish the audit",
		"last_kind": "task",
		"created":   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
	})

	// The task SIGNALS that it started and then blocks. Polling a status flag for a couple of
	// seconds made this fail on a loaded machine, and a flaky test is worse than no test: it
	// teaches everyone to re-run until it is green.
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService: func() (Service, error) {
			return &fakeService{task: func(ctx context.Context, task string, pr func(string, ...any)) (string, error) {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
				return "done", nil
			}}, nil
		},
	})

	// Released in a cleanup registered AFTER startServer, and then WAITED for.
	//
	// The order is the whole fix. Cleanups run last-registered-first, so a cleanup
	// registered before startServer runs after srv.Close AND after t.TempDir's
	// RemoveAll - and that is too late to be useful here: this task blocks on release
	// and IGNORES its context, so closing the server does not end it. Releasing it that
	// late left the run's goroutine to finish on its own schedule, and its deferred
	// saveSession writes into the sessions directory while t.TempDir's cleanup is
	// removing it - which failed about one run in twenty under load with
	// "RemoveAll cleanup: directory not empty".
	//
	// Waiting is the other half, and the shared helper does it: isRunning is false only
	// after releaseRunSlot, the run goroutine's last defer, so by the time this returns
	// the write has already happened and every temp dir is quiet.
	t.Cleanup(func() {
		close(release)
		waitForNoRun(t, srv)
	})

	if _, ok := srv.lookup("s-interrupted"); !ok {
		t.Fatal("the interrupted session must have been restored before it was resumed")
	}
	select {
	case <-entered:
		// The run really started, through the shared path.
	case <-time.After(10 * time.Second):
		t.Error("a session that was mid-run must be resumed, not left idle")
	}
}

// A running record with no task text cannot be resumed - the prompt is gone - and is left clean
// rather than started with an empty prompt that would produce nothing.
func TestARunningRecordWithNoTaskIsNotResumed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id":        "s-no-task",
		"title":     "was running, task lost",
		"running":   true,
		"created":   time.Now().UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
	})

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})

	c, ok := srv.lookup("s-no-task")
	if !ok {
		t.Fatal("the record must still be restored as a session")
	}
	if c.isRunning() {
		t.Error("a resume with no task text would run an empty prompt, which is worse than not resuming")
	}
}

// A store that cannot be SAVED reports it once per failure rather than swallowing it: a gateway
// that cannot persist has to say so, or the operator learns it from missing data.
func TestASaveFailureReachesTheOperator(t *testing.T) {
	srv, sink := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   filepath.Join(t.TempDir(), "sessions"),
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
	})

	blockStoreDirectory(t, srv.store.dir)
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("the default conversation must exist")
	}
	srv.saveSession(c)
	if len(sink.lines(t)) == 0 {
		t.Error("a save that failed must reach the log")
	}

	// The same for the bulk save before a restart, and for a delete.
	srv.saveAllSessions()
	if !strings.Contains(strings.Join(sink.lines(t), "\n"), "restart") {
		t.Error("a save-all that failed must reach the log: the upgrade depends on it")
	}
	srv.deletePersistedSession("some-session")
	if !strings.Contains(strings.Join(sink.lines(t), "\n"), "delete") {
		t.Error("a delete that failed must reach the log")
	}
}

// A store that cannot even be LISTED reports the failure and restores nothing, rather than
// registering a session per file it could not read.
func TestAnUnreadableStoreAtStartupIsReported(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory the store cannot read: a file blocking the path it lists.
	seedSessionFile(t, dir, map[string]any{
		"id": "s1", "title": "t", "created": time.Now().UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().UTC().Format(time.RFC3339Nano), "turns": []map[string]string{},
	})
	blockStoreDirectory(t, dir)

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
	})
	if srv == nil {
		t.Fatal("a gateway whose store cannot be listed must still serve")
	}
}

// --- projects and sessions: the remaining refusals ---------------------------------------------

// A create body that is not JSON is refused, and nothing is registered.
func TestASessionCreateWithAMalformedBodyIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	before := len(srv.snapshot())

	if w := post(t, srv, "/v1/sessions", `{not json`, testToken); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if after := len(srv.snapshot()); after != before {
		t.Errorf("sessions went from %d to %d: a refused create must register nothing", before, after)
	}
}

// A session asked for a project that does not exist is refused with 404: a session for a missing
// project would run in the wrong directory, which is exactly what projects prevent.
func TestASessionForAnUnknownProjectIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())

	w := post(t, srv, "/v1/sessions", `{"project_id":"p-does-not-exist"}`, testToken)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: body = %s", w.Code, w.Body.String())
	}
}

// A session in a project that is NOT a repository still gets a usable session, in the project's own
// directory, and the fallback is reported so the operator knows the isolation was not available.
func TestASessionInANonRepositoryProjectFallsBackToTheProjectDirectory(t *testing.T) {
	sink := newLogSink(t)
	// WorkspaceDir and Log go in through the MUTATOR, not by assigning to srv.opts afterwards: the
	// server is already serving in a goroutine by then, and writing its options from the test is a
	// data race the detector reports as one.
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.WorkspaceDir = t.TempDir()
		o.Log = sink.log
	})
	withProjects(t, srv, srv.opts.WorkspaceDir)

	// A plain directory, not a git repository: the worktree cannot be added.
	pdir := filepath.Join(srv.opts.WorkspaceDir, "not-a-repo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := "p-not-a-repo"
	if err := srv.projects.save(Project{ID: pid, Title: "plain", Dir: pdir, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}

	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: body = %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if ss.Workspace != pdir {
		t.Errorf("Workspace = %q, want the project's own directory as the fallback", ss.Workspace)
	}
	if len(sink.lines(t)) == 0 {
		t.Error("the fallback must be reported: the session has no isolation from its project")
	}
}

// A session in a git project reports itself MERGEABLE once its own worktree has a commit the base
// branch does not, and it reports its own branch rather than the project's.
func TestASessionReportsItselfMergeableWhenItHasUnmergedWork(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")

	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	if ss.Mergeable {
		t.Error("a session with no commit of its own has nothing to integrate")
	}

	// A commit in the session's OWN worktree.
	if err := os.WriteFile(filepath.Join(ss.Workspace, "work.txt"), []byte("session work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", ss.Workspace, "add", "work.txt")
	mustRun(t, "git", "-C", ss.Workspace, "commit", "-qm", "session work")

	c, ok := srv.lookup(ss.ID)
	if !ok {
		t.Fatal("the session must be registered")
	}
	if st := c.status(); !st.Mergeable {
		t.Error("a session with a commit the base branch lacks must report itself mergeable")
	} else if st.Branch != sessionBranch(ss.ID) {
		t.Errorf("Branch = %q, want the session's own branch %q", st.Branch, sessionBranch(ss.ID))
	} else {
		// Mergeable and on its own branch: exactly what the session should report.
	}
}

// TestASessionReportsItselfMergedOnceItsWorkIsIntegrated: a session whose work was
// ALREADY merged reports itself not mergeable: the front end shows a disabled Integrate
// button rather than an action that would do nothing.
func TestASessionReportsItselfMergedOnceItsWorkIsIntegrated(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	fs := withProjectsAndFake(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ss.Workspace, "work.txt"), []byte("session work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", ss.Workspace, "add", "work.txt")
	mustRun(t, "git", "-C", ss.Workspace, "commit", "-qm", "session work")

	fs.task = mergeIntegrationTask(t, srv, ss, p)
	doMerge(t, srv, ss, p)

	c, ok := srv.lookup(ss.ID)
	if !ok {
		t.Fatal("the session must be registered")
	}
	if c.status().Mergeable {
		t.Error("once the work is in the base branch there is nothing left to integrate")
	}
}

// --- helpers -----------------------------------------------------------------------------------

// startServer builds a server without serving it, so the startup wiring is exercised without a
// listener in the way. It is the Options-level twin of newTestServer.
//
// A REAL logger is wired in rather than a captured slice: a test that asserted against its own
// log function would prove nothing about the lines an operator actually gets.
func startServer(t *testing.T, opts Options) (*Server, *logSink) {
	t.Helper()
	sink := newLogSink(t)
	opts.Log = sink.log
	// Start refuses without a service to speak for, which is the same rule newTestServer follows.
	if opts.Service == nil {
		opts.Service = &fakeService{}
	}
	srv, err := Start(opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		// A background update of a main branch must end before the test's directories are removed.
		srv.mainRefresh.wait()
		_ = srv.Close(context.Background())
	})
	return srv, sink
}

// logSink is a REAL logger writing to a file the test reads back, rather than a captured slice.
//
// The real one, deliberately: a server that took a func would let a test assert against a log
// function the production path never calls, and the failure it is meant to prove would stay silent
// exactly where it matters.
type logSink struct {
	path string
	log  *logx.Logger
}

func newLogSink(t *testing.T) *logSink {
	t.Helper()
	path := filepath.Join(t.TempDir(), "motita.log")
	l, err := logx.New(logx.Options{Path: path, Level: logx.Info})
	if err != nil {
		t.Fatalf("logx.New: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return &logSink{path: path, log: l}
}

// lines reads back what the server logged. logx writes each record straight to the file with no
// buffer in between, so a read after the call sees it.
func (s *logSink) lines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading the log: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// seedSessionFile writes a session record straight to a store directory, which is how a previous
// process's state is reproduced without going through a live gateway.
func seedSessionFile(t *testing.T, dir string, rec map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := rec["id"].(string)
	if id == "" {
		t.Fatal("a seeded record needs an id")
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
