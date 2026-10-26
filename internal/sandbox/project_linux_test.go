//go:build linux

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/gitx"
)

// A project's sessions get a HOME of their own in the tools directory; everything else keeps the
// shared one, and a name that would leave the tools directory is not honoured.
func TestTheHomeIsTheProjectsOwn(t *testing.T) {
	if ProjectHome(" ") != "" || ProjectHome("shop-1a2b") != filepath.Join("homes", "shop-1a2b") {
		t.Fatal("ProjectHome")
	}
	var none *Sandbox
	if none.Home() != "" || (&Sandbox{}).Home() != "" {
		t.Error("no sandbox, or no tools directory, has no HOME of its own")
	}
	tools := t.TempDir()
	s := &Sandbox{op: Options{ToolsDir: tools}, base: t.TempDir()}
	if s.Home() != filepath.Join(tools, "home") {
		t.Errorf("shared home: %s", s.Home())
	}
	p := s.WithHome(ProjectHome("shop-1a2b"))
	home := filepath.Join(tools, "homes", "shop-1a2b")
	if p.Home() != home || s.Home() != filepath.Join(tools, "home") {
		t.Errorf("the copy has the project's HOME and the original keeps its own: %s %s", p.Home(), s.Home())
	}
	if !isDir(home) {
		t.Error("the project's HOME is created for the programs that write to it")
	}
	env := strings.Join(p.environmentWithTmp(t.TempDir(), false, false), "\n")
	if !strings.Contains(env, "HOME="+home+"\n") {
		t.Errorf("the commands are given the project's HOME: %s", env)
	}
	if roots := p.writeRoots(p.base, t.TempDir()); !slices.Contains(roots, home) ||
		slices.Contains(roots, filepath.Join(tools, "home")) {
		t.Errorf("the project's HOME is written, the shared one is not: %v", roots)
	}
	for _, escape := range []string{"../elsewhere", "/abs"} {
		if got := s.WithHome(escape).Home(); got != filepath.Join(tools, "home") {
			t.Errorf("%q: %s", escape, got)
		}
	}
}

// The worktrees of the project's other repositories inside a session write their git directories
// and read their repositories like the session's own worktree does; a folder there that is not a
// worktree adds nothing.
func TestTheOtherRepositoriesOfAProjectAreGranted(t *testing.T) {
	_, own, work := fakeWorktree(t)
	extra := filepath.Join(work, gitx.ReposDir, "api")
	extraOwn, _ := fakeWorktreeAt(t, t.TempDir(), extra)
	_ = extraOwn
	// A repository cloned as a plain folder of the project is granted the same way.
	direct := filepath.Join(work, "web")
	fakeWorktreeAt(t, t.TempDir(), direct)
	if err := os.MkdirAll(filepath.Join(work, gitx.ReposDir, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, gitx.ReposDir, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writes, repos := worktreeGit(work)
	if len(repos) != 3 || repos[0] != filepath.Dir(filepath.Dir(filepath.Dir(own))) {
		t.Fatalf("repositories: %v", repos)
	}
	if !slices.Contains(writes, filepath.Join(filepath.Dir(repos[1]), "main", ".git", "worktrees", "w")) &&
		!slices.Contains(writes, filepath.Join(repos[1], ".git", "worktrees", "w")) {
		t.Errorf("the other repository's worktree directory is written: %v", writes)
	}
	s := &Sandbox{op: Options{}, base: t.TempDir()}
	if roots := s.readRoots(work); !slices.Contains(roots, repos[1]) {
		t.Errorf("a command in another session's folder reads that folder's repositories: %v", roots)
	}
}

// An approved command still cannot read motita's configuration or a model's login, and reads the
// git logins its `git push` needs.
func TestAnApprovedCommandKeepsMotitasKeys(t *testing.T) {
	auth, conf := t.TempDir(), filepath.Join(t.TempDir(), "config.yaml")
	for _, f := range []string{"git-github.json", "anthropic.json"} {
		if err := os.WriteFile(filepath.Join(auth, f), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := &Sandbox{op: Options{GitAuthDir: auth + "/", HiddenPaths: []string{auth, conf}}}
	if got := s.approvedHidden(); !slices.Equal(got, []string{filepath.Join(auth, "anthropic.json"), conf}) {
		t.Errorf("approved hidden: %v", got)
	}
	s.op.GitAuthDir = ""
	if got := s.approvedHidden(); !slices.Equal(got, []string{auth, conf}) {
		t.Errorf("without a logins directory everything given stays hidden: %v", got)
	}

	needLandlock(t)
	if err := os.WriteFile(conf, []byte("llm:\n  api_key: sk-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	box, err := New(Options{Dir: t.TempDir(), ConfineWrites: true, GitAuthDir: auth, HiddenPaths: []string{auth, conf}})
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	script := "echo x > " + filepath.Join(other, "ok.txt") + "; cat " + filepath.Join(auth, "git-github.json") +
		"; cat " + conf + " " + filepath.Join(auth, "anthropic.json") + " || echo refused"
	out, _, _, err := box.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", script},
		Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(other, "ok.txt")); statErr != nil {
		t.Error("an approved command writes where it says")
	}
	if !strings.HasPrefix(out, "{}") || strings.Contains(out, "sk-secret") || !strings.Contains(out, "refused") {
		t.Errorf("approved command output: %q", out)
	}
}

// A confined command reads the toolchains, not the rest of the tools directory: another
// project's HOME is out of reach, and so is the rest of the root it was told to keep private.
func TestAConfinedCommandStaysOutOfOtherProjects(t *testing.T) {
	needLandlock(t)
	tools, root := t.TempDir(), t.TempDir()
	base := filepath.Join(root, "sessions", "a")
	for path, body := range map[string]string{
		filepath.Join(tools, "tools", "go", "bin", "tool"): "tool",
		filepath.Join(tools, "homes", "other", "secret"):   "their-home",
		filepath.Join(root, "other-project", "notes"):      "their-notes",
		filepath.Join(base, "mine"):                        "mine",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	box, err := New(Options{Dir: base, ConfineWrites: true, ToolsDir: tools, Home: ProjectHome("mine"),
		PrivateRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	script := "cat " + filepath.Join(tools, "tools", "go", "bin", "tool") + " mine; echo >> $HOME/h && echo home; " +
		"cat " + filepath.Join(tools, "homes", "other", "secret") + " " + filepath.Join(root, "other-project", "notes")
	out, _, _, err := box.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "toolmine") || !strings.Contains(out, "home") ||
		strings.Contains(out, "their-") {
		t.Errorf("confined output: %q", out)
	}
	if box.Readable(filepath.Join(root, "other-project", "notes")) || !box.Readable(filepath.Join(base, "mine")) {
		t.Error("motita's own reads follow the same rule")
	}
}

// PrivateRoots is a copy of what the sandbox was given, and nothing for no sandbox.
func TestPrivateRootsIsACopy(t *testing.T) {
	var none *Sandbox
	s := &Sandbox{op: Options{PrivateRoots: []string{"/w"}}}
	got := s.PrivateRoots()
	got[0] = "/x"
	if none.PrivateRoots() != nil || s.PrivateRoots()[0] != "/w" {
		t.Error("PrivateRoots")
	}
}
