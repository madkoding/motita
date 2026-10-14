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
