package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// artifactRunner is a Runner that can browse artifacts: the list and the content of each file.
type artifactRunner struct {
	*fakeRunner
	list    []ArtifactInfo
	files   map[string][]byte
	listErr error
	readErr error
}

func (r *artifactRunner) ListArtifacts(context.Context) ([]ArtifactInfo, error) {
	return r.list, r.listErr
}

func (r *artifactRunner) ReadArtifact(_ context.Context, name string) ([]byte, error) {
	if r.readErr != nil {
		return nil, r.readErr
	}
	return r.files[name], nil
}

func artifactTUI(r *artifactRunner) *TUI {
	if r.fakeRunner == nil {
		r.fakeRunner = &fakeRunner{}
	}
	return newFakeTUI("", r)
}

// drawn is everything the command wrote to the conversation, as text. It reads the messages
// and not the screen: the screen is a window, and a long listing scrolls its tail out of it.
func drawn(ui *TUI) string {
	var sb strings.Builder
	for _, m := range ui.messages {
		sb.WriteString(m.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestArtifactsListsTheFiles(t *testing.T) {
	ui := artifactTUI(&artifactRunner{list: []ArtifactInfo{{Name: "report.md", Type: "text/markdown", Size: 4096}, {Name: "tiny.txt", Type: "text/plain", Size: 3}}})
	ui.artifactsCommand(context.Background(), "")
	out := drawn(ui)
	if !strings.Contains(out, "report.md") || !strings.Contains(out, "4 KB") || !strings.Contains(out, "1 KB") {
		t.Fatalf("list = %s", out)
	}
}

func TestArtifactsSaysWhenThereAreNone(t *testing.T) {
	ui := artifactTUI(&artifactRunner{})
	ui.artifactsCommand(context.Background(), "")
	if !strings.Contains(drawn(ui), "Nothing saved") {
		t.Fatalf("got %s", drawn(ui))
	}
}

func TestArtifactsReportsAFailedList(t *testing.T) {
	ui := artifactTUI(&artifactRunner{listErr: errors.New("connection refused")})
	ui.artifactsCommand(context.Background(), "")
	if !strings.Contains(drawn(ui), "could not be asked") {
		t.Fatalf("got %s", drawn(ui))
	}
}

func TestArtifactsNeedsAGateway(t *testing.T) {
	ui := newFakeTUI("", &fakeRunner{})
	ui.artifactsCommand(context.Background(), "")
	if !strings.Contains(drawn(ui), "keeps artifacts") {
		t.Fatalf("got %s", drawn(ui))
	}
}

func TestArtifactsShowsATextFileAndTrimsALongOne(t *testing.T) {
	long := strings.Repeat("line\n", artifactPreviewLines+5)
	ui := artifactTUI(&artifactRunner{files: map[string][]byte{"short.txt": []byte("hello\n"), "long.txt": []byte(long)}})
	ui.artifactsCommand(context.Background(), "short.txt")
	if !strings.Contains(drawn(ui), "hello") {
		t.Fatalf("short = %s", drawn(ui))
	}
	ui = artifactTUI(&artifactRunner{files: map[string][]byte{"long.txt": []byte(long)}})
	ui.artifactsCommand(context.Background(), "long.txt")
	if !strings.Contains(drawn(ui), "5 more lines") {
		t.Fatalf("long = %s", drawn(ui))
	}
}

func TestArtifactsDoesNotPrintBinary(t *testing.T) {
	ui := artifactTUI(&artifactRunner{files: map[string][]byte{"a.bin": {0xff, 0x00, 0xfe}}})
	ui.artifactsCommand(context.Background(), "a.bin")
	if !strings.Contains(drawn(ui), "is not text") {
		t.Fatalf("got %s", drawn(ui))
	}
}

func TestArtifactsReportsAFailedRead(t *testing.T) {
	ui := artifactTUI(&artifactRunner{readErr: errors.New("gone")})
	ui.artifactsCommand(context.Background(), "a.txt")
	ui.artifactsCommand(context.Background(), "save a.txt")
	if strings.Count(drawn(ui), "gone") < 2 {
		t.Fatalf("got %s", drawn(ui))
	}
}

func TestArtifactsSaveWritesOnceAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	ui := artifactTUI(&artifactRunner{files: map[string][]byte{"report.md": []byte("# hi")}})
	ui.artifactsCommand(context.Background(), "save report.md")
	if b, _ := os.ReadFile(filepath.Join(dir, "report.md")); string(b) != "# hi" {
		t.Fatalf("file = %q", b)
	}
	ui.artifactsCommand(context.Background(), "save report.md")
	if !strings.Contains(drawn(ui), "already exists") {
		t.Fatalf("second save = %s", drawn(ui))
	}
}

func TestArtifactsSaveRefusesAPathAndReportsAWriteFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	ui := artifactTUI(&artifactRunner{files: map[string][]byte{"a.txt": []byte("x")}})
	ui.artifactsCommand(context.Background(), "save ../escape.txt")
	ui.artifactsCommand(context.Background(), "save .hidden")
	if strings.Count(drawn(ui), "plain name") < 2 {
		t.Fatalf("got %s", drawn(ui))
	}
	// A working directory that no longer exists makes the open fail for any user, with an
	// error that is not "already exists".
	gone := t.TempDir()
	t.Chdir(gone)
	_ = os.RemoveAll(gone)
	ui = artifactTUI(&artifactRunner{files: map[string][]byte{"b.txt": []byte("x")}})
	ui.artifactsCommand(context.Background(), "save b.txt")
	if strings.Contains(drawn(ui), "saved b.txt") || strings.Contains(drawn(ui), "already exists") || drawn(ui) == "" {
		t.Fatalf("got %q", drawn(ui))
	}
}

func TestArtifactsUsage(t *testing.T) {
	ui := artifactTUI(&artifactRunner{})
	ui.artifactsCommand(context.Background(), "a b c")
	if !strings.Contains(drawn(ui), "usage: /artifacts") {
		t.Fatalf("got %s", drawn(ui))
	}
}

func TestTheArtifactsCommandIsRegistered(t *testing.T) {
	ui := artifactTUI(&artifactRunner{list: []ArtifactInfo{{Name: "x.txt", Type: "text/plain", Size: 1}}})
	commandActions["/artifacts"](ui, context.Background(), "")
	if !strings.Contains(drawn(ui), "x.txt") {
		t.Fatalf("got %s", drawn(ui))
	}
}
