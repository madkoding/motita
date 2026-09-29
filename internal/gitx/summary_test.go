package gitx

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestChangesSinceReportsEveryKindOfChange(t *testing.T) {
	repo := newRepo(t)
	write(t, filepath.Join(repo, "gone.txt"), "x\n")
	write(t, filepath.Join(repo, "old.txt"), "one\ntwo\nthree\nfour\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "more")
	rev := HeadRev(context.Background(), repo)
	if rev == "" {
		t.Fatal("HeadRev empty in a repository with commits")
	}

	write(t, filepath.Join(repo, "f.txt"), "base\nmore\n")
	git(t, repo, "rm", "-q", "gone.txt")
	git(t, repo, "mv", "old.txt", "new.txt")
	write(t, filepath.Join(repo, "fresh.txt"), "new\n")
	git(t, repo, "add", "new.txt")

	got, err := ChangesSince(context.Background(), repo, rev)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]FileChange{}
	for _, c := range got {
		by[c.Path] = c
	}
	if by["f.txt"].Status != "modified" || by["f.txt"].Added != 1 {
		t.Errorf("f.txt = %+v", by["f.txt"])
	}
	if by["gone.txt"].Status != "deleted" || by["gone.txt"].Deleted != 1 {
		t.Errorf("gone.txt = %+v", by["gone.txt"])
	}
	if by["new.txt"].Status != "renamed" {
		t.Errorf("new.txt = %+v", by["new.txt"])
	}
	if by["fresh.txt"].Status != "added" {
		t.Errorf("fresh.txt = %+v", by["fresh.txt"])
	}
}

func TestChangesSinceEdgeCases(t *testing.T) {
	ctx := context.Background()
	if c, err := ChangesSince(ctx, "", ""); c != nil || err != nil {
		t.Errorf("empty dir = %v, %v", c, err)
	}
	if c, err := ChangesSince(ctx, t.TempDir(), ""); c != nil || err != nil {
		t.Errorf("not a repo = %v, %v", c, err)
	}
	if HeadRev(ctx, t.TempDir()) != "" {
		t.Error("HeadRev outside a repository must be empty")
	}
	// No commit before the run: everything is new.
	fresh := newRepo(t)
	write(t, filepath.Join(fresh, "a.txt"), "a\n")
	got, err := ChangesSince(ctx, fresh, "")
	if err != nil || len(got) == 0 {
		t.Errorf("empty rev = %v, %v", got, err)
	}
}

func TestChangesSinceFailures(t *testing.T) {
	repo := newRepo(t)
	for _, needle := range []string{"--name-status", "--numstat", "ls-files"} {
		t.Run(needle, func(t *testing.T) {
			failOnGit(t, needle)
			if _, err := ChangesSince(context.Background(), repo, "HEAD"); err == nil {
				t.Error("want an error")
			}
		})
	}
	t.Run("no git", func(t *testing.T) {
		noGit(t)
		if _, err := ChangesSince(context.Background(), repo, "HEAD"); !isNoGit(err) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestParseChangesAndLastPath(t *testing.T) {
	got := parseChanges("M\ta.go\nbad\nR100\told.go\tnew.go", "1\t2\ta.go\n-\t-\tbin.png\nx\n", "u.txt\n\na.go\n")
	want := []FileChange{
		{Path: "a.go", Status: "modified", Added: 1, Deleted: 2},
		{Path: "new.go", Status: "renamed"},
		{Path: "u.txt", Status: "added"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	for in, out := range map[string]string{
		"plain.go":        "plain.go",
		"a => b":          "b",
		"d/{x => y}/f.go": "d/y/f.go",
		"d/{ => y}/f.go":  "d/y/f.go",
		"d/{unclosed":     "d/{unclosed",
		"d/{no arrow}/f":  "d/{no arrow}/f",
	} {
		if g := lastPath(in); g != out {
			t.Errorf("lastPath(%q) = %q, want %q", in, g, out)
		}
	}
}

func TestChangesSinceEmptyTreeFailure(t *testing.T) {
	repo := newRepo(t)
	failOnGit(t, "hash-object")
	if _, err := ChangesSince(context.Background(), repo, ""); err == nil {
		t.Error("want an error when the empty tree cannot be built")
	}
}
