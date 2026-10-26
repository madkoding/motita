package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uploadCall(srv *Server, id, query string, body []byte) *httptest.ResponseRecorder {
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/sessions/"+id+"/attachments"+query, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestAttachmentIsKeptInTheWorkspaceUnderASafeUniqueName(t *testing.T) {
	srv, c, ws, _ := artifactServer(t, nil)
	c.workspace = ws

	w := uploadCall(srv, c.id, "?name=../../shot.PNG", []byte("png"))
	var v attachmentView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}
	if v.Name != "shot.PNG" || v.Path != ".motita/uploads/shot.PNG" || v.Kind != "image" || v.Size != 3 {
		t.Fatalf("view = %+v", v)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, ".motita", "uploads", "shot.PNG")); string(b) != "png" {
		t.Fatalf("file = %q", b)
	}

	// The same name again does not overwrite.
	json.Unmarshal(uploadCall(srv, c.id, "?name=shot.PNG", []byte("two")).Body.Bytes(), &v)
	if v.Name != "shot-2.PNG" {
		t.Fatalf("second name = %q", v.Name)
	}
}

func TestAttachmentRefusals(t *testing.T) {
	srv, c, ws, _ := artifactServer(t, nil)
	c.workspace = ws
	if w := uploadCall(srv, c.id, "?name=", []byte("x")); w.Code != http.StatusBadRequest {
		t.Fatalf("no name = %d", w.Code)
	}
	if w := uploadCall(srv, c.id, "?name=.hidden", []byte("x")); w.Code != http.StatusCreated {
		t.Fatalf("a leading dot is stripped, got %d", w.Code)
	}
	if w := uploadCall(srv, c.id, "?name=empty.txt", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("empty = %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(ws, ".motita", "uploads", "empty.txt")); err == nil {
		t.Fatal("an empty upload left a file behind")
	}
	dir := filepath.Join(ws, ".motita", "uploads")
	for i := 0; i < maxAttachments; i++ {
		_ = os.WriteFile(filepath.Join(dir, "f"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26))), []byte("x"), 0o644)
	}
	if w := uploadCall(srv, c.id, "?name=more.txt", []byte("x")); w.Code != http.StatusConflict {
		t.Fatalf("full = %d", w.Code)
	}
}

func TestAttachmentNameAndKind(t *testing.T) {
	long := strings.Repeat("é", 200) + ".mp4"
	got := safeAttachmentName(long)
	if r := []rune(got); len(r) != 120 || !strings.HasSuffix(got, ".mp4") {
		t.Fatalf("long name = %q", got)
	}
	if got := safeAttachmentName(`a:b*c\d.pdf`); got != "d.pdf" {
		t.Fatalf("backslash path = %q", got)
	}
	if got := safeAttachmentName("a:b*c.pdf"); got != "a_b_c.pdf" {
		t.Fatalf("unsafe chars = %q", got)
	}
	for name, kind := range map[string]string{"a.MOV": "video", "a.pdf": "document", "a.flac": "audio", "a.go": "file"} {
		if attachmentKind(name) != kind {
			t.Errorf("%s -> %s", name, attachmentKind(name))
		}
	}
}

func TestAttachmentOverTheSizeLimitIsRemoved(t *testing.T) {
	srv, c, ws, _ := artifactServer(t, nil)
	c.workspace = ws
	old := maxAttachmentBytes
	maxAttachmentBytes = 4
	defer func() { maxAttachmentBytes = old }()
	if w := uploadCall(srv, c.id, "?name=big.mp4", []byte("12345678")); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big = %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(ws, ".motita", "uploads", "big.mp4")); err == nil {
		t.Fatal("a refused upload left a file behind")
	}
}

func TestAttachmentOfASessionWithoutWorkspaceGoesToTheWorkingDirectory(t *testing.T) {
	srv, c, _, _ := artifactServer(t, nil)
	wd := t.TempDir()
	t.Chdir(wd)
	if w := uploadCall(srv, c.id, "?name=a.txt", []byte("x")); w.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(wd, ".motita", "uploads", "a.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentFolderThatIsAFileIsRefused(t *testing.T) {
	srv, c, ws, _ := artifactServer(t, nil)
	c.workspace = ws
	_ = os.RemoveAll(filepath.Join(ws, ".motita"))
	_ = os.WriteFile(filepath.Join(ws, ".motita"), []byte("x"), 0o644)
	if w := uploadCall(srv, c.id, "?name=a.txt", []byte("x")); w.Code != http.StatusInternalServerError {
		t.Fatalf("blocked folder = %d", w.Code)
	}
}

func TestAttachmentNameThatIsNotAPlainNameIsRefused(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, _, err := createAttachment(root, "../x"); err == nil {
		t.Fatal("a name with a separator was accepted")
	}
}
