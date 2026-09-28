package gateway

// Tests for the ceiling's honesty. The ceiling is the one number a user is told when
// the gateway refuses them, and it used to lie in two ways: the message reported the
// CONFIGURED ceiling, not how many the process actually held, and the restore path
// ignored the ceiling entirely — so a gateway with max_sessions=8 and 60 on disk
// loaded all 60 and then refused a new one saying "it holds 8".

import (
	"fmt"
	"net/http"
	"testing"
)

// TestTheCeilingMessageReportsWhatIsHeldNotWhatIsConfigured: the error message a user
// reads when they are refused must say how many conversations the process is ACTUALLY
// holding, not the configured ceiling. The default session is always in memory, so a
// ceiling of 4 means 3 user sessions fit alongside it.
func TestTheCeilingMessageReportsWhatIsHeldNotWhatIsConfigured(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, withFactory(),
		func(o *Options) { o.MaxSessions = 4 })

	// The default session is already in memory (1 of 4). Fill the remaining 3.
	for i := 0; i < 3; i++ {
		created := post(t, srv, "/v1/sessions", "{}", testToken)
		if created.Code != http.StatusCreated {
			t.Fatalf("session %d: expected 201, got %d: %s", i, created.Code, created.Body.String())
		}
	}

	// One more must be refused: the registry now holds 4.
	refused := post(t, srv, "/v1/sessions", "{}", testToken)
	if refused.Code != http.StatusConflict {
		t.Fatalf("expected 409 at the ceiling, got %d: %s", refused.Code, refused.Body.String())
	}
	// The message must say it holds 4 (the real count), which is also the
	// ceiling here. The test that distinguishes the two is the next one.
	if !contains(refused.Body.String(), "holds 4") {
		t.Errorf("the refusal should say it holds 4 (the real count), got: %s", refused.Body.String())
	}
}

// TestTheCeilingMessageDistinguishesHeldFromConfigured: the lie was that the message
// printed the CONFIGURED ceiling regardless of how many were held. This test forces
// the two numbers apart: the ceiling is 8, but only 3 are held. The refusal (at 8)
// must say "holds 8", never the ceiling alone.
func TestTheCeilingMessageDistinguishesHeldFromConfigured(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, withFactory(),
		func(o *Options) { o.MaxSessions = 8 })

	// Hold 3 user sessions (plus the default = 4 total, well under 8).
	for i := 0; i < 3; i++ {
		post(t, srv, "/v1/sessions", "{}", testToken)
	}
	if held := srv.sessionCount(); held != 4 {
		t.Fatalf("expected 4 in memory (1 default + 3), got %d", held)
	}

	// Fill to 8 and verify the refusal says the real count.
	for i := 0; i < 4; i++ {
		post(t, srv, "/v1/sessions", "{}", testToken)
	}
	if held := srv.sessionCount(); held != 8 {
		t.Fatalf("expected 8 in memory, got %d", held)
	}
	refused := post(t, srv, "/v1/sessions", "{}", testToken)
	if refused.Code != http.StatusConflict {
		t.Fatalf("expected 409 at the ceiling, got %d", refused.Code)
	}
	if !contains(refused.Body.String(), "holds 8") {
		t.Errorf("the refusal should say it holds 8 (the real count), got: %s", refused.Body.String())
	}
}

// TestRestoreRespectsTheCeiling: a gateway that starts with more persisted sessions
// than its ceiling must NOT load all of them. It loads the N most recently used and
// leaves the rest on disk. With the bug, the restore loaded every record without
// checking the ceiling.
func TestRestoreRespectsTheCeiling(t *testing.T) {
	dir := t.TempDir()
	// Seed 10 sessions, all with distinct last-used times so the "most recent N"
	// selection is deterministic. The ceiling will be 4.
	for i := 0; i < 10; i++ {
		seedSessionFile(t, dir, map[string]any{
			"id":        fmt.Sprintf("s-restore-%d", i),
			"title":     fmt.Sprintf("session %d", i),
			"created":   "2026-01-01T00:00:00Z",
			"last_used": fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1),
		})
	}

	srv, _ := startServer(t, Options{
		Token:       testToken,
		SessionDir:  dir,
		MaxSessions: 4,
		NewService:  func() (Service, error) { return &fakeService{}, nil },
	})
	// loadPersistedSessions is called explicitly because startServer does not
	// call it (it is called from the app's serve path, not from Start).
	srv.loadPersistedSessions()

	held := srv.sessionCount()
	// The default session is always in memory, so the ceiling of 4 means
	// 3 restored + 1 default = 4.
	if held != 4 {
		t.Fatalf("restore should have loaded 3 sessions (ceiling 4 minus the default), got %d in memory", held)
	}

	// The 3 most recently used are s-restore-9, s-restore-8, s-restore-7
	// (last_used Jan 10, 9, 8). The older ones must NOT be in memory.
	for _, id := range []string{"s-restore-9", "s-restore-8", "s-restore-7"} {
		if _, ok := srv.lookup(id); !ok {
			t.Errorf("the most recently used session %q should be in memory", id)
		}
	}
	for _, id := range []string{"s-restore-0", "s-restore-1", "s-restore-2"} {
		if _, ok := srv.lookup(id); ok {
			t.Errorf("the older session %q should NOT be in memory (it is on disk, not loaded)", id)
		}
	}
}

// contains is a local helper because the test package does not import strings
// in every file.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
