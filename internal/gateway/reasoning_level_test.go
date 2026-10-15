package gateway

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// A level the agent does not know is refused with 400 and neither applied nor saved: kept, it would
// be sent upstream on every turn and the provider would answer 400 to each one.
func TestReasoningRefusesAnUnknownLevel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	svc := &fakeService{}
	srv, _ := startServer(t, Options{
		Token: testToken, WorkspaceDir: t.TempDir(), SessionDir: dir,
		ProjectDir: filepath.Join(t.TempDir(), "projects"),
		Service:    svc, NewService: func() (Service, error) { return &fakeService{}, nil },
	})
	post(t, srv, sessionPath(srv, DefaultSession, "/reasoning"), `{"level":"low"}`, testToken)

	w := post(t, srv, sessionPath(srv, DefaultSession, "/reasoning"), `{"level":"bogus"}`, testToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bogus") {
		t.Errorf("the error must name the refused level: %s", w.Body.String())
	}
	if svc.reasoning != "low" {
		t.Errorf("level = %q, want the previous one kept", svc.reasoning)
	}
	if rec := readRecordOnDisk(t, dir, DefaultSession); rec.Reasoning != "low" {
		t.Errorf("stored reasoning = %q, want low", rec.Reasoning)
	}
}

// Every level the agent knows is accepted.
func TestReasoningAcceptsEveryKnownLevel(t *testing.T) {
	svc := &fakeService{}
	srv := newTestServer(t, svc)
	for _, level := range []string{"off", "low", "medium", "high"} {
		w := post(t, srv, sessionPath(srv, DefaultSession, "/reasoning"), `{"level":"`+level+`"}`, testToken)
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d %s", level, w.Code, w.Body.String())
		}
		if svc.reasoning != level {
			t.Errorf("level = %q, want %q", svc.reasoning, level)
		}
	}
}
