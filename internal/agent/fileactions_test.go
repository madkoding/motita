package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

func fileAgent(t *testing.T) *fixture {
	t.Helper()
	return mount(t, idleServer(t), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWriteFileWritesTheContentAsItIs replays what the real session needed a python script for:
// a Go file with tabs and backquotes, written verbatim, in a directory that does not exist yet.
func TestWriteFileWritesTheContentAsItIs(t *testing.T) {
	e := fileAgent(t)
	body := "package x\n\nfunc f() {\n\ts := `raw \"quoted\"`\n\t_ = s\n}\n"
	out, err := e.agent.runActions(context.Background(), []Command{
		{Kind: "write_file", Description: "add f", Command: "pkg/x/f.go\n" + body},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, filepath.Join(e.dir, "pkg/x/f.go")); got != body {
		t.Errorf("the file must hold exactly the content:\n%q\nwant\n%q", got, body)
	}
	if !strings.Contains(out, "[write_file] add f") || !strings.Contains(out, "wrote pkg/x/f.go") {
		t.Errorf("the model must be told what was written: %q", out)
	}
}

// TestEditFileReplacesExactlyOneOccurrence: the edit lands where the text is, and every way it
// cannot is said in words the model can act on, without touching the file.
func TestEditFileReplacesExactlyOneOccurrence(t *testing.T) {
	e := fileAgent(t)
	path := filepath.Join(e.dir, "a.go")
	original := "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 2\n}\n// dup\n// dup\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := func(block string) string {
		t.Helper()
		out, err := e.agent.runActions(context.Background(), []Command{{Kind: "edit_file", Command: "a.go\n" + block}}, "")
		if err != nil {
			t.Fatalf("an edit that does not apply is not a refusal: %v", err)
		}
		return out
	}

	out := edit("<<<<<<< SEARCH\n\treturn 1\n=======\n\treturn 10\n>>>>>>> REPLACE\n")
	if !strings.Contains(out, "edited a.go: 1 replacement") || !strings.Contains(readBack(t, path), "\treturn 10\n") {
		t.Fatalf("the edit must apply: %q", out)
	}
	// An empty replacement deletes the text.
	edit("<<<<<<< SEARCH\nfunc b() {\n\treturn 2\n}\n=======\n>>>>>>> REPLACE")
	if strings.Contains(readBack(t, path), "func b") {
		t.Error("an empty replacement must delete the text")
	}

	before := readBack(t, path)
	for block, want := range map[string]string{
		"<<<<<<< SEARCH\n\treturn 99\n=======\nx\n>>>>>>> REPLACE": "is not in a.go",
		"<<<<<<< SEARCH\n// dup\n=======\nx\n>>>>>>> REPLACE":      "occurs 2 times",
		"replace return 10 with 11":                                "an edit_file block is the path",
		"<<<<<<< SEARCH\nno divider here\n>>>>>>> REPLACE":         "needs the current text",
		"<<<<<<< SEARCH\n\n=======\nx\n>>>>>>> REPLACE":            "needs the current text",
	} {
		if out := edit(block); !strings.Contains(out, want) {
			t.Errorf("block %q: want %q in %q", block, want, out)
		}
	}
	if readBack(t, path) != before {
		t.Error("an edit that did not apply must leave the file as it was")
	}

	out, _ = e.agent.runActions(context.Background(), []Command{{Kind: "edit_file",
		Command: "missing.go\n<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE"}}, "")
	if !strings.Contains(out, "[failed:") {
		t.Errorf("a file that does not exist must be reported: %q", out)
	}
}

// TestFileActionsStayInsideTheWorkspace: these actions write without a shell, and so without the
// shell's policy; the workspace is their boundary, and read-only mode refuses them outright.
func TestFileActionsStayInsideTheWorkspace(t *testing.T) {
	e := fileAgent(t)
	outside := filepath.Join(t.TempDir(), "escape.txt")
	for _, target := range []string{"../escape.txt", outside, "", "  "} {
		out, err := e.agent.runActions(context.Background(), []Command{{Kind: "write_file", Command: target + "\nx"}}, "")
		if err == nil || !strings.Contains(out, "[refused:") {
			t.Errorf("path %q must be refused: %q %v", target, out, err)
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("a file was written outside the workspace")
	}
	// An absolute path INSIDE the workspace is fine.
	inside := filepath.Join(e.dir, "in.txt")
	if _, err := e.agent.runActions(context.Background(), []Command{{Kind: "write_file", Command: inside + "\nok"}}, ""); err != nil {
		t.Fatal(err)
	}
	if readBack(t, inside) != "ok" {
		t.Error("an absolute path inside the workspace must be written")
	}

	e.agent.cfg.Agent.ReadOnly = true
	out, err := e.agent.runActions(context.Background(), []Command{{Kind: "edit_file", Command: "in.txt\nx"}}, "")
	if err == nil || !strings.Contains(out, "read-only") {
		t.Errorf("read-only mode must refuse a file action: %q %v", out, err)
	}
}

// TestFileActionFailuresOnDisk: a directory that cannot be made, and a file that cannot be
// written, are reported to the model - they are facts about the disk, not refusals.
func TestFileActionFailuresOnDisk(t *testing.T) {
	e := fileAgent(t)
	if err := os.WriteFile(filepath.Join(e.dir, "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Command{
		{Kind: "write_file", Command: "plain/child.txt\nx"}, // parent is a file
		{Kind: "write_file", Command: "adir\nx"},            // target is a directory
	} {
		out, err := e.agent.runActions(context.Background(), []Command{c}, "")
		if err != nil || !strings.Contains(out, "[failed:") {
			t.Errorf("%q: want a reported failure, got %q %v", c.Command, out, err)
		}
	}
	// A file that can be read but not written back.
	ro := filepath.Join(e.dir, "ro.txt")
	if err := os.WriteFile(ro, []byte("a\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		out, _ := e.agent.runActions(context.Background(), []Command{{Kind: "edit_file",
			Command: "ro.txt\n<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE"}}, "")
		if !strings.Contains(out, "[failed:") {
			t.Errorf("a file that cannot be written must be reported: %q", out)
		}
	}
	// A workspace whose path cannot be resolved: os.Getwd fails once the current directory is
	// gone, and a relative workspace then has no absolute form.
	e.agent.cfg.Agent.WorkspaceDir = "rel"
	gone := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(gone); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	os.Remove(gone)
	if _, err := e.agent.workspacePath("x"); err == nil {
		t.Error("a workspace with no absolute path must be an error")
	}
}

// TestAFileActionCountsAsAWriteInTheLoop: in a real round the write is executed work (not a
// recall-only round) and a kept read of the file is dropped, so the next round never reads a
// stale copy from memory.
func TestAFileActionCountsAsAWriteInTheLoop(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch round {
		case 1:
			return step(false, "printf 'v1\\n' > f.txt", "cat f.txt")
		case 2:
			return mustJSON(map[string]any{"reasoning": "edit", "done": false, "final_action": map[string]string{"command": ""},
				"actions": []map[string]string{{"kind": "edit_file", "description": "bump",
					"command": "f.txt\n<<<<<<< SEARCH\nv1\n=======\nv2\n>>>>>>> REPLACE"}}})
		case 3:
			return step(false, "cat f.txt")
		}
		return step(true)
	}}
	e, result, err := runScript(t, s, "grep -q v2 f.txt", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if !strings.Contains(s.executes[2], "edited f.txt: 1 replacement") {
		t.Errorf("round 3 must see the edit's result:\n%s", s.executes[2])
	}
	if strings.Contains(s.executes[3], "not run again") {
		t.Errorf("the read after the edit must run again, not come from memory:\n%s", s.executes[3])
	}
	if readBack(t, filepath.Join(e.dir, "f.txt")) != "v2\n" {
		t.Error("the edit must be on disk")
	}
}
