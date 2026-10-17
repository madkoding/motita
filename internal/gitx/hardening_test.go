package gitx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRepositoryConfigRunsNoCode: the repository's own .git/config is writable by whatever
// worked in it, and every git this package starts runs as the user, outside the sandbox. A
// core.fsmonitor command and the hooks a commit, a merge and a checkout run are therefore
// commands an agent could have planted; none of them may run.
func TestRepositoryConfigRunsNoCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted commands are shell scripts")
	}
	ctx := context.Background()
	repo := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\necho \"$0\" >> " + marker + "\n"
	hooks := filepath.Join(repo, ".git", "hooks")
	for _, hook := range []string{"pre-commit", "commit-msg", "post-commit", "pre-merge-commit", "post-merge",
		"post-checkout"} {
		write(t, filepath.Join(hooks, hook), script)
		if err := os.Chmod(filepath.Join(hooks, hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	monitor := filepath.Join(t.TempDir(), "monitor")
	write(t, monitor, script)
	if err := os.Chmod(monitor, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "config", "core.fsmonitor", monitor)

	write(t, filepath.Join(repo, "g.txt"), "dirty\n")
	if _, err := Dirty(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitAll(ctx, repo, "work"); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "branch", "side")
	if err := Checkout(ctx, repo, "side"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "h.txt"), "side\n")
	if _, err := CommitAll(ctx, repo, "side work"); err != nil {
		t.Fatal(err)
	}
	if err := Checkout(ctx, repo, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeInto(ctx, repo, "main", "side", "merge side"); err != nil {
		t.Fatal(err)
	}
	if exists(marker) {
		t.Errorf("repository config ran code: %s", read(t, marker))
	}
}

// TestBranchNamesAreNeverOptions: a branch name reaches git as an argument, and one that starts
// with "-" would be read as a flag: --output writes a file, --upload-pack runs a command.
func TestBranchNamesAreNeverOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted command is a shell command")
	}
	ctx := context.Background()
	repo := newRepo(t)
	pushToOrigin(t, repo)
	marker := filepath.Join(t.TempDir(), "ran")

	if _, _, err := CommitsBetween(ctx, repo, "--output="+marker, "main"); err == nil {
		t.Error("CommitsBetween with a flag for a branch reported success")
	}
	if err := PullFastForward(ctx, repo, "--upload-pack=touch "+marker+";"); err == nil {
		t.Error("PullFastForward with a flag for a branch reported success")
	}
	if exists(marker) {
		t.Error("a branch name was read as an option")
	}
}

// TestSnapshotRunsNoFilterDriver: a repository's local config can name a clean filter and an
// attributes file can point every file at it, so `git add -A` would run that filter's command -
// as the user, outside any sandbox. Snapshot runs the add, and it must disarm the filter first.
// This is the F5 regression: a prompt injection that writes .git/config and .gitattributes must
// not reach code through the gateway's own snapshot.
func TestSnapshotRunsNoFilterDriver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted filter is a shell command")
	}
	ctx := context.Background()
	repo := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	// A required filter: with the command emptied but the requirement kept, `git add` would fail
	// closed; the fix drops the requirement too, so the snapshot still succeeds.
	git(t, repo, "config", "filter.evil.clean", "touch "+marker+"; cat")
	git(t, repo, "config", "filter.evil.required", "true")
	write(t, filepath.Join(repo, ".gitattributes"), "* filter=evil\n")
	write(t, filepath.Join(repo, "work.txt"), "dirty\n")

	head, snap, err := Snapshot(ctx, repo, testRef)
	if err != nil {
		t.Fatalf("Snapshot = %v", err)
	}
	if head == "" || snap == "" {
		t.Fatalf("Snapshot produced no checkpoint: %q %q", head, snap)
	}
	if exists(marker) {
		t.Error("the repository's clean filter ran during Snapshot")
	}
}

// TestNeutralizeCoversEveryCommandKey walks the config keys whose value git runs as a command and
// checks each is disarmed, and that an ordinary key is left alone. It is a table so a new key in
// the switch is a new row, not a new test.
func TestNeutralizeCoversEveryCommandKey(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"filter.a.clean", "-c filter.a.clean= -c filter.a.required=false"},
		{"filter.a.smudge", "-c filter.a.smudge= -c filter.a.required=false"},
		{"filter.a.process", "-c filter.a.process= -c filter.a.required=false"},
		{`filter.a.b.clean`, "-c filter.a.b.clean= -c filter.a.b.required=false"},
		{"diff.a.textconv", "-c diff.a.textconv="},
		{"diff.a.command", "-c diff.a.command="},
		{"diff.external", "-c diff.external="},
		{"merge.a.driver", "-c merge.a.driver="},
		{"trailer.a.command", "-c trailer.a.command="},
		{"trailer.a.cmd", "-c trailer.a.cmd="},
		{"gpg.program", "-c gpg.program="},
		{"gpg.ssh.program", "-c gpg.ssh.program="},
		{"gpg.ssh.defaultKeyCommand", "-c gpg.ssh.defaultKeyCommand="},
		{"credential.helper", "-c credential.helper="},
		{"credential.https://x.helper", "-c credential.https://x.helper="},
		{"remote.origin.uploadpack", "-c remote.origin.uploadpack="},
		{"remote.origin.receivepack", "-c remote.origin.receivepack="},
		{"uploadpack.packObjectsHook", "-c uploadpack.packObjectsHook="},
		{"receivepack.shallow", "-c receivepack.shallow="},
		{"protocol.ext.allow", "-c protocol.ext.allow=never"},
		{"core.gitProxy", "-c protocol.git.allow=never"},
		{"core.sshCommand", "-c core.sshCommand="},
		{"core.editor", "-c core.editor="},
		{"core.pager", "-c core.pager="},
		{"core.askPass", "-c core.askPass="},
		{"core.fsmonitor", "-c core.fsmonitor="},
		{"core.hooksPath", "-c core.hooksPath="},
		{"core.alternateRefsCommand", "-c core.alternateRefsCommand="},
		{"sequence.editor", "-c sequence.editor="},
		{"commit.gpgSign", "-c commit.gpgSign="},
		{"tag.gpgSign", "-c tag.gpgSign="},
		{"sendemail.sendmailCmd", "-c sendemail.sendmailCmd="},
		{"user.name", ""},
		{"branch.main.remote", ""},
		{"core", ""},
	}
	for _, c := range cases {
		if got := strings.Join(neutralize(c.name), " "); got != c.want {
			t.Errorf("neutralize(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestSubsectionOfHasNoSubsection: a name with fewer than three parts has no subsection, which
// neutralize never reaches (it calls subsectionOf only for filter.<x>.<verb>), so it is checked
// here on its own.
func TestSubsectionOfHasNoSubsection(t *testing.T) {
	if got := subsectionOf("core.editor"); got != "" {
		t.Errorf("subsectionOf(two parts) = %q, want empty", got)
	}
	if got := subsectionOf("single"); got != "" {
		t.Errorf("subsectionOf(one part) = %q, want empty", got)
	}
}

// TestNeutralizeLocalConfigReadsEveryKey: the overrides come from the repository's local config,
// expanded (an include brings its own keys into the listing), and nothing from a dir that is not
// a repository.
func TestNeutralizeLocalConfigReadsEveryKey(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	git(t, repo, "config", "filter.a.clean", "x")
	over := strings.Join(neutralizeLocalConfig(ctx, repo), " ")
	if !strings.Contains(over, "-c filter.a.clean=") {
		t.Errorf("local override missing the filter: %q", over)
	}
	if got := neutralizeLocalConfig(ctx, t.TempDir()); got != nil {
		t.Errorf("a non-repository contributes no override, got %q", got)
	}
}

// TestHardeningForNeutralizesTheRepository: the exported form for callers outside the package
// carries the fixed hardening and the repository's own overrides.
func TestHardeningForNeutralizesTheRepository(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	git(t, repo, "config", "filter.a.clean", "x")
	got := strings.Join(HardeningFor(ctx, repo), " ")
	if !strings.Contains(got, "core.hooksPath=/dev/null") || !strings.Contains(got, "-c filter.a.clean=") {
		t.Errorf("HardeningFor = %q", got)
	}
}

// Hardening hands out a copy: a caller that appends to it cannot change what this package runs.
func TestHardeningIsACopy(t *testing.T) {
	h := Hardening()
	if strings.Join(h, " ") != "-c core.hooksPath=/dev/null -c core.fsmonitor=false -c protocol.ext.allow=never" {
		t.Fatalf("hardening: %q", h)
	}
	h[1] = "core.hooksPath=hooks"
	if Hardening()[1] != "core.hooksPath=/dev/null" {
		t.Error("the package's hardening changed through a copy")
	}
}

// TestNeutralizeSeesIncludesAndWorktreeConfig: --local would hide a driver that .git/config pulls
// in through [include] or that config.worktree defines, while git still applies it. Both must be
// neutralized, and a machine-owner scope must not be.
func TestNeutralizeSeesIncludesAndWorktreeConfig(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	extra := filepath.Join(t.TempDir(), "x.cfg")
	write(t, extra, "[filter \"inc\"]\n\tclean = true\n")
	git(t, repo, "config", "include.path", extra)
	git(t, repo, "config", "extensions.worktreeConfig", "true")
	git(t, repo, "config", "--worktree", "diff.wt.textconv", "true")
	over := strings.Join(neutralizeLocalConfig(ctx, repo), " ")
	for _, want := range []string{"-c filter.inc.clean=", "-c diff.wt.textconv="} {
		if !strings.Contains(over, want) {
			t.Errorf("override %q missing from %q", want, over)
		}
	}
}

// TestAFilterNamedWithAnEqualsSignRunsNoCode: git splits `-c name=value` at the first "=", so
// filter."a=b".clean cannot be emptied with an override. Git must refuse to run in such a
// repository instead of running the planted command.
func TestAFilterNamedWithAnEqualsSignRunsNoCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted command is a shell script")
	}
	ctx := context.Background()
	repo := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	write(t, filepath.Join(repo, ".gitattributes"), "* filter=a=b\n")
	write(t, filepath.Join(repo, ".git", "config"), readFile(t, filepath.Join(repo, ".git", "config"))+
		"[filter \"a=b\"]\n\tclean = touch "+marker+"; cat\n")
	write(t, filepath.Join(repo, "f.txt"), "x\n")
	_, _ = executeWithin(ctx, gitTimeout, repo, "add", "f.txt")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the planted filter ran")
	}
	if got := neutralize("filter.a=b.clean"); len(got) != 1 || !strings.HasPrefix(got[0], "--git-dir=") {
		t.Errorf("neutralize = %q, want the fail-closed override", got)
	}
	if got := neutralize("user.name=x"); got != nil {
		t.Errorf("a harmless key got an override: %q", got)
	}
}

// TestAProtocolPolicyCannotArmTheExtTransport: a repository that sets protocol.allow=always (not
// protocol.ext.allow, which neutralize overrides by name) turns on the ext:: transport, and an
// ext:: origin is a command git runs on fetch. The fixed hardening forbids ext by name, which
// beats any general policy.
func TestAProtocolPolicyCannotArmTheExtTransport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted transport is a shell command")
	}
	repo := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	git(t, repo, "config", "protocol.allow", "always")
	git(t, repo, "remote", "add", "origin", "ext::sh -c touch% "+marker)
	_ = PullFastForward(context.Background(), repo, "main")
	if exists(marker) {
		t.Error("an ext:: origin ran a command on fetch")
	}
}

// TestAlternateRefsCommandRunsNoCode: core.alternateRefsCommand is a command git runs on fetch
// in a repository that borrows objects from another one (objects/info/alternates).
func TestAlternateRefsCommandRunsNoCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted command is a shell command")
	}
	repo := newRepo(t)
	remote := pushToOrigin(t, repo)
	clone := filepath.Join(t.TempDir(), "clone")
	git(t, repo, "clone", "-q", remote, clone)
	other := filepath.Join(t.TempDir(), "other")
	git(t, repo, "clone", "-q", remote, other)
	write(t, filepath.Join(clone, ".git", "objects", "info", "alternates"), filepath.Join(other, ".git", "objects")+"\n")
	marker := filepath.Join(t.TempDir(), "ran")
	git(t, clone, "config", "core.alternateRefsCommand", "touch "+marker+"; true")
	write(t, filepath.Join(repo, "new.txt"), "new\n")
	git(t, repo, "add", "new.txt")
	git(t, repo, "commit", "-qm", "new")
	git(t, repo, "push", "-q", "origin", "main")

	if err := PullFastForward(context.Background(), clone, "main"); err != nil {
		t.Fatal(err)
	}
	if exists(marker) {
		t.Error("core.alternateRefsCommand ran on fetch")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
