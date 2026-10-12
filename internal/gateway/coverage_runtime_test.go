package gateway

// Coverage tests for the gateway package: projectOf, session branches, the update endpoints,
// session persistence, the WebSocket helpers and providerKeyPresent.

import (
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

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
	"github.com/madkoding/motita/internal/updater"
)

// --- projectOf ---

func TestProjectOfReturnsNilWhenNoStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if p := srv.projectOf("anything"); p != nil {
		t.Error("expected nil when no project store")
	}
}

func TestProjectOfReturnsNilWhenEmptyID(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	if p := srv.projectOf(""); p != nil {
		t.Error("expected nil for empty id")
	}
}

func TestProjectOfReturnsNilWhenMissing(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	if p := srv.projectOf("nonexistent"); p != nil {
		t.Error("expected nil for missing project")
	}
}

func TestProjectOfReturnsProject(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	body := strings.NewReader(`{"title":"Find Me","dir":"found"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	id := resp["id"].(string)
	p := srv.projectOf(id)
	if p == nil || p.Title != "Find Me" {
		t.Errorf("projectOf = %+v, want project titled 'Find Me'", p)
	}
}

// --- sessionBranch ---

func TestSessionBranch(t *testing.T) {
	got := sessionBranch("abc123")
	if got != "motita/abc123" {
		t.Errorf("sessionBranch = %q, want %q", got, "motita/abc123")
	}
}

// --- Update endpoints ---

func TestHandleUpdateCheckWithoutUpdater(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, "/v1/update/check", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body updater.CheckResult
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error == "" {
		t.Error("expected an error message when no updater is configured")
	}
}

func TestHandleUpdateRunWithoutUpdater(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/update/run", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUpdateCheckWithCache(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.updater = updater.New("test", "/tmp/fake-exe")
	srv.lastCheckMu.Lock()
	srv.lastCheck = updater.CheckResult{LatestVersion: "v9.9.9", CurrentVersion: "test"}
	srv.lastCheckMu.Unlock()
	w := get(t, srv, "/v1/update/check", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body updater.CheckResult
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.LatestVersion != "v9.9.9" {
		t.Errorf("LatestVersion = %q, want v9.9.9", body.LatestVersion)
	}
}

// --- Persistence ---

func TestLoadPersistedSessionsWithoutStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.loadPersistedSessions()
}

func TestSaveAllSessionsWithoutStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.saveAllSessions()
}

func TestSaveAllSessionsWithStore(t *testing.T) {
	sdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.SessionDir = sdir
	})
	srv.saveAllSessions()
	if _, err := os.Stat(filepath.Join(sdir, "default.json")); err != nil {
		t.Errorf("expected default.json to exist: %v", err)
	}
}

func TestDeletePersistedSessionWithoutStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.deletePersistedSession("whatever")
}

func TestDeletePersistedSessionWithStore(t *testing.T) {
	sdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.SessionDir = sdir
	})
	srv.saveAllSessions()
	srv.deletePersistedSession("default")
	if _, err := os.Stat(filepath.Join(sdir, "default.json")); !os.IsNotExist(err) {
		t.Errorf("expected default.json to be deleted: %v", err)
	}
}

func TestDeletePersistedSessionMissing(t *testing.T) {
	sdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.SessionDir = sdir
	})
	srv.deletePersistedSession("nonexistent")
}

func TestResumeInterruptedSessionsWithoutStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.resumeInterruptedSessions()
}

// TestSaveSessionSkipsForgottenConversation proves the guard in saveSession:
// a run's goroutine holds a pointer to the conversation and its deferred
// saveSession can fire AFTER handleDeleteSession has forgotten the session
// and deleted its file. Without the guard, that late save would resurrect
// the session on disk.
func TestSaveSessionSkipsForgottenConversation(t *testing.T) {
	sdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.SessionDir = sdir
	})
	// Register a conversation in the registry so saveSession's lookup finds it.
	c := newConversation("s-test-forgotten", &fakeService{})
	srv.sessionsMu.Lock()
	srv.sessions[c.id] = c
	srv.sessionsMu.Unlock()

	srv.saveSession(c) // must write the file
	if _, err := os.Stat(filepath.Join(sdir, "s-test-forgotten.json")); err != nil {
		t.Fatalf("the first save should have written the file: %v", err)
	}
	// Delete the file and forget the session, as handleDeleteSession does.
	srv.forget(c.id)
	os.Remove(filepath.Join(sdir, "s-test-forgotten.json"))

	srv.saveSession(c) // must NOT re-create the file
	if _, err := os.Stat(filepath.Join(sdir, "s-test-forgotten.json")); !os.IsNotExist(err) {
		t.Errorf("a forgotten session must not be re-saved: file exists after saveSession")
	}
}

// --- setProjectID on conversation ---

func TestConversationSetProjectID(t *testing.T) {
	c := newConversation("test", &fakeService{})
	c.setProjectID("proj-1", "/tmp/worktree-1", "/tmp/proj-1")
	if c.projectID != "proj-1" {
		t.Errorf("projectID = %q, want %q", c.projectID, "proj-1")
	}
	if c.workspace != "/tmp/worktree-1" {
		t.Errorf("workspace = %q, want %q", c.workspace, "/tmp/worktree-1")
	}
	// The project's own checkout is tracked separately: for a session with its
	// own worktree it is NOT the workspace, and that difference is what makes
	// "is there work to integrate" answerable.
	if c.projectDir != "/tmp/proj-1" {
		t.Errorf("projectDir = %q, want %q", c.projectDir, "/tmp/proj-1")
	}
}

// --- WebSocket helpers ---

func TestIsClosedConnErr(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		// EOF is the NORMAL end of a WebSocket connection - a browser closing its tab - and it is
		// classified as a clean exit. It used to be classified as a failure, which logged a warning
		// for every client that hung up.
		{io.EOF, true},
		{errors.New("use of closed network connection"), true},
		{errors.New("write: broken pipe"), true},
		{errors.New("read: connection reset by peer"), true},
		{errors.New("some other error"), false},
	}
	for _, tt := range tests {
		got := isClosedConnErr(tt.err)
		if got != tt.want {
			t.Errorf("isClosedConnErr(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestWebSocketHeartbeatLoopCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ackCh := make(chan struct{}, 1)
	cl := &wsClient{heartbeat: 50 * time.Millisecond}
	cl.heartbeatLoop(ctx, ackCh)
}

func TestWsHandshakeNonHijacker(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/ws", nil)
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	_, _, err := wsHandshake(w, r)
	if err == nil {
		t.Error("expected error from non-hijacking ResponseWriter")
	}
}

func TestWsWriteTextViaPipe(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	payload := []byte(`{"hello":"world"}`)
	go wsWriteText(server, payload)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	var hdr [2]byte
	io.ReadFull(client, hdr[:])
	if hdr[0]&0x0f != opText {
		t.Errorf("opcode = %x, want text", hdr[0]&0x0f)
	}
	length := int(hdr[1] & 0x7f)
	body := make([]byte, length)
	io.ReadFull(client, body)
	if string(body) != string(payload) {
		t.Errorf("body = %q, want %q", body, payload)
	}
}

func TestWsWriteCloseViaPipe(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	// net.Pipe is synchronous: the write blocks until the read side consumes it,
	// so the read must happen in a goroutine before the write.
	type result struct {
		payload []byte
		err     error
	}
	done := make(chan result, 1)
	go func() {
		client.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, payload, _, _, err := wsReadFrame(client)
		done <- result{payload, err}
	}()
	if err := wsWriteClose(server, CloseNormal, "bye"); err != nil {
		t.Fatalf("write close: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("read: %v", r.err)
		}
		if len(r.payload) < 2 {
			t.Fatalf("payload too short: %d bytes", len(r.payload))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the close frame")
	}
}

// --- providerKeyPresent ---

func TestProviderKeyPresentForKnownProviders(t *testing.T) {
	got := providerKeyPresent(config.Default(), "openai")
	_ = got
}

func TestProviderKeyPresentWithAPIKey(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.APIKey = "test-key"
	if !providerKeyPresent(cfg, "openai") {
		t.Error("expected true when APIKey is set and provider uses MOTITA_LLM_API_KEY")
	}
}

// --- wsWriteFrame with large payload (>65535) ---

func TestWsWriteFrameLargePayload(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	payload := make([]byte, 70000) // > 65535, triggers 64-bit length
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	done := make(chan struct{})
	go func() {
		wsWriteFrame(server, opBinary, payload)
		close(done)
	}()
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	var hdr [2]byte
	io.ReadFull(client, hdr[:])
	if hdr[1]&0x7f != 127 {
		t.Errorf("expected 64-bit length, got mask %d", hdr[1]&0x7f)
	}
	var lenBuf [8]byte
	io.ReadFull(client, lenBuf[:])
	// Read the rest of the payload
	remaining := make([]byte, 70000)
	io.ReadFull(client, remaining)
	<-done
}

// TestProviderKeyPresentReadsLoginsAndEachProvidersVariable: a stored login counts
// as a credential, and a key exported for one vendor does not count for another.
func TestProviderKeyPresentReadsLoginsAndEachProvidersVariable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOTITA_AUTH_DIR", dir)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	t.Setenv("DASHSCOPE_API_KEY", "")
	os.Unsetenv("DASHSCOPE_API_KEY") // set-but-empty still counts as named; t.Setenv restores it
	cfg := config.Default()
	cfg.LLM.APIKey = ""
	if !providerKeyPresent(cfg, "anthropic") {
		t.Error("ANTHROPIC_API_KEY must count for anthropic")
	}
	if providerKeyPresent(cfg, "qwen") {
		t.Error("nothing is configured for qwen yet")
	}
	if err := oauth.SaveCredential(dir, oauth.Credential{Provider: "qwen", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if !providerKeyPresent(cfg, "qwen") {
		t.Error("a stored login must count")
	}
}
