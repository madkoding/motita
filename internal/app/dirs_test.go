package app

// The session and project directories are the two siblings of scheduleDir that the schedule
// work did not touch, and their branches are the same shape: a home when there is one, an empty
// string when there is not. The empty case is what keeps the in-memory behaviour - the
// end-to-end container runs with HOME=/ - and it is the half that had no test, which is why
// both functions measured below the bar while their third sibling did not.

import (
	"path/filepath"
	"testing"
)

// TestTheSessionDirectoryLivesUnderTheHome: conversations are persisted under one folder the
// user can back up or delete as a unit, not scattered wherever the process happens to be.
func TestTheSessionDirectoryLivesUnderTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := sessionDir()
	want := filepath.Join(home, ".motita", "sessions")
	if got != want {
		t.Fatalf("sessionDir() = %q, want %q", got, want)
	}
}

// TestTheSessionDirectoryIsEmptyWithoutAHome: with no HOME there is no sensible location, and
// the empty result tells the caller to keep its in-memory behaviour rather than guess one.
func TestTheSessionDirectoryIsEmptyWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := sessionDir(); got != "" {
		t.Fatalf("sessionDir() = %q with no HOME, want empty", got)
	}
}

// TestTheProjectDirectoryLivesUnderTheHome: the same rule as the sessions and the schedules -
// every piece of state the program owns sits under the motita home.
func TestTheProjectDirectoryLivesUnderTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := projectDir()
	want := filepath.Join(home, ".motita", "projects")
	if got != want {
		t.Fatalf("projectDir() = %q, want %q", got, want)
	}
}

// TestTheProjectDirectoryIsEmptyWithoutAHome: and the same fallback, for the same reason.
func TestTheProjectDirectoryIsEmptyWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := projectDir(); got != "" {
		t.Fatalf("projectDir() = %q with no HOME, want empty", got)
	}
}
