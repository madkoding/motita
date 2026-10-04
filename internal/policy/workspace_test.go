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
	} {
		d := Mode{Enforce: true}.DecideLine(line, dir)
		if d.Verdict != Ask || d.Rule != "leaves-workspace" {
			t.Errorf("%q: got %s/%s, want ask/leaves-workspace", line, d.Verdict, d.Rule)
		}
	}
}

func TestStayingInTheWorkspaceIsNotAQuestion(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"cd sub && ls", "cd .", "git -C . status", "make -C sub check", "git status"} {
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
