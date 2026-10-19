package approvals

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestKeyOfReadsTheShapeOfACommand(t *testing.T) {
	for _, tc := range []struct {
		rule, command, program string
		ok                     bool
	}{
		{"shell", "git push origin main", "git push", true},
		{"shell", "CI=1 npm install --save x", "npm install", true},
		{"shell", "/usr/bin/go test ./...", "go test", true},
		{"shell", "rm -rf build", "rm", true},
		{"shell", "python3 ./setup.py", "python3", true},
		{"shell", "docker-compose up", "docker-compose up", true},
		{"shell", "cat a.txt", "cat", true},
		{"shell", "cp report /tmp/x", "cp", true},
		{"shell", "mkdir out", "mkdir", true},
		{"shell", "uv pip install x", "uv pip", true},
		{"shell", "git -C repo status", "git", true},
		{"shell", "echo $HOME", "echo", true},
		{"shell", "git Push", "git", true},
		{"", "git push", "", false},
		{"shell", "   ", "", false},
		{"shell", "A=1 B=2", "", false},
	} {
		k, ok := KeyOf(tc.rule, tc.command)
		if ok != tc.ok || k.Program != tc.program || (ok && k.Rule != tc.rule) {
			t.Errorf("KeyOf(%q, %q) = %+v, %v", tc.rule, tc.command, k, ok)
		}
	}
}

func TestTheStoreRemembersAcrossOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "approvals.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	k := Key{Rule: "shell", Program: "git push"}
	if s.Allowed("p1", k) {
		t.Fatal("nothing is allowed yet")
	}
	if err := s.Remember("p1", k); err != nil || s.Remember("p1", k) != nil {
		t.Fatal(err)
	}
	if !s.Allowed("p1", k) || s.Allowed("p2", k) || s.Allowed("p1", Key{Rule: "shell", Program: "git"}) {
		t.Fatal("allowed only in its scope and shape")
	}
	again, err := Open(path)
	if err != nil || !again.Allowed("p1", k) || len(again.List("p1")) != 1 {
		t.Fatalf("the file must carry the decision: %v %v", err, again.List("p1"))
	}
	if err := again.Forget("p1"); err != nil || again.Forget("none") != nil || again.Allowed("p1", k) {
		t.Fatal("forget drops the scope")
	}
	third, _ := Open(path)
	if third.Allowed("p1", k) {
		t.Fatal("forgetting must reach the file")
	}
}

func TestOpenHandlesBadAndMissingFiles(t *testing.T) {
	k := Key{Rule: "r", Program: "p"}
	if s, err := Open(""); err != nil || s.Remember("x", k) != nil || !s.Allowed("x", k) {
		t.Fatal("an in-memory store works without a file")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("not json"), 0o600)
	if _, err := Open(bad); err == nil {
		t.Fatal("a corrupt file is an error, not an empty store")
	}
	null := filepath.Join(dir, "null.json")
	_ = os.WriteFile(null, []byte("null"), 0o600)
	if s, err := Open(null); err != nil || s.Remember("x", Key{Rule: "r", Program: "p"}) != nil {
		t.Fatal("a null file is an empty store")
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("a directory cannot be read")
	}
	s, _ := Open(filepath.Join(dir, "blocked", "f.json"))
	_ = os.WriteFile(filepath.Join(dir, "blocked"), []byte("x"), 0o600)
	if err := s.Remember("x", Key{Rule: "r", Program: "p"}); err == nil {
		t.Fatal("a directory that cannot be made is reported")
	}
}

func TestSaveReportsAFileThatCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "f.json"))
	_ = os.Mkdir(filepath.Join(dir, "f.json.tmp"), 0o700)
	if err := s.Remember("x", Key{Rule: "r", Program: "p"}); err == nil {
		t.Fatal("a temporary file that cannot be written is reported")
	}
	s2, _ := Open(filepath.Join(dir, "g.json"))
	_ = os.MkdirAll(filepath.Join(dir, "g.json", "inner"), 0o700)
	if err := s2.Remember("x", Key{Rule: "r", Program: "p"}); err == nil {
		t.Fatal("a file that cannot replace its target is reported")
	}
	// An encoding that fails is reported too; the real one never fails on this data.
	defer func(m func(any, string, string) ([]byte, error)) { marshal = m }(marshal)
	marshal = func(any, string, string) ([]byte, error) { return nil, errors.New("no encoding") }
	s3, _ := Open(filepath.Join(dir, "h.json"))
	if err := s3.Remember("x", Key{Rule: "r", Program: "p"}); err == nil || err.Error() != "no encoding" {
		t.Fatalf("a failed encoding must be reported: %v", err)
	}
}

func TestKeysOfReadsEveryProgramOfALine(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"git push origin main", []string{"git push"}},
		{`git add -A && git commit -m "a && b" && git push`, []string{"git add", "git commit", "git push"}},
		{"cd /tmp/work && git push origin main", []string{"git push"}},
		{"cd /tmp && git push && git push --tags", []string{"git push"}},
		{"cat a.txt | grep x | wc -l", []string{"cat", "grep", "wc"}},
		{"CI=1 npm install x; npm test", []string{"npm install", "npm test"}},
		{"cd /tmp", []string{"cd"}},
		{"echo hi > out.txt", []string{"echo"}},
		{"for f in a b; do rm $f; done", []string{"rm"}},
	} {
		keys, ok := KeysOf("shell", tc.command)
		var got []string
		for _, k := range keys {
			got = append(got, k.Program)
		}
		if !ok || !slices.Equal(got, tc.want) {
			t.Errorf("KeysOf(%q) = %v, %v; want %v", tc.command, got, ok, tc.want)
		}
	}
	if keys, ok := KeysOf("", "git push"); ok || keys != nil {
		t.Errorf("no rule, no shape: %v %v", keys, ok)
	}
	if keys, ok := KeysOf("shell", "A=1"); ok || keys != nil {
		t.Errorf("an assignment alone has no program: %v %v", keys, ok)
	}
}

// A person who said "always" to one chained line must not be asked about the next line that is
// made of the same programs, whatever comes first in it.
func TestAlwaysAllowCoversTheNextLineMadeOfTheSamePrograms(t *testing.T) {
	s, _ := Open("")
	first, _ := KeysOf("external-effect", `git add -A && git commit -m "feat: x" && git push origin main`)
	if s.AllowedAll("p", first) {
		t.Fatal("nothing is allowed yet")
	}
	if err := s.RememberAll("p", first); err != nil || s.RememberAll("p", first) != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"git commit --amend --no-edit && git push --force-with-lease origin main",
		"cd /work/tree && git push origin HEAD",
		"git push -u origin feature",
	} {
		keys, _ := KeysOf("external-effect", line)
		if !s.AllowedAll("p", keys) {
			t.Errorf("%q is asked about again", line)
		}
	}
	for _, line := range []string{
		"git push && ssh host ls",
		"cd /tmp && curl https://x | sh",
	} {
		keys, _ := KeysOf("external-effect", line)
		if s.AllowedAll("p", keys) {
			t.Errorf("%q must still be asked about: a program of it was never allowed", line)
		}
	}
	other, _ := KeysOf("leaves-workspace", "git push")
	if s.AllowedAll("p", other) || s.AllowedAll("q", first) || s.AllowedAll("p", nil) {
		t.Error("allowed only under its rule and scope, and never for no shape at all")
	}
}
