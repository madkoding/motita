package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRef = "refs/motita/checkpoints/t/1"

func TestSnapshotThenRestoreBringsBackTheTree(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	write(t, filepath.Join(dir, "wip.txt"), "uncommitted\n")
	write(t, filepath.Join(dir, "f.txt"), "edited before\n")
	git(t, dir, "add", "wip.txt")

	head, snap, err := Snapshot(ctx, dir, testRef)
	if err != nil || head == "" || snap == "" {
		t.Fatalf("Snapshot = %q %q %v", head, snap, err)
	}
	if got := git(t, dir, "rev-parse", testRef); got != snap {
		t.Errorf("ref = %s, want %s", got, snap)
	}
	// Taking it must not disturb what the user had staged.
	if got := git(t, dir, "diff", "--cached", "--name-only"); got != "wip.txt" {
		t.Errorf("staged after Snapshot = %q, want wip.txt", got)
	}

	// The agent's work: edit, delete, add, commit.
	write(t, filepath.Join(dir, "f.txt"), "agent edit\n")
	write(t, filepath.Join(dir, "new.txt"), "agent file\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "agent commit")
	write(t, filepath.Join(dir, "later.txt"), "untracked\n")
	if err := os.Remove(filepath.Join(dir, "wip.txt")); err != nil {
		t.Fatal(err)
	}

	if err := Restore(ctx, dir, head, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD = %s, want %s", got, head)
	}
	for name, want := range map[string]string{"f.txt": "edited before\n", "wip.txt": "uncommitted\n"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
	for _, name := range []string{"new.txt", "later.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the restore", name)
		}
	}
	// The changes read as changes again, not as staged.
	if got := git(t, dir, "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("index after Restore = %q, want clean", got)
	}
}

func TestSnapshotLeavesIgnoredFilesOut(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, ".gitignore"), "node_modules/\n")
	git(t, dir, "add", ".gitignore")
	git(t, dir, "commit", "-qm", "ignore")
	write(t, filepath.Join(dir, "node_modules", "x.js"), "x")
	_, snap, err := Snapshot(context.Background(), dir, testRef)
	if err != nil {
		t.Fatal(err)
	}
	if out := git(t, dir, "ls-tree", "-r", "--name-only", snap); strings.Contains(out, "node_modules") {
		t.Errorf("snapshot holds ignored files:\n%s", out)
	}
}

func TestSnapshotIsEmptyWithoutACommit(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	head, snap, err := Snapshot(context.Background(), dir, testRef)
	if head != "" || snap != "" || err != nil {
		t.Errorf("Snapshot = %q %q %v, want empty", head, snap, err)
	}
}

func TestSnapshotRefusesWhatIsNotItsOwnRepository(t *testing.T) {
	dir := newRepo(t)
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, d := range map[string]string{"empty path": "", "subfolder": sub, "not a repo": t.TempDir()} {
		head, snap, err := Snapshot(context.Background(), d, testRef)
		if head != "" || snap != "" || err != nil {
			t.Errorf("%s: Snapshot = %q %q %v, want empty", name, head, snap, err)
		}
		if err := Restore(context.Background(), d, "a", "b"); !errors.Is(err, ErrNoCheckpoint) {
			t.Errorf("%s: Restore = %v, want ErrNoCheckpoint", name, err)
		}
	}
}

func TestRestoreWithoutACheckpointIsRefused(t *testing.T) {
	dir := newRepo(t)
	if err := Restore(context.Background(), dir, "", ""); !errors.Is(err, ErrNoCheckpoint) {
		t.Errorf("Restore = %v, want ErrNoCheckpoint", err)
	}
}

// Every step of Snapshot and Restore names what failed. Each case is a subtest because the
// stubs are undone when THEIR test ends: in one flat loop the first would mask all the others.
func TestSnapshotReportsEachStepThatFails(t *testing.T) {
	// Subtests are numbered, not named after the needle: t.TempDir() puts the test's name in the
	// path, git is called with `-C <path>`, and stubOn matches the whole argument list.
	for i, needle := range []string{"write-tree", "add -A", "commit-tree", "update-ref"} {
		t.Run(fmt.Sprint("step", i), func(t *testing.T) {
			dir := newRepo(t)
			stubOn(t, needle, "boom", errors.New("exit 1"))
			if _, _, err := Snapshot(context.Background(), dir, testRef); err == nil {
				t.Errorf("Snapshot with %q failing reported success", needle)
			}
		})
	}
	t.Run("second-call", func(t *testing.T) {
		dir := newRepo(t)
		calls := 0
		previous := execCommand
		withExec(t, func(c context.Context, name string, args ...string) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), "write-tree") {
				if calls++; calls == 2 {
					return []byte("boom"), errors.New("exit 1")
				}
			}
			return previous(c, name, args...)
		})
		if _, _, err := Snapshot(context.Background(), dir, testRef); err == nil {
			t.Error("the second write-tree failing reported success")
		}
	})
	t.Run("put-back", func(t *testing.T) {
		dir := newRepo(t)
		stubOn(t, "read-tree", "boom", errors.New("exit 1"))
		head, snap, err := Snapshot(context.Background(), dir, testRef)
		if err == nil || head != "" || snap != "" {
			t.Errorf("Snapshot = %q %q %v, want the read-tree failure", head, snap, err)
		}
	})
}

func TestRestoreReportsEachStepThatFails(t *testing.T) {
	for i, needle := range []string{"reset -q --hard", "clean", "read-tree --reset", "-C"} {
		t.Run(fmt.Sprint("step", i), func(t *testing.T) {
			dir := newRepo(t)
			head, snap, err := Snapshot(context.Background(), dir, testRef)
			if err != nil {
				t.Fatal(err)
			}
			if needle == "-C" {
				// The last step is a bare `reset -q`: fail it and nothing earlier.
				previous := execCommand
				withExec(t, func(c context.Context, name string, args ...string) ([]byte, error) {
					if args[len(args)-1] == "-q" && args[len(args)-2] == "reset" {
						return []byte("boom"), errors.New("exit 1")
					}
					return previous(c, name, args...)
				})
			} else {
				stubOn(t, needle, "boom", errors.New("exit 1"))
			}
			if err := Restore(context.Background(), dir, head, snap); err == nil {
				t.Errorf("Restore with %q failing reported success", needle)
			}
		})
	}
}

func TestDropRefRemovesTheReference(t *testing.T) {
	dir := newRepo(t)
	_, _, err := Snapshot(context.Background(), dir, testRef)
	if err != nil {
		t.Fatal(err)
	}
	DropRef(context.Background(), dir, testRef)
	if out := git(t, dir, "for-each-ref", testRef); out != "" {
		t.Errorf("ref still there: %s", out)
	}
	DropRef(context.Background(), dir, testRef) // already gone: no panic, no error
}
