package skills

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// propose writes a proposal the way the background review leaves one.
func propose(t *testing.T, l *Library, name, body string) string {
	t.Helper()
	dir := filepath.Join(l.Root(), ProposedDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name+".md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAProposalIsNotOnTheShelfUntilAccepted(t *testing.T) {
	l := New(t.TempDir())
	if names, err := l.Proposed(); err != nil || names != nil {
		t.Fatalf("no proposals yet: %v %v", names, err)
	}
	propose(t, l, "deploy", "# Deploy\nRun make deploy.\n")
	propose(t, l, "build", "# Build\nRun make.\n")
	if _, err := l.Get("deploy"); err == nil {
		t.Fatal("a proposal is served before the user accepted it")
	}
	if names, err := l.Proposed(); err != nil || strings.Join(names, ",") != "build,deploy" {
		t.Fatalf("proposed: %v %v", names, err)
	}
	s, err := l.Proposal("Deploy")
	if err != nil || s.Title != "Deploy" || !strings.Contains(s.Body, "make deploy") {
		t.Fatalf("read: %+v %v", s, err)
	}

	if err := l.AcceptProposal("deploy"); err != nil {
		t.Fatal(err)
	}
	if s, err := l.Get("deploy"); err != nil || s.Title != "Deploy" {
		t.Errorf("accepted, but not served: %+v %v", s, err)
	}
	if err := l.RejectProposal("build"); err != nil {
		t.Fatal(err)
	}
	if names, _ := l.Proposed(); len(names) != 0 {
		t.Errorf("left waiting: %v", names)
	}
	if _, err := l.Get("build"); err == nil {
		t.Error("a rejected proposal was served")
	}
}

// Accepting a proposal for a skill that exists replaces it: the proposal is its new version.
func TestAcceptingReplacesTheShelvedVersion(t *testing.T) {
	l := New(t.TempDir())
	if _, err := l.Save("deploy", "# Deploy\nold\n"); err != nil {
		t.Fatal(err)
	}
	propose(t, l, "deploy", "# Deploy\nnew\n")
	if err := l.AcceptProposal("deploy"); err != nil {
		t.Fatal(err)
	}
	if s, _ := l.Get("deploy"); !strings.Contains(s.Body, "new") {
		t.Errorf("the shelf kept the old version: %q", s.Body)
	}
}

func TestProposedSkipsWhatIsNotAProposal(t *testing.T) {
	l := New(t.TempDir())
	dir := filepath.Join(l.Root(), ProposedDir)
	propose(t, l, "real", "# Real\n")
	for _, name := range []string{"notes.txt", ".real.123.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link.md")); err != nil {
			t.Fatal(err)
		}
	}
	if names, err := l.Proposed(); err != nil || strings.Join(names, ",") != "real" {
		t.Errorf("proposed: %v %v", names, err)
	}
	// A link is never read, installed or removed through.
	if runtime.GOOS != "windows" {
		if _, err := l.Proposal("link"); !errors.Is(err, ErrNotFound) {
			t.Errorf("read through a link: %v", err)
		}
		if err := l.AcceptProposal("link"); !errors.Is(err, ErrNotFound) {
			t.Errorf("accepted through a link: %v", err)
		}
	}
}

func TestProposalFailures(t *testing.T) {
	l := New(t.TempDir())
	for _, err := range []error{
		func() error { _, err := l.Proposal(" "); return err }(),
		l.AcceptProposal(""),
		l.RejectProposal("..."),
	} {
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Errorf("an empty name: %v", err)
		}
	}
	for _, err := range []error{
		func() error { _, err := l.Proposal("missing"); return err }(),
		l.AcceptProposal("missing"),
		l.RejectProposal("missing"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("no such proposal: %v", err)
		}
	}

	// Past the size limit: not read.
	propose(t, l, "big", "# Big\n"+strings.Repeat("x", 64))
	l.MaxFileBytes = 16
	if _, err := l.Proposal("big"); err == nil || !strings.Contains(err.Error(), "could not read") {
		t.Errorf("an oversized proposal: %v", err)
	}

	// The move refused.
	old := renameFile
	renameFile = func(string, string) error { return errors.New("disk full") }
	err := l.AcceptProposal("big")
	renameFile = old
	if err == nil || !strings.Contains(err.Error(), "could not accept") {
		t.Errorf("a refused move: %v", err)
	}

	// The directory cannot be listed.
	other := New(t.TempDir())
	if err := os.WriteFile(filepath.Join(other.Root(), ProposedDir), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Proposed(); err == nil {
		t.Error("an unreadable proposals directory is not an empty one")
	}
}

func TestRejectingAProposalThatCannotBeRemoved(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write")
	}
	l := New(t.TempDir())
	propose(t, l, "stuck", "# Stuck\n")
	dir := filepath.Join(l.Root(), ProposedDir)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := l.RejectProposal("stuck"); err == nil || !strings.Contains(err.Error(), "could not reject") {
		t.Errorf("a refused removal: %v", err)
	}
}
