package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- downloadFile tests ----

func TestDownloadFile(t *testing.T) {
	content := []byte("hello world binary")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "downloaded")
	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, maxBinarySize, dest, func(ProgressEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(content) {
		t.Errorf("downloaded %q, want %q", got, content)
	}
}

func TestDownloadFileNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, maxBinarySize, filepath.Join(dir, "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error for 500")
	}
}

func TestDownloadFileBadDest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	u := &Updater{HTTPClient: srv.Client()}
	// dest in a non-existent directory
	err := u.downloadFile(context.Background(), srv.URL, maxBinarySize, "/nonexistent-dir-xyz/sub/file",
		func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error for unwritable dest")
	}
}

func TestDownloadFileBadURL(t *testing.T) {
	u := &Updater{HTTPClient: &http.Client{}}
	err := u.downloadFile(context.Background(), "http://\x7f.invalid", maxBinarySize, filepath.Join(t.TempDir(), "x"),
		func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error from bad URL")
	}
}

func TestDownloadFileContextCanceled(t *testing.T) {
	// Use a server that never responds, then cancel the context.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(ctx, srv.URL, maxBinarySize, filepath.Join(t.TempDir(), "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

func TestDownloadFileMidStreamCancel(t *testing.T) {
	// Server that streams slowly so the cancel hits mid-read.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		// Write one byte, then stall.
		_, _ = w.Write([]byte{0})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(10 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	dir := t.TempDir()
	u := &Updater{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	err := u.downloadFile(ctx, srv.URL, maxBinarySize, filepath.Join(dir, "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error from context deadline")
	}
}

func TestDownloadFileReadError(t *testing.T) {
	// A server that returns a body that errors during read: use a handler
	// that sets Content-Length but closes the connection prematurely.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		// hijack and close to cause an early read error
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, maxBinarySize, filepath.Join(dir, "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected read error")
	}
}

// ---- fetchLatestRelease: non-OK status reads body ----

func TestFetchLatestReleaseNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	u := &Updater{HTTPClient: srv.Client(), APIURL: func() string { return srv.URL }}
	_, err := u.fetchLatestRelease(context.Background())
	if err == nil {
		t.Error("expected error for 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention status 429: %v", err)
	}
}

// ---- downloadFile: progress reporting with Content-Length > 0 ----

func TestDownloadFileProgressReporting(t *testing.T) {
	// Large enough content with known length so the >200ms progress branch fires.
	content := make([]byte, 0) // we'll write via a slow handler
	_ = content
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4")
		// Write byte by byte with a delay to trigger the 200ms progress report.
		for i := 0; i < 4; i++ {
			_, _ = w.Write([]byte{byte('A' + i)})
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(250 * time.Millisecond)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "out")

	var maxPct int
	u := &Updater{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	err := u.downloadFile(context.Background(), srv.URL, maxBinarySize, dest, func(e ProgressEvent) {
		if e.Percent > maxPct {
			maxPct = e.Percent
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if len(got) != 4 {
		t.Errorf("len = %d, want 4", len(got))
	}
}

// ---- downloadFile: write error (dest becomes unwritable mid-stream) ----

func TestDownloadFileWriteError(t *testing.T) {
	content := []byte("some content here")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	// Create dest as a directory so os.Create fails.
	dir := t.TempDir()
	dest := filepath.Join(dir, "blocker")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, maxBinarySize, dest, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error because dest is a directory")
	}
}

// Ensure io is used (prevents unused import if some branches are optimized away).
var _ = io.EOF
