package gateway

// The conversation archive's life on the server side: the folder belongs to the conversation,
// so it is deleted with it, swept when a session disappears while the process is down, and
// bound to the service that serves it so a restart finds it again.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contextService is a service that keeps the archive id it was given, which the real runner
// does and the plain fakeService does not.
type contextService struct {
	*fakeService
	contextID string
}

func (c *contextService) SetContextID(id string) { c.contextID = id }

func TestBindContextGivesEachConversationItsOwnArchive(t *testing.T) {
	svc := &contextService{fakeService: &fakeService{}}
	srv := newTestServer(t, svc)
	if svc.contextID != DefaultSession {
		t.Errorf("the default conversation is bound to %q, want %q", svc.contextID, DefaultSession)
	}

	srv.bindContext(nil)
	srv.bindContext(&conversation{})
	// A service that does not keep an archive is left alone: the interface is optional.
	srv.bindContext(&conversation{id: "s9", svc: &fakeService{}})
}

func TestDeletePersistedSessionDeletesTheArchive(t *testing.T) {
	srv, _ := startServer(t, Options{Token: testToken, SessionDir: t.TempDir()})
	srv.opts.ContextDir = t.TempDir()

	dir := filepath.Join(srv.opts.ContextDir, "s-deleted")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "turn-000001-chat.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.deletePersistedSession("s-deleted")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the archive outlived its conversation: %v", err)
	}

	// A conversation with no archive is not an error.
	srv.deletePersistedSession("never-existed")
}

func TestDeleteContextReportsAFolderItCannotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission that makes the removal fail")
	}
	srv, sink := startServer(t, Options{Token: testToken, SessionDir: t.TempDir()})
	srv.opts.ContextDir = t.TempDir()

	dir := filepath.Join(srv.opts.ContextDir, "s-stuck")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(srv.opts.ContextDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(srv.opts.ContextDir, 0o700) })

	srv.deleteContext("s-stuck")
	if !strings.Contains(strings.Join(sink.lines(t), "\n"), "could not delete the conversation archive") {
		t.Errorf("the failure was not reported:\n%s", strings.Join(sink.lines(t), "\n"))
	}

	// The gateway was never told where the archives live: it must not guess one.
	(&Server{}).deleteContext("s-stuck")
}

func TestLoadPersistedSessionsSweepsOrphanArchives(t *testing.T) {
	srv, _ := startServer(t, Options{Token: testToken, SessionDir: t.TempDir()})
	srv.opts.ContextDir = t.TempDir()

	keep := filepath.Join(srv.opts.ContextDir, DefaultSession)
	orphan := filepath.Join(srv.opts.ContextDir, "s-gone")
	for _, dir := range []string{keep, orphan} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	srv.loadPersistedSessions()

	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the archive of the conversation in memory was removed: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("the archive of a session nobody knows survived: %v", err)
	}
}

func TestLoadPersistedSessionsReportsAnUnreadableArchiveRoot(t *testing.T) {
	srv, sink := startServer(t, Options{Token: testToken, SessionDir: t.TempDir()})
	// A FILE where the folder of archives should be: the sweep cannot even read it.
	root := filepath.Join(t.TempDir(), "archives")
	if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.opts.ContextDir = root
	srv.loadPersistedSessions()

	if !strings.Contains(strings.Join(sink.lines(t), "\n"), "could not sweep the conversation archives") {
		t.Errorf("the sweep failure was not reported:\n%s", strings.Join(sink.lines(t), "\n"))
	}
}
