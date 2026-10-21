package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// TestThePrivateSystemTreesAreRootLike: on macOS /etc and /var are links into /private.
func TestThePrivateSystemTreesAreRootLike(t *testing.T) {
	for _, target := range []string{"/private/etc", "/private/var"} {
		if !isRootLike(target, "") {
			t.Errorf("isRootLike(%q) = false: on macOS it is the system tree itself", target)
		}
	}
}

// TestAWorkspaceReachedThroughALinkIsStillTheWorkspace: on macOS /tmp is a link to /private/tmp,
// so a workspace that does not exist yet under it still holds the files written inside it.
func TestAWorkspaceReachedThroughALinkIsStillTheWorkspace(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	dir := filepath.Join(link, "not-yet")
	if out := firstOutside([]string{"a.txt", filepath.Join(dir, "b.txt")}, dir); out != "" {
		t.Errorf("%q is inside the workspace %s", out, dir)
	}
	if out := firstOutside([]string{filepath.Join(real, "c.txt")}, dir); out == "" {
		t.Error("a sibling of the workspace is outside it")
	}
}
