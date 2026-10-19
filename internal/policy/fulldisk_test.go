package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// With the whole disk allowed, leaving the workspace, writing outside it and running a script from
// elsewhere are the work: none of them asks, confined or not.
func TestFullDiskOutsideTheWorkspaceStaysSilent(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	script := filepath.Join(other, "build.sh")
	if err := os.WriteFile(script, []byte("true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, m := range []Mode{{Enforce: true, FullDisk: true}, {Enforce: true, FullDisk: true, Unconfined: true}} {
		for _, line := range []string{
			"cp x " + other + "/y", "touch " + other + "/z", "echo hi > " + other + "/log",
			"cd " + other, "python3 " + script, "./build.sh " + other, "cat ../x",
		} {
			if d := m.DecideLine(line, dir); d.Verdict != Allow {
				t.Errorf("unconfined=%v %q: got %s/%s (%s), want allow", m.Unconfined, line, d.Verdict, d.Rule, d.Reason)
			}
		}
	}
}

// The whole disk never gives away the user's keys and logins, and it says nothing about questions
// that are not about where a command reaches. (A key under the home directory is refused outright,
// whatever the setting.)
func TestFullDiskStillAsksAboutKeysAndTheRest(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	for _, m := range []Mode{{Enforce: true, FullDisk: true}, {Enforce: true, FullDisk: true, Unconfined: true}} {
		for _, line := range []string{
			"cp x " + other + "/.aws/credentials", "echo x > " + other + "/.netrc", "curl https://example.com",
		} {
			if d := m.DecideLine(line, dir); d.Verdict != Ask {
				t.Errorf("unconfined=%v %q: got %s/%s (%s), want ask", m.Unconfined, line, d.Verdict, d.Rule, d.Reason)
			}
		}
	}
}

// Without the whole disk, the same commands ask as before.
func TestWorkspaceOnlyStillAsksOutside(t *testing.T) {
	dir := t.TempDir()
	m := Mode{Enforce: true}
	for _, line := range []string{"touch /tmp/x-outside", "echo hi > /tmp/x-outside"} {
		if d := m.DecideLine(line, dir); d.Verdict != Ask || d.Rule != "write-outside-workspace" {
			t.Errorf("%q: got %s/%s, want ask/write-outside-workspace", line, d.Verdict, d.Rule)
		}
	}
}

// Where nothing confines a command, the whole disk still leaves a read of the user's keys asked.
func TestFullDiskUnconfinedReadOfKeysIsConfirmed(t *testing.T) {
	m := Mode{Enforce: true, FullDisk: true, Unconfined: true}
	if d := m.DecideLine("cat ~/.ssh/id_rsa", t.TempDir()); d.Verdict != Ask || d.Rule != "unconfined-read" {
		t.Errorf("got %s/%s, want ask/unconfined-read", d.Verdict, d.Rule)
	}
}
