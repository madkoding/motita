package mockserve

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRunAnnouncesAndExitsOnAFailedBind(t *testing.T) {
	var out bytes.Buffer
	var got *http.Server
	code := -1
	h := http.NotFoundHandler()
	run(&out, "mockx", "127.0.0.1", 1234, h, func(srv *http.Server) error {
		got = srv
		return errors.New("boom")
	}, func(c int) { code = c })
	if got.Addr != "127.0.0.1:1234" || got.ReadHeaderTimeout != 10*time.Second || got.Handler == nil {
		t.Fatalf("server not configured: %+v", got)
	}
	if code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	s := out.String()
	if !strings.Contains(s, "mockx listening on http://127.0.0.1:1234/v1") || !strings.Contains(s, "error: boom") {
		t.Fatalf("output %q", s)
	}
}

func TestRunDoesNotExitWhenTheServerStopsCleanly(t *testing.T) {
	Run("mockx", "127.0.0.1", 1, http.NotFoundHandler(), func(*http.Server) error { return nil }, func(int) {
		t.Fatal("exit called on a clean stop")
	})
}

func TestListenForcedFailure(t *testing.T) {
	t.Setenv("MOCKSERVE_FAIL", "1")
	if err := Listen("MOCKSERVE_FAIL")(&http.Server{}); err == nil {
		t.Fatal("expected the forced failure")
	}
}

func TestListenReportsARealBindError(t *testing.T) {
	t.Setenv("MOCKSERVE_FAIL", "")
	// An address that cannot be parsed makes ListenAndServe return at once.
	if err := Listen("MOCKSERVE_FAIL")(&http.Server{Addr: "127.0.0.1:not-a-port"}); err == nil {
		t.Fatal("expected a bind error")
	}
}
