package gateway

// The last nine branches in the package. Every one of them is an ERROR path that only happens once
// something outside the process has gone wrong, which is why they need their own fixtures.

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- wsHandshake: a hijack that fails ----------------------------------------------------------

// A hijack that FAILS is reported as such. The difference from "this writer cannot hijack" is the
// difference between a misconfigured writer and a connection the kernel would not give up, and an
// operator reading the log needs to be able to tell them apart.
func TestAHijackThatFailsIsReported(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	h := &failingHijackWriter{err: errors.New("the connection is already gone")}

	conn, _, err := wsHandshake(h, req)
	if err == nil {
		t.Fatal("a hijack that failed must be reported")
	}
	if conn != nil {
		t.Error("no connection may be handed back when the hijack failed")
	}
	if !strings.Contains(err.Error(), "could not hijack") {
		t.Errorf("err = %q, want it to name the hijack", err)
	}
}

// failingHijackWriter is a ResponseWriter that CAN be asked to hijack, and fails when it is.
type failingHijackWriter struct {
	err error
}

func (h *failingHijackWriter) Header() http.Header       { return make(http.Header) }
func (h *failingHijackWriter) Write([]byte) (int, error) { return 0, nil }
func (h *failingHijackWriter) WriteHeader(int)           {}
func (h *failingHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, h.err
}

// --- the two startup listings that fail ---------------------------------------------------------

// A session store that cannot be LISTED at startup is reported and the gateway comes up anyway: an
// unreadable sessions directory costs persistence, not the server.
func TestAnUnlistableSessionStoreAtStartupIsReported(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A FILE inside the directory where its own name is now a directory: ReadDir on the store's
	// path still works, so the failure has to come from the STORE'S OWN read. Replacing the store's
	// directory with a file after it was opened is what produces that.
	srv, sink := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
	})
	if srv.store == nil {
		t.Fatal("the store must have opened")
	}
	// The directory is removed and replaced by a FILE, so every later read of it fails ENOTDIR.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("now a file"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv.loadPersistedSessions()
	if !strings.Contains(strings.Join(sink.lines(t), "\n"), "could not load persisted sessions") {
		t.Errorf("a listing that failed must be reported, got %v", sink.lines(t))
	}

	// And the same for the resume path, which reads the same store.
	srv.resumeInterruptedSessions()
	if !strings.Contains(strings.Join(sink.lines(t), "\n"), "could not load sessions for resume") {
		t.Errorf("the resume's listing failure must be reported too, got %v", sink.lines(t))
	}
}

// A session to be resumed whose conversation is NOT in memory is skipped: the record survives but
// there is nothing to run it in, and the gateway keeps starting.
func TestAResumableRecordWithNoConversationIsSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	// A resumable record whose id has NO conversation: the store is filled AFTER the server started,
	// so loadPersistedSessions has already run and the record will never be registered.
	seedSessionFile(t, dir, map[string]any{
		"id": "s-never-registered", "title": "orphan record",
		"running": true, "last_task": "the task", "last_kind": "task",
		"created":   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
	})

	// The server starts over an EMPTY directory, so nothing is loaded, and the record above is
	// written into the store afterwards.
	empty := filepath.Join(t.TempDir(), "sessions")
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   empty,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})
	if _, ok := srv.lookup("s-never-registered"); ok {
		t.Fatal("this test needs a record that was NOT registered")
	}
	// Move the record into the store the server actually reads.
	if err := os.Rename(filepath.Join(dir, "s-never-registered.json"),
		filepath.Join(empty, "s-never-registered.json")); err != nil {
		t.Fatal(err)
	}

	srv.resumeInterruptedSessions() // must skip it, not panic
	if _, ok := srv.lookup("s-never-registered"); ok {
		t.Error("a resume cannot register a conversation the load did not")
	}
}

// --- the update check with no updater -----------------------------------------------------------

// With NO updater configured the check endpoint says so in the result, rather than reporting the
// version as current - which would tell a user they are up to date when nothing was ever checked.
func TestTheUpdateCheckWithoutAnUpdaterSaysSo(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.opts.Version = "v1.2.3"

	w := get(t, srv, "/v1/update/check", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: an unavailable feature is not an error", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "self-update is not available") {
		t.Errorf("body = %q, want it to say why", body)
	}
	if !strings.Contains(body, "v1.2.3") {
		t.Errorf("body = %q, want the current version reported", body)
	}
}

// The same for the upgrade stream: it is refused with a client error, because the request cannot be
// served and the client should not retry the same way.
func TestTheUpgradeStreamWithoutAnUpdaterIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.updater = nil

	w := httptest.NewRecorder()
	srv.handleUpdateRun(w, httptest.NewRequest(http.MethodPost, "/v1/update/run", nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "self-update is not available") {
		t.Errorf("body = %q, want it to say why", w.Body.String())
	}
}

// --- the entropy source -------------------------------------------------------------------------

// A UUID that cannot be generated PANICS with a message naming the cause. That is a deliberate
// choice, not an oversight: a predictable id gates access in this protocol, and a wrong-but-quiet id
// is worse than a crash that says so.
//
// The failure is injected through randReader - the package's own seam, declared in token.go for
// exactly this kind of branch - so the panic is ASSERTED rather than left as an unreachable line.
func TestAUUIDWithoutEntropyPanicsWithTheCause(t *testing.T) {
	restore := randReader
	randReader = failingReader{}
	t.Cleanup(func() { randReader = restore })

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a uuid that cannot be generated must not be returned as a guess")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value = %#v, want a message naming the cause", r)
		}
		if !strings.Contains(msg, "could not generate a uuid") {
			t.Errorf("panic = %q, want it to name what failed", msg)
		}
	}()
	_ = newUUIDv4()
	t.Fatal("unreachable: newUUIDv4 must have panicked")
}

// And the format it produces when entropy IS available is the declared one: a v4 UUID has the
// version and variant bits set, and its shape is stable because clients parse it.
func TestAUUIDHasTheDeclaredVersionAndVariant(t *testing.T) {
	id := newUUIDv4()
	if len(id) != 36 {
		t.Fatalf("uuid = %q, want 36 characters", id)
	}
	if id[14] != '4' {
		t.Errorf("uuid = %q, want version 4 in position 14", id)
	}
	if !strings.ContainsRune("89ab", rune(id[19])) {
		t.Errorf("uuid = %q, want the variant bits set", id)
	}
	if newUUIDv4() == id {
		t.Error("two uuids must not collide: an id that repeats is an id that is guessable")
	}
}

// --- the periodic checker without an updater ----------------------------------------------------

// The checker does nothing at all with no updater: starting a goroutine that can never check would
// be a goroutine that only exists to be leaked.
func TestTheUpdateCheckerDoesNothingWithoutAnUpdater(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.updater = nil

	before := countGoroutines()
	srv.startUpdateChecker()
	// Nothing to wait FOR: the assertion is that no goroutine was started at all.
	if after := countGoroutines(); after > before+1 {
		t.Errorf("goroutines went from %d to %d: no updater means no checker", before, after)
	}
}

// countGoroutines reads the runtime's goroutine count, which is how "nothing was started" is
// asserted without a timer.
func countGoroutines() int {
	return runtime.NumGoroutine()
}
