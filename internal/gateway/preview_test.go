package gateway

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildChangeReportShowsPicturesAndFiles(t *testing.T) {
	ws := t.TempDir()
	gitIn(t, ws, "init", "-q", "-b", "main")
	gitIn(t, ws, "config", "user.email", "t@e.x")
	gitIn(t, ws, "config", "user.name", "T")
	put(t, filepath.Join(ws, "a.txt"), "a\n")
	gitIn(t, ws, "add", ".")
	gitIn(t, ws, "commit", "-qm", "init")
	rev := revOf(t, ws)

	put(t, filepath.Join(ws, "a.txt"), "a\nb\n")
	put(t, filepath.Join(ws, "logo.png"), "PNGDATA")
	put(t, filepath.Join(ws, PreviewDir, "after.png"), "SHOT")
	put(t, filepath.Join(ws, PreviewDir, "notes.txt"), "not an image")
	put(t, filepath.Join(ws, PreviewDir, "empty.png"), "")

	rep := buildChangeReport(context.Background(), ws, rev)
	for _, f := range rep.Files {
		if strings.HasPrefix(f.Path, PreviewDir) {
			t.Errorf("a screenshot is evidence, not a change: %v", f)
		}
	}
	if len(rep.Files) != 2 {
		t.Errorf("files = %+v", rep.Files)
	}
	if len(rep.Previews) != 2 || rep.Previews[0].Name != "after.png" || rep.Previews[1].Name != "logo.png" {
		t.Fatalf("previews = %+v", rep.Previews)
	}
	if !strings.HasPrefix(rep.Previews[0].URL, "data:image/png;base64,") {
		t.Errorf("url = %q", rep.Previews[0].URL)
	}

	clearPreviews(ws)
	if _, err := os.Stat(filepath.Join(ws, PreviewDir)); !os.IsNotExist(err) {
		t.Error("clearPreviews left the folder")
	}
}

func revOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestBuildChangeReportDegrades(t *testing.T) {
	if r := buildChangeReport(context.Background(), "", ""); len(r.Files)+len(r.Previews) != 0 {
		t.Errorf("no workspace = %+v", r)
	}
	clearPreviews("")
	// Not a repository: no files, but the screenshots the agent left still show.
	ws := t.TempDir()
	put(t, filepath.Join(ws, PreviewDir, "x.jpg"), "J")
	r := buildChangeReport(context.Background(), ws, "")
	if len(r.Files) != 0 || len(r.Previews) != 1 || !strings.Contains(r.Previews[0].URL, "image/jpeg") {
		t.Errorf("non-repo = %+v", r)
	}
	// An unknown extension falls back to png; the cap and the size limit apply.
	dir := filepath.Join(ws, PreviewDir)
	for i := 0; i < maxPreviews+2; i++ {
		put(t, filepath.Join(dir, "p"+string(rune('a'+i))+".png"), "P")
	}
	if r := buildChangeReport(context.Background(), ws, ""); len(r.Previews) != maxPreviews {
		t.Errorf("previews = %d, want cap %d", len(r.Previews), maxPreviews)
	}
	if _, ok := readPreview(filepath.Join(ws, "missing.png")); ok {
		t.Error("missing file accepted")
	}
	if _, ok := readPreview(dir); ok {
		t.Error("directory accepted")
	}
	big := filepath.Join(ws, "big.png")
	put(t, big, strings.Repeat("x", maxPreviewBytes+1))
	if _, ok := readPreview(big); ok {
		t.Error("oversized file accepted")
	}
	noext := filepath.Join(ws, "noext")
	put(t, noext, "N")
	if ev, ok := readPreview(noext); !ok || !strings.HasPrefix(ev.URL, "data:image/png") {
		t.Errorf("noext = %+v %v", ev, ok)
	}
	unread := filepath.Join(ws, "unread.png")
	put(t, unread, "U")
	_ = os.Chmod(unread, 0)
	if os.Geteuid() != 0 {
		if _, ok := readPreview(unread); ok {
			t.Error("unreadable file accepted")
		}
	}
}

// A run that changes files ends with a `done` that carries them - and one that changes nothing
// carries no `changes` at all.
func TestDoneCarriesTheChangesTheRunMade(t *testing.T) {
	ws := t.TempDir()
	gitIn(t, ws, "init", "-q", "-b", "main")
	gitIn(t, ws, "config", "user.email", "t@e.x")
	gitIn(t, ws, "config", "user.name", "T")
	put(t, filepath.Join(ws, "a.txt"), "a\n")
	gitIn(t, ws, "add", ".")
	gitIn(t, ws, "commit", "-qm", "init")

	for _, edit := range []bool{true, false} {
		svc := &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
			if edit {
				put(t, filepath.Join(ws, "b.txt"), "b\n")
				put(t, filepath.Join(ws, PreviewDir, "s.png"), "SHOT")
			}
			return "done it", nil
		}}
		srv := newTestServer(t, svc)
		c := &conversation{id: "c", svc: svc, workspace: ws}
		rn, ok := srv.startDetachedRun(c, "t", "task", srv.approverFactory(c))
		if !ok {
			t.Fatal("run did not start")
		}
		<-rn.done
		evs, _ := rn.since(0)
		var done doneEvent
		for _, e := range evs {
			if e.Event == EventDone {
				if err := json.Unmarshal(e.Data, &done); err != nil {
					t.Fatal(err)
				}
			}
		}
		if edit && (done.Changes == nil || len(done.Changes.Files) != 1 || len(done.Changes.Previews) != 1) {
			t.Errorf("edit: changes = %+v", done.Changes)
		}
		if !edit && done.Changes != nil {
			t.Errorf("no edit: changes = %+v", done.Changes)
		}
		os.Remove(filepath.Join(ws, "b.txt"))
	}
}

func TestBuildChangeReportSurvivesAGitFailure(t *testing.T) {
	ws := t.TempDir()
	put(t, filepath.Join(ws, ".git", "HEAD"), "garbage")
	put(t, filepath.Join(ws, PreviewDir, "x.png"), "S")
	r := buildChangeReport(context.Background(), ws, "deadbeef")
	if len(r.Previews) != 1 {
		t.Errorf("previews = %+v", r.Previews)
	}
}
