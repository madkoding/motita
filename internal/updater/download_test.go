package updater

// downloadFile's cancellation path, reached through the injectable HTTPClient - no production
// seam is added for it.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cancelOnFirstRead is a body that ends the context WHILE the stream is still alive and then
// hands back a successful read. That ordering is the whole point: the loop only takes its
// ctx.Done case when the previous iteration did NOT return, so the check at the top of the loop
// is what stops the download here. A body that fails the read instead (what a real cancelled
// request does) exits through the error path and never reaches it.
type cancelOnFirstRead struct {
	cancel  context.CancelFunc
	payload []byte
	read    bool
}

func (b *cancelOnFirstRead) Read(p []byte) (int, error) {
	if !b.read {
		b.read = true
		b.cancel()
		return copy(p, b.payload), nil // no error: the loop goes round again
	}
	return 0, io.EOF
}

func (b *cancelOnFirstRead) Close() error { return nil }

// bodyTransport answers every request with a response the test dictates, so the loop sees the
// body it was built for and nothing else.
type bodyTransport struct {
	body   io.ReadCloser
	length int64
}

func (t *bodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Body:          t.body,
		ContentLength: t.length,
	}, nil
}

// TestDownloadFileStopsWhenTheContextEndsBetweenReads: a download must stop at the next
// boundary once the caller has given up, and say so with the context's error rather than a
// partial file that looks complete.
func TestDownloadFileStopsWhenTheContextEndsBetweenReads(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	body := &cancelOnFirstRead{cancel: cancel, payload: []byte("data")}
	u := &Updater{HTTPClient: &http.Client{Transport: &bodyTransport{body: body, length: 4}}}

	err := u.downloadFile(ctx, "http://example.invalid/bin", filepath.Join(dir, "out"), func(ProgressEvent) {})
	if err == nil {
		t.Fatal("a download whose context ended must be reported as failed, not completed")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the error must be the context's, got %v", err)
	}
}

// TestDownloadFileReportsAFailedWrite: the destination is opened before the body is read, so a
// destination that cannot be written is reported rather than half-populated.
func TestDownloadFileReportsAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	// The destination is a directory: os.Create fails on it.
	dest := filepath.Join(dir, "adirectory")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	body := &cancelOnFirstRead{cancel: func() {}, payload: []byte("data")}
	u := &Updater{HTTPClient: &http.Client{Transport: &bodyTransport{body: body, length: 4}}}

	err := u.downloadFile(context.Background(), "http://example.invalid/bin", dest, func(ProgressEvent) {})
	if err == nil {
		t.Fatal("a destination that cannot be written must be reported")
	}
	if strings.Contains(err.Error(), "answered") {
		t.Errorf("the failure is the destination, not the response: %v", err)
	}
}
