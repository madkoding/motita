package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInitMakesARepositoryOnMain(t *testing.T) {
	dir := t.TempDir()
	if err := Init(context.Background(), dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := Repo(context.Background(), dir); err != nil {
		t.Fatalf("not a repository after Init: %v", err)
	}
	if got := git(t, dir, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("branch = %q, want main", got)
	}
}

func TestInitWritesNoIdentityToTheRepository(t *testing.T) {
	dir := t.TempDir()
	if err := Init(context.Background(), dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); containsAny(s, "[user]", "[credential]") {
		t.Errorf("Init copied global configuration into the repository:\n%s", s)
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		for i := 0; i+len(n) <= len(s); i++ {
			if s[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}

func TestInitLeavesAnExistingRepositoryAlone(t *testing.T) {
	dir := newRepo(t)
	head := git(t, dir, "rev-parse", "HEAD")
	if err := Init(context.Background(), dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved: %s -> %s", head, got)
	}
}

func TestInitReportsAMissingGit(t *testing.T) {
	noGit(t)
	if err := Init(context.Background(), t.TempDir()); !isNoGit(err) {
		t.Errorf("err = %v, want ErrNoGit", err)
	}
}

func TestGlobalIdentityRoundTrip(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	if n, e := GlobalIdentity(context.Background()); n != "" || e != "" {
		t.Fatalf("empty config answered %q %q", n, e)
	}
	if err := SetGlobalIdentity(context.Background(), "Ada", "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if n, e := GlobalIdentity(context.Background()); n != "Ada" || e != "ada@example.com" {
		t.Errorf("identity = %q %q", n, e)
	}
}

func TestSetGlobalIdentityFailures(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	for _, needle := range []string{"user.name", "user.email"} {
		failOnGit(t, needle)
		if err := SetGlobalIdentity(context.Background(), "Ada", "ada@example.com"); err == nil {
			t.Errorf("no error when %s cannot be saved", needle)
		}
	}
}
