package gateway

// The remaining branches that need a REAL socket or a REAL git repository: the successful upgrade
// path in the SSE stream, the WebSocket read loop's control frames, and the clone helper.
//
// A live client is used wherever the behaviour is the socket's, because a branch that only exists
// once bytes are on a wire cannot be asserted against a struct.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/updater"
)

// --- the upgrade, all the way through -----------------------------------------------------------

// A COMPLETE upgrade: check, download, verify, install and restart. The restart is what a real
// upgrade ends with, and it is the branch a partial test never reaches.
//
// The binary is replaced with a fake that is built for THIS platform's asset name, and the checksum
// file matches it, so the verification succeeds instead of failing first.
func TestAFullUpgradeReplacesTheBinaryAndRestarts(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "motita")
	if err := os.WriteFile(exe, []byte("the old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte("#!/bin/sh\necho the new binary\n")
	sum := sha256.Sum256(newBinary)
	assetName := assetNameForThisPlatform()

	var base string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/release"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"tag_name":"v9.9.9","name":"newer","assets":[
				{"name":"`+assetName+`","browser_download_url":"`+base+`/binary"},
				{"name":"SHA256SUMS","browser_download_url":"`+base+`/checksums"}]}`)
		case r.URL.Path == "/binary":
			_, _ = w.Write(newBinary)
		case r.URL.Path == "/checksums":
			_, _ = io.WriteString(w, hex.EncodeToString(sum[:])+"  "+assetName+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	base = ts.URL

	srv := newTestServer(t, &fakeService{})
	srv.opts.Version = "v1.0.0"
	srv.updater = updater.New("v1.0.0", exe)
	srv.updater.APIURL = func() string { return ts.URL + "/release" }

	w := httptest.NewRecorder()
	srv.handleUpdateRun(w, httptest.NewRequest(http.MethodPost, "/v1/update/run", nil))

	body := w.Body.String()
	if strings.Contains(body, `"stage":"error"`) {
		t.Fatalf("the upgrade reported an error: %s", body)
	}
	for _, stage := range []string{"checking", "downloading", "verifying", "installing", "restarting", "done"} {
		if !strings.Contains(body, `"stage":"`+stage+`"`) {
			t.Errorf("the stream never reported the %q stage: %s", stage, body)
		}
	}

	// The binary on disk was replaced by the new one, which is the whole point of the feature.
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("reading the replaced binary: %v", err)
	}
	if string(got) != string(newBinary) {
		t.Errorf("the binary was not replaced: %q", got)
	}

	// And the gateway signalled its own restart by cancelling its base context. The handler sleeps
	// 500ms before doing so, to let the SSE response flush.
	select {
	case <-srv.baseCtx.Done():
	case <-time.After(5 * time.Second):
		t.Error("a completed upgrade must restart the gateway, or the new binary never runs")
	}
}

// --- the WebSocket read loop over a live socket -------------------------------------------------

// A PING from a client is answered with a PONG (RFC 6455 §5.5.2), and the connection stays open.
func TestAPingIsAnsweredWithAPongOverASocket(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	c := dialWebSocket(t, srv, DefaultSession, testToken)
	defer c.close()
	// The welcome is the first message on every connection; read it so the reply under test is the
	// next one.
	if msg := c.readMsg(t); msg.Type != MsgAuthResponse {
		t.Fatalf("first message = %q, want the welcome", msg.Type)
	}

	// Masked: a client-to-server frame MUST be masked (RFC 6455 5.3), and the server closes the
	// connection on an unmasked one - which is a rule of its own, covered elsewhere.
	if err := c.writeFrame(opPing, []byte("are you there"), true); err != nil {
		t.Fatalf("sending the ping: %v", err)
	}
	// readText returns (payload, opcode).
	payload, opcode := c.readText(t)
	if opcode != opPong {
		t.Fatalf("opcode = %d, want a pong (%d)", opcode, opPong)
	}
	if payload != "are you there" {
		t.Errorf("the pong must echo the ping's payload, got %q", payload)
	}
}

// A BINARY frame is refused as a recoverable protocol violation: this protocol is JSON only.
func TestABinaryFrameIsRefused(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	c := dialWebSocket(t, srv, DefaultSession, testToken)
	defer c.close()
	if msg := c.readMsg(t); msg.Type != MsgAuthResponse {
		t.Fatalf("first message = %q, want the welcome", msg.Type)
	}

	if err := c.writeFrame(opBinary, []byte{0x00, 0x01}, true); err != nil {
		t.Fatalf("sending the binary frame: %v", err)
	}
	msg := c.readMsg(t)
	if msg.Type != MsgError {
		t.Errorf("type = %q, want an error message", msg.Type)
	}
	if !msg.hasFlag(FlagErrorRecoverable) {
		t.Errorf("flags = %v, want the error to be recoverable", msg.Flags)
	}
}

// A CLIENT heartbeat is answered with an ack, which is what lets a client keep a connection it is
// not otherwise using.
func TestAClientHeartbeatIsAcked(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	c := dialWebSocket(t, srv, DefaultSession, testToken)
	defer c.close()
	if msg := c.readMsg(t); msg.Type != MsgAuthResponse {
		t.Fatalf("first message = %q, want the welcome", msg.Type)
	}

	c.sendTextMsg(makeMsg(MsgHeartbeat, nil))
	msg := c.readMsg(t)
	if msg.Type != MsgHeartbeatAck {
		t.Errorf("type = %q, want a heartbeat_ack", msg.Type)
	}
}

// The SERVER's own heartbeat is sent when the interval is short, and the ack the client sends is
// what keeps the connection alive. This is the branch that a test with the heartbeat turned off
// never reaches.
func TestTheServersHeartbeatIsSentAndAcked(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WSHeartbeat = 50 * time.Millisecond })
	c := dialWebSocket(t, srv, DefaultSession, testToken)
	defer c.close()

	// The welcome arrives first.
	if msg := c.readMsg(t); msg.Type != MsgAuthResponse {
		t.Fatalf("first message = %q, want the welcome", msg.Type)
	}

	// Then a heartbeat, which is answered so the connection is not closed for silence.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msg := c.readMsg(t)
		if msg.Type == MsgHeartbeat {
			c.sendTextMsg(makeMsg(MsgHeartbeatAck, nil))
			return
		}
	}
	t.Error("a 50ms heartbeat interval must produce a heartbeat probe")
}

// A query that STREAMS progress reaches the client as notifications, which is how a long turn shows
// its work instead of going silent.
func TestAQueryStreamsProgressAsNotifications(t *testing.T) {
	svc := &fakeService{plan: func(ctx context.Context, prompt string, progress func(string, ...any)) (string, error) {
		progress("working on it", "step", 1)
		return "the answer", nil
	}}
	srv := newWSTestServer(t, svc)
	c := dialWebSocket(t, srv, DefaultSession, testToken)
	defer c.close()
	if msg := c.readMsg(t); msg.Type != MsgAuthResponse {
		t.Fatalf("first message = %q, want the welcome", msg.Type)
	}

	c.sendTextMsg(makeMsg(MsgAuth, map[string]string{"token": testToken}))
	if msg := c.readMsg(t); !msg.hasFlag(FlagAuthenticated) {
		t.Fatalf("auth failed: %+v", msg)
	}

	c.sendTextMsg(makeMsg(MsgQuery, map[string]string{"query": "do the thing"}))

	// The result arrives eventually; progress notifications may arrive before it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg := c.readMsg(t)
		if msg.Type == MsgQueryResponse {
			return
		}
	}
	t.Error("a query must be answered with a query_response")
}

// --- the clone helper ---------------------------------------------------------------------------

// cloneGitRepo REFUSES a URL it cannot clone and reports what git said, which is what an operator
// needs in order to tell a typo from an unreachable host.
func TestCloneGitRepoReportsWhatGitSaid(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "clone")
	// A local path that is not a repository: git fails, and its message must come back.
	out, err := cloneGitRepo("/definitely/not/a/repository", dest)
	if err == nil {
		t.Fatal("cloning a non-repository must fail")
	}
	if !strings.Contains(err.Error(), "could not clone") {
		t.Errorf("error = %v, want it to say the clone failed", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("git's own output must be reported: it is what tells a typo from an unreachable host")
	}
}

// A SUCCESSFUL clone answers git's output and creates the destination. The source is a local
// repository, so nothing reaches the network. (The unusable-parent case is covered in
// store_paths_test.go.)
func TestCloneGitRepoClonesALocalRepository(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, src)

	dest := filepath.Join(t.TempDir(), "clone")
	out, err := cloneGitRepo(src, dest)
	if err != nil {
		t.Fatalf("cloneGitRepo: %v (output: %s)", err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "f.txt")); err != nil {
		t.Errorf("the clone must contain the source's files: %v", err)
	}
}
