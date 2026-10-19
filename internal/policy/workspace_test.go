package policy

import "testing"

func TestLeavingTheWorkspaceIsAsked(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"cd /etc && ls",
		"cd",
		"cd ~/other",
		"cd ..",
		"cd -",
		"pushd /tmp",
		"git -C /somewhere/else status",
		"make -C /somewhere/else check",
		// Every other way a program is pointed at another tree.
		"git -C . -C /other/project status",
		"git --git-dir=/other/project/.git log",
		"git --work-tree /other/project status",
		"make -C/other/project check",
		"make --directory=/other/project",
		"make -f /other/project/Makefile",
		"go -C /other/project build ./...",
		"npm --prefix /other/project install",
		"pnpm --dir=/other/project install",
		"yarn --cwd /other/project build",
		"cargo build --manifest-path /other/project/Cargo.toml",
		"tar -C /other/project -xf a.tar",
	} {
		d := Mode{Enforce: true}.DecideLine(line, dir)
		if d.Verdict != Ask || d.Rule != "leaves-workspace" {
			t.Errorf("%q: got %s/%s, want ask/leaves-workspace", line, d.Verdict, d.Rule)
		}
	}
}

func TestStayingInTheWorkspaceIsNotAQuestion(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"cd sub && ls", "cd .", "git -C . status", "make -C sub check", "git status",
		"git --work-tree=. status", "make -Csub", "npm --prefix . test", "go -C sub build", "tar -cf a.tar src",
		"git -C", "make --directory="} {
		if d := (Mode{Enforce: true}).DecideLine(line, dir); d.Rule == "leaves-workspace" {
			t.Errorf("%q: %+v", line, d)
		}
	}
	if _, hit := leavesWorkspace("cd", []string{"/etc"}, ""); hit {
		t.Error("with no workspace there is nothing to leave")
	}
}

func TestAMissingToolIsAskedAboutWithTheWayForward(t *testing.T) {
	have := func(name string) bool { return name != "rg" }
	m := Mode{Enforce: true, Have: have}
	d := m.DecideLine("ls && rg foo", t.TempDir())
	if d.Verdict != Ask || d.Rule != "tool-missing" {
		t.Fatalf("got %+v", d)
	}
	for _, line := range []string{"ls", "cd . && echo hi", "./rg foo", "FOO=1 ls", "echo $HOME"} {
		if d := m.DecideLine(line, t.TempDir()); d.Rule == "tool-missing" {
			t.Errorf("%q: %+v", line, d)
		}
	}
	if d := (Mode{Enforce: true}).DecideLine("rg foo", t.TempDir()); d.Rule == "tool-missing" {
		t.Errorf("without a lookup nothing can be called missing: %+v", d)
	}
}

// A line that runs a program which gains privileges when it starts is told apart, wherever in
// the line it is; one that does not is not.
func TestElevates(t *testing.T) {
	for line, want := range map[string]bool{
		"sudo apt install jq":          true,
		"make && /usr/bin/doas reboot": true,
		"echo hi;pkexec ls":            true,
		"ping -c1 example.com":         true,
		"go test ./...":                false,
		"npm install":                  false,
	} {
		if Elevates(line) != want {
			t.Errorf("%q: want %v", line, want)
		}
	}
}

// A project holds repositories in its subfolders: the commands that work inside them run without a
// question, whether the line moves there with `cd` or points git at it with `-C`.
func TestSubfolderRepositoriesRunWithoutAQuestion(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"cd sub && git status", "cd sub/deep && git commit -m 'feat: x'", "pushd sub", "git -C sub log",
		"git -C ./sub commit -m 'fix: y'", "cat sub/a.go", "ls sub",
	} {
		if d := (Mode{Enforce: true}).DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q: got %s/%s (%s), want allow", line, d.Verdict, d.Rule, d.Reason)
		}
	}
	// What leaves the machine is still asked about, wherever the repository is.
	for _, line := range []string{"cd sub && git push", "git -C sub push"} {
		if d := (Mode{Enforce: true}).DecideLine(line, dir); d.Verdict != Ask {
			t.Errorf("%q: got %s/%s, want ask", line, d.Verdict, d.Rule)
		}
	}
}

// With the whole disk allowed, git runs in any folder; the user's keys are still asked about.
func TestFullDiskRunsGitAnywhere(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	m := Mode{Enforce: true, FullDisk: true}
	for _, line := range []string{"git -C " + other + " status", "cd " + other + " && git commit -m 'feat: x'"} {
		if d := m.DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q: got %s/%s, want allow", line, d.Verdict, d.Rule)
		}
	}
	if d := m.DecideLine("git -C ~/.ssh status", dir); d.Verdict != Ask {
		t.Errorf("a path among the user's keys is still asked about: got %s/%s", d.Verdict, d.Rule)
	}
}
