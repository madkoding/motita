//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

// userHome is a home directory with a private note, a git configuration, a credential store and a
// workspace inside it, the layout of a person who keeps their projects under ~.
func userHome(t *testing.T) (home, work string) {
	t.Helper()
	home = t.TempDir()
	work = filepath.Join(home, "work")
	os.MkdirAll(work, 0o755)
	os.MkdirAll(filepath.Join(home, ".config", "git"), 0o755)
	os.WriteFile(filepath.Join(home, "diary.txt"), []byte("PRIVATE-DIARY"), 0o644)
	os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Someone\n"), 0o644)
	os.WriteFile(filepath.Join(home, ".config", "git", "ignore"), []byte("*.log\n"), 0o644)
	os.WriteFile(filepath.Join(work, "mine.txt"), []byte("WORKSPACE-FILE"), 0o644)
	return home, work
}

func run(t *testing.T, s *Sandbox, script string) string {
	t.Helper()
	out, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", script}})
	if err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	return out
}

// Confined to the workspace, a command reads and writes its workspace (even one inside the home
// directory), reads the system and the git configuration, and cannot read nor list the rest of the
// user's files.
func TestAWorkspaceCommandCannotReadTheUsersFiles(t *testing.T) {
	needLandlock(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	home, work := userHome(t)
	s, err := New(Options{Dir: work, ConfineWrites: true, GitHome: home})
	if err != nil {
		t.Fatal(err)
	}
	out := run(t, s, "cat "+filepath.Join(home, "diary.txt")+"; ls "+home+" && echo LISTED; cat mine.txt; "+
		"echo new > new.txt && cat new.txt; cat "+filepath.Join(home, ".gitconfig")+"; cat "+
		filepath.Join(home, ".config", "git", "ignore")+"; head -c 4 /bin/sh >/dev/null && echo SYSTEM")
	if strings.Contains(out, "PRIVATE-DIARY") || strings.Contains(out, "LISTED") {
		t.Errorf("a workspace command read the user's files: %q", out)
	}
	for _, want := range []string{"WORKSPACE-FILE", "new", "Someone", "*.log", "SYSTEM"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q must stay readable: %q", want, out)
		}
	}
	if !contains(s.Isolation(), ModeReadConfine) || s.FullDisk() {
		t.Errorf("the read confinement must be reported: %v", s.Isolation())
	}
}

// On the whole disk a command reads the user's files and writes outside the workspace; motita's own
// logins stay out of reach.
func TestAFullDiskCommandReachesEverythingButMotitasSecrets(t *testing.T) {
	needLandlock(t)
	home, work := userHome(t)
	_, auth, _ := motitaHome(t)
	other := t.TempDir()
	s, err := New(Options{Dir: work, ConfineWrites: true, GitHome: home, HiddenPaths: []string{auth}, FullDisk: true})
	if err != nil {
		t.Fatal(err)
	}
	out := run(t, s, "cat "+filepath.Join(home, "diary.txt")+"; echo x > "+filepath.Join(other, "out.txt")+
		" && echo WROTE; cat "+filepath.Join(auth, "git-github.json"))
	if !strings.Contains(out, "PRIVATE-DIARY") || !strings.Contains(out, "WROTE") {
		t.Errorf("the whole disk must be reachable: %q", out)
	}
	if strings.Contains(out, "TOKEN-IN-AUTH") {
		t.Errorf("motita's logins must stay hidden on the whole disk: %q", out)
	}
	if contains(s.Isolation(), ModeWriteConfine) || contains(s.Isolation(), ModeReadConfine) {
		t.Errorf("no confinement may be claimed on the whole disk: %v", s.Isolation())
	}
}

// The setting changes while the sandbox lives, for it and for the copies its background agents run in.
func TestSetFullDiskAppliesToTheNextCommandAndToCopies(t *testing.T) {
	needLandlock(t)
	home, work := userHome(t)
	s, err := New(Options{Dir: work, ConfineWrites: true, GitHome: home})
	if err != nil {
		t.Fatal(err)
	}
	copied := s.WithDir(work)
	read := "cat " + filepath.Join(home, "diary.txt")
	if strings.Contains(run(t, copied, read), "PRIVATE-DIARY") {
		t.Fatal("confined before the change")
	}
	s.SetFullDisk(true)
	if !copied.FullDisk() || !strings.Contains(run(t, copied, read), "PRIVATE-DIARY") {
		t.Error("a copy must follow the change")
	}
	s.SetFullDisk(false)
	if strings.Contains(run(t, s, read), "PRIVATE-DIARY") {
		t.Error("going back to the workspace must confine the next command")
	}
}

// git reads the repository a worktree belongs to (its config, HEAD) on every command: with both
// inside the home directory, the worktree still works.
func TestAWorktreeInsideThePrivateHomeStillRunsGit(t *testing.T) {
	needLandlock(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	main, work := filepath.Join(home, "repo"), filepath.Join(home, "wt")
	git := func(dir string, args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=t",
			"-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(main, 0o755)
	git(main, "init", "-q", "-b", "main")
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	git(main, "worktree", "add", "-q", work, "-b", "feature")
	s, err := New(Options{Dir: work, ConfineWrites: true, GitHome: home})
	if err != nil {
		t.Fatal(err)
	}
	out, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c",
		"echo x > f.txt && git add f.txt && git -c commit.gpgsign=false -c user.name=t -c user.email=t@t " +
			"commit -q -m work && git log --oneline | head -1"}})
	if err != nil || exit != 0 || !strings.Contains(out, "work") {
		t.Fatalf("exit=%d err=%v out=%q", exit, err, out)
	}
}

func TestReadRootsNameTheToolsTheGitConfigurationAndTheRepository(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	_, own, base := fakeWorktree(t)
	_, otherOwn, other := fakeWorktree(t)
	tools := t.TempDir()
	s, err := New(Options{Dir: base, ToolsDir: tools, GitHome: "/home/u"})
	if err != nil {
		t.Fatal(err)
	}
	roots := s.readRoots(other)
	got := strings.Join(roots, " ")
	// The repository is its checkout: <checkout>/.git/worktrees/<id>.
	checkout := func(own string) string { return filepath.Dir(filepath.Dir(filepath.Dir(own))) }
	for _, want := range []string{filepath.Join(tools, "bin"), filepath.Join(tools, "tools"), "/home/u/.gitconfig",
		"/home/u/.config/git/config", "/home/u/.config/git/ignore", "/xdg/git/attributes", checkout(own),
		checkout(otherOwn)} {
		if !slices.Contains(roots, want) {
			t.Errorf("%s is missing from %s", want, got)
		}
	}
	if slices.Contains(roots, tools) {
		t.Errorf("the tools directory as a whole holds every project's HOME: %s", got)
	}
	if strings.Contains(got, "credentials") {
		t.Errorf("the credential store must never be granted: %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	bare := &Sandbox{op: Options{}}
	if roots := bare.readRoots(t.TempDir()); len(roots) != 0 {
		t.Errorf("nothing to grant without tools, home or repository: %v", roots)
	}
}

// The tools directory and the roots the sandbox was given are private too, never "/" or a
// relative path.
func TestPrivatePathsAddTheToolsAndTheGivenRoots(t *testing.T) {
	tools := t.TempDir()
	s := &Sandbox{op: Options{ToolsDir: tools, PrivateRoots: []string{"/srv/motita", "/", "rel", ""}}}
	got := s.privatePaths()[len(privateTrees):]
	if !slices.Equal(got, []string{tools, "/srv/motita"}) {
		t.Errorf("private paths: %v", got)
	}
}

func TestPrivatePathsAddTheUsersHome(t *testing.T) {
	for home, extra := range map[string]bool{"/data/me": true, "/": false, "relative": false, "": false} {
		s := &Sandbox{op: Options{GitHome: home}}
		got := s.privatePaths()
		if (len(got) == len(privateTrees)+1) != extra || (extra && got[len(got)-1] != home) {
			t.Errorf("home %q: %v", home, got)
		}
	}
}

// A private tree whose surroundings cannot be worked out stops the command instead of letting it
// read what the person kept from it.
func TestAPrivateTreeThatCannotBeConfinedStopsTheCommand(t *testing.T) {
	needLandlock(t)
	err := confine(Spec{WriteRoots: []string{t.TempDir()}, PrivatePaths: []string{"/"}})
	if err == nil || !strings.Contains(err.Error(), "stays readable") {
		t.Errorf("got %v", err)
	}
}

func TestConfinementAvailableFollowsTheKernel(t *testing.T) {
	if ConfinementAvailable() != (landlockABI() > 0) {
		t.Error("ConfinementAvailable must say what the kernel offers")
	}
}

// Confined to the workspace the ruleset also handles listing, and grants reads on the directories
// the command writes and on the read roots, beside everything around the private trees.
func TestConfineGrantsTheWorkspaceReadsWhenTheUsersFilesArePrivate(t *testing.T) {
	needLandlock(t)
	home, work := userHome(t)
	real := landlockSyscall
	t.Cleanup(func() { landlockSyscall = real })
	// home sits in the shared temporary directory, where other packages' tests add and remove
	// entries while this one runs: the directories above the test's own one list as empty, so
	// what is granted around home is only the sibling made here.
	sibling := t.TempDir()
	root := filepath.Dir(home)
	realReadDir := readDir
	t.Cleanup(func() { readDir = realReadDir })
	readDir = func(dir string) ([]os.DirEntry, error) {
		if dir != root && !strings.HasPrefix(dir, root+"/") {
			return nil, nil
		}
		return realReadDir(dir)
	}
	rules := 0
	landlockSyscall = func(trap, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno) {
		switch {
		case trap == sysLandlockCreateRuleset && a1 == 0:
			return 3, 0, 0
		case trap == sysLandlockCreateRuleset:
			return uintptr(mustOpenDevNull(t)), 0, 0
		case trap == sysLandlockAddRule:
			rules++
		default:
			// any other call succeeds
		}
		return 0, 0, 0
	}
	gitconfig := filepath.Join(home, ".gitconfig")
	spec := Spec{WriteRoots: []string{work}, ReadRoots: []string{gitconfig}, PrivatePaths: []string{home}}
	if err := confine(spec); err != nil {
		t.Fatal(err)
	}
	around := readableAround([]string{home})
	if len(around) != 1 || around[0] != sibling {
		t.Fatalf("readable around home: %v, want [%s]", around, sibling)
	}
	// One write rule and one read rule for the workspace, one for the file, and the ones around.
	if rules != len(around)+3 {
		t.Errorf("rules=%d, want %d", rules, len(around)+3)
	}
}

// A Sandbox built by hand is confined to the workspace until it is told otherwise.
func TestAHandBuiltSandboxStartsOnTheWorkspace(t *testing.T) {
	s := &Sandbox{}
	if s.FullDisk() {
		t.Fatal("the workspace is the default")
	}
	s.SetFullDisk(true)
	if !s.FullDisk() {
		t.Error("the switch must take")
	}
}

// On the whole disk the secrets stay out of reach for WRITING as well as reading - a command could
// otherwise rewrite motita.yaml to switch off the policy - and the tools bin directories, which
// come first on every PATH, stay writable to nobody, so no command can plant a program there to
// hijack a later one. What the tools hold is still readable, so installed tools run.
func TestAFullDiskCommandCannotWriteSecretsNorTheToolsBin(t *testing.T) {
	needLandlock(t)
	home, work := userHome(t)
	secrets, auth, _ := motitaHome(t)
	secret := filepath.Join(secrets, "motita.yaml")
	os.WriteFile(secret, []byte("policy: on\n"), 0o644)
	tools := t.TempDir()
	bin := filepath.Join(tools, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho real\n"), 0o755)
	other := t.TempDir()
	s, err := New(Options{Dir: work, ConfineWrites: true, GitHome: home, ToolsDir: tools,
		HiddenPaths: []string{auth, secret}, FullDisk: true})
	if err != nil {
		t.Fatal(err)
	}
	script := "echo rewritten > " + secret + " 2>/dev/null && echo WROTE-SECRET || echo secret-safe; " +
		"echo evil > " + filepath.Join(bin, "make") + " 2>/dev/null && echo WROTE-BIN || echo bin-safe; " +
		"echo ok > " + filepath.Join(other, "out.txt") + " && echo WROTE-OTHER; " +
		"cat " + filepath.Join(bin, "git")
	out := run(t, s, script)
	if strings.Contains(out, "WROTE-SECRET") {
		t.Errorf("a full-disk command rewrote a secret: %q", out)
	}
	if strings.Contains(out, "WROTE-BIN") {
		t.Errorf("a full-disk command planted a program in the tools bin: %q", out)
	}
	for _, want := range []string{"secret-safe", "bin-safe", "WROTE-OTHER", "real"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q expected: %q", want, out)
		}
	}
}

// writeGrantRoots keeps a root that holds no protected path, and replaces one that does with what
// is around the protected paths; when there is nothing to protect the roots come back unchanged.
func TestWriteGrantRoots(t *testing.T) {
	if got := writeGrantRoots([]string{"/work"}, nil); len(got) != 1 || got[0] != "/work" {
		t.Errorf("nothing to protect leaves the roots: %v", got)
	}
	home, auth, _ := motitaHome(t)
	other := t.TempDir()
	got := writeGrantRoots([]string{other, "/"}, []string{auth})
	joined := "\n" + strings.Join(got, "\n") + "\n"
	if !strings.Contains(joined, "\n"+other+"\n") {
		t.Errorf("a root that holds no secret must be kept: %v", got)
	}
	for _, never := range []string{auth, home, "/"} {
		if strings.Contains(joined, "\n"+never+"\n") {
			t.Errorf("%s holds or is a secret and must not be a write grant: %v", never, got)
		}
	}
}

// When the directories around a protected path cannot be listed, the root is granted whole rather
// than leaving a command unable to write - the same fail-open the read side takes.
func TestWriteGrantRootsFailsOpenWhenAroundIsUnknown(t *testing.T) {
	_, auth, _ := motitaHome(t)
	prev := readDir
	readDir = func(string) ([]os.DirEntry, error) { return nil, os.ErrPermission }
	defer func() { readDir = prev }()
	got := writeGrantRoots([]string{"/"}, []string{auth})
	if len(got) != 1 || got[0] != "/" {
		t.Errorf("a root that cannot be carved must be granted whole: %v", got)
	}
}
