package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func folderPath(p string) string { return "/v1/notes/folder?path=" + url.QueryEscape(p) }

func TestTheListSaysWhichFoldersAScanWrites(t *testing.T) {
	srv, _ := archServer(t, nil)
	w := noteCall(t, srv, http.MethodGet, "/v1/notes", "")
	for _, want := range []string{
		`{"path":"Projects","kind":"projects"}`,
		`{"path":"Sessions","kind":"sessions"}`,
		`{"path":"Projects/App - Manual","kind":"guide","project":"p1aaaaaaaaaa","title":"App"}`,
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("the list lacks %s:\n%s", want, w.Body)
		}
	}
}

func TestScanFoldersWithoutProjects(t *testing.T) {
	srv, _ := noteServer(t)
	srv.projects = nil
	if got := srv.scanFolders(); len(got) != 2 {
		t.Errorf("no projects, two folders: %+v", got)
	}
	srv, _, ws := scanServer(t, nil)
	// A project folder that cannot be read leaves only the fixed folders.
	if err := os.RemoveAll(filepath.Join(ws, "projects")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ws, "projects"), nil, 0o644)
	if got := srv.scanFolders(); len(got) != 2 {
		t.Errorf("unreadable projects, two folders: %+v", got)
	}
}

func TestADeletedGuideIsGoneAndTheNextScanWritesItAgain(t *testing.T) {
	srv, dir := archServer(t, nil)
	scanWith(t, srv, "")
	w := noteCall(t, srv, http.MethodDelete, folderPath("Projects/App - Manual"), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "Projects", "App - Manual")); err == nil {
		t.Error("the guide's folder is still there")
	}
	if _, err := os.Stat(filepath.Join(dir, "Projects", "App.md")); err != nil {
		t.Errorf("the project's own note went too: %v", err)
	}
	if got := scanWith(t, srv, ""); got.Documented == 0 {
		t.Errorf("the next scan did not write the guide: %+v", got)
	}
}

func TestDeletingAFolderAnswersWhatWentWrong(t *testing.T) {
	srv, _ := noteServer(t)
	if w := noteCall(t, srv, http.MethodDelete, folderPath("../x"), ""); w.Code != http.StatusBadRequest {
		t.Errorf("a bad path: %d", w.Code)
	}
	if w := noteCall(t, srv, http.MethodDelete, folderPath("Nope"), ""); w.Code != http.StatusNotFound {
		t.Errorf("a missing folder: %d", w.Code)
	}
	off := newTestServer(t, &fakeService{})
	if w := noteCall(t, off, http.MethodDelete, folderPath("A"), ""); w.Code != http.StatusNotImplemented {
		t.Errorf("no vault: %d", w.Code)
	}
	if w := noteCall(t, off, http.MethodPost, "/v1/notes/folder/rescan?path=A", ""); w.Code != http.StatusNotImplemented {
		t.Errorf("no vault: %d", w.Code)
	}
}

func TestRescanningAGuideWritesThatProjectAgain(t *testing.T) {
	srv, dir := archServer(t, nil)
	scanWith(t, srv, "")
	guide := filepath.Join(dir, "Projects", "App - Manual", "Start here.md")
	os.WriteFile(guide, []byte("# App manual\n"), 0o644)
	w := noteCall(t, srv, http.MethodPost, "/v1/notes/folder/rescan?path="+url.QueryEscape("Projects/App - Manual"), "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("rescan: %d %s", w.Code, w.Body)
	}
	if p := waitScan(t, srv); p.Result == nil || p.Result.Projects != 1 {
		t.Errorf("the project was not scanned: %+v", p)
	}
	if body := readNote(t, dir, "Projects/App - Manual/Start here.md"); !strings.Contains(body, "What this project is") {
		t.Errorf("the guide was not written again:\n%s", body)
	}
	// The folders of every project and every session are written again for all of them.
	if w := noteCall(t, srv, http.MethodPost, "/v1/notes/folder/rescan?path=Projects", ""); w.Code != http.StatusAccepted {
		t.Errorf("rescan Projects: %d %s", w.Code, w.Body)
	}
	waitScan(t, srv)
	// A folder nobody scans has nothing to write again.
	if w := noteCall(t, srv, http.MethodPost, "/v1/notes/folder/rescan?path=Mine", ""); w.Code != http.StatusBadRequest {
		t.Errorf("a folder of mine: %d", w.Code)
	}
}
