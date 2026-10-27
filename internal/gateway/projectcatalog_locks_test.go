package gateway

import (
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
