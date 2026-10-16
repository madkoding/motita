package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A clone of one repository holds only that repository: another URL is locked at once, and the
// same URL, spelled with a trailing ".git" or another case of the host, waits.
func TestTheCatalogLocksOneRepositoryAtATime(t *testing.T) {
	srv := &Server{}
	release := srv.lockCatalogRepo("https://Example.com/a/b.git")

	other := make(chan struct{})
	go func() {
		srv.lockCatalogRepo("https://example.com/a/c")()
		close(other)
	}()
	select {
	case <-other:
	case <-time.After(3 * time.Second):
		t.Fatal("another repository waited for the clone of this one")
	}

	same := make(chan struct{})
	go func() {
		srv.lockCatalogRepo("https://example.com/a/b/")()
		close(same)
	}()
	select {
	case <-same:
		t.Fatal("the same repository, spelled another way, did not wait")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-same:
	case <-time.After(3 * time.Second):
		t.Fatal("the lock was not released")
	}
}

// Two clones of different repositories with the same name do not get the same folder, and a folder
// is offered again once its clone is over.
func TestTwoClonesDoNotPickTheSameFolder(t *testing.T) {
	srv := &Server{}
	base := filepath.Join(t.TempDir(), "app")
	first := srv.reserveCloneDir(base, "https://example.com/a/app", nil)
	second := srv.reserveCloneDir(base, "https://example.org/b/app", nil)
	if first != base || second != base+"-2" {
		t.Fatalf("folders = %q, %q", first, second)
	}
	if !srv.beingCloned(first) || !srv.beingCloned(second) {
		t.Error("both folders are being cloned into")
	}
	srv.releaseCloneDir(first)
	if srv.beingCloned(first) {
		t.Error("a finished clone still marks its folder")
	}
	if again := srv.reserveCloneDir(base, "https://example.net/c/app", nil); again != base {
		t.Errorf("a released folder must be offered again, got %q", again)
	}
}

// A clone that is still being made cannot be deleted from under itself.
func TestACloneBeingMadeIsNotDeleted(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	clone := filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "half")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	srv.cloning = map[string]bool{clone: true}
	code := send(t, srv, http.MethodDelete, "/v1/repos/half?force=1", testToken, "").Code
	if code != http.StatusConflict || !pathExists(clone) {
		t.Errorf("a clone being made: %d, exists %v", code, pathExists(clone))
	}
}
