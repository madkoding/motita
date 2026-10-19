package approvals

import (
	"errors"
	"os"
	"path/filepath"
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
