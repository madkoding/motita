package gateway

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AttachmentDir is where the files a person attaches to a message are kept, relative to the
// session's workspace: inside the agent's reach, so the message can just name the path.
const AttachmentDir = ".motita/uploads"

// maxAttachmentBytes bounds one upload. Videos are the reason it is not small; it is still a
// ceiling, because the body is written to disk as it arrives. A variable so a test need not
// send 200 MB.
var maxAttachmentBytes int64 = 200 << 20

const (
	// maxAttachments bounds how many files one session keeps, so a loop of uploads cannot fill
	// the workspace file by file.
	maxAttachments = 100
)

// attachmentKinds names what the interface and the agent's hint are told about a file.
var attachmentKinds = map[string]string{
	".png": "image", ".jpg": "image", ".jpeg": "image", ".gif": "image", ".webp": "image",
	".bmp": "image", ".svg": "image", ".heic": "image", ".avif": "image",
	".mp4": "video", ".mov": "video", ".webm": "video", ".mkv": "video", ".avi": "video", ".m4v": "video",
	".mp3": "audio", ".wav": "audio", ".m4a": "audio", ".ogg": "audio", ".flac": "audio",
	".pdf": "document", ".doc": "document", ".docx": "document", ".xls": "document", ".xlsx": "document",
	".ppt": "document", ".pptx": "document", ".odt": "document", ".rtf": "document",
}

// attachmentTypes are the pictures a browser may be shown inline. Anything else is a download.
var attachmentTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

// attachmentKind classifies a file by its extension; anything else is "file".
func attachmentKind(name string) string {
	if k, ok := attachmentKinds[strings.ToLower(filepath.Ext(name))]; ok {
		return k
	}
	return "file"
}

// attachmentView is what an upload answers: the path the message should name, relative to the
// workspace.
type attachmentView struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kind string `json:"kind"`
}

// safeAttachmentName reduces what the browser sent to a plain, visible file name.
func safeAttachmentName(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if r := []rune(name); len(r) > 120 {
		ext := filepath.Ext(name)
		name = string(r[:120-len([]rune(ext))]) + ext
	}
	return name
}

// createAttachment opens a new file in root under name, adding -2, -3... before the extension
// when the name is taken: two screenshots both called image.png must both survive.
func createAttachment(root *os.Root, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = stem + "-" + strconv.Itoa(i) + ext
		}
		if candidate != filepath.Base(candidate) || strings.Contains(candidate, "..") || !validArtifactName(candidate) {
			return nil, "", errors.New("the attachment name is not valid")
		}
		// #nosec G302 G304 G703 -- candidate is a plain name checked above, opened through os.Root, which cannot leave dir.
		f, err := root.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, candidate, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
}

// openAttachmentRoot opens the folder a session's attachments are written through. A variable so a
// test can make it fail for any user, root included.
var openAttachmentRoot = os.OpenRoot

// attachmentWorkspace is the folder the session's agent works in: its workspace, or the
// process's own directory for a session with none.
func attachmentWorkspace(c *conversation) string {
	if ws := c.workspaceDir(); ws != "" {
		return ws
	}
	return "."
}

// removeAttachments deletes the files a session's person attached, when the session goes. They
// live in a folder of the session's own, so a workspace shared with other sessions keeps theirs.
func removeAttachments(c *conversation) {
	ws := c.workspaceDir()
	if ws == "" || !validArtifactName(c.id) {
		return
	}
	_ = os.RemoveAll(filepath.Join(ws, filepath.FromSlash(AttachmentDir), c.id))
}

// handleUploadAttachment (POST /v1/sessions/{id}/attachments?name=...) stores the raw body as a file in the
// session's workspace and answers where. The message then names that path, and the agent reads
// the file with the tools it already has.
func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	name := safeAttachmentName(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "the attachment needs a name")
		return
	}
	ws := attachmentWorkspace(c)
	dir := filepath.Join(ws, filepath.FromSlash(AttachmentDir), c.id)
	// #nosec G301 G703 -- the workspace's own folder, like the artifacts one; the agent must read it.
	if err := os.MkdirAll(dir, 0o755); err != nil || !insideWorkspace(ws, dir) {
		writeError(w, http.StatusInternalServerError, "the attachments folder cannot be used")
		return
	}
	if entries, _ := os.ReadDir(dir); len(entries) >= maxAttachments {
		writeError(w, http.StatusConflict, "this session already holds the most attachments it can")
		return
	}
	root, err := openAttachmentRoot(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer root.Close()
	f, saved, err := createAttachment(root, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, maxAttachmentBytes))
	_ = f.Close()
	if err != nil || n == 0 {
		_ = root.Remove(saved) // #nosec G703 -- removed through os.Root, by the name just created.
		if n == 0 && err == nil {
			writeError(w, http.StatusBadRequest, "the attachment is empty")
			return
		}
		writeError(w, http.StatusRequestEntityTooLarge, "the attachment is larger than the limit")
		return
	}
	writeJSON(w, http.StatusCreated, attachmentView{
		Name: saved,
		Path: AttachmentDir + "/" + c.id + "/" + saved,
		Size: n,
		Kind: attachmentKind(saved),
	})
}

// handleGetAttachment (GET /v1/sessions/{id}/attachments/{name}) serves a file the person
// attached, for the thumbnail the interface draws. It is content the person chose, but it is still
// served with the artifact folder's caution: no sniffing, no scripts, and a type the browser can
// only show when it is a picture.
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	name := r.PathValue("name")
	ws := attachmentWorkspace(c)
	if !validArtifactName(name) || !validArtifactName(c.id) {
		writeError(w, http.StatusNotFound, "there is no attachment with that name")
		return
	}
	root, err := os.OpenRoot(filepath.Join(ws, filepath.FromSlash(AttachmentDir), c.id))
	if err != nil {
		writeError(w, http.StatusNotFound, "there is no attachment with that name")
		return
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "there is no attachment with that name")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "there is no attachment with that name")
		return
	}
	h := w.Header()
	typ, inline := attachmentTypes[strings.ToLower(filepath.Ext(name))]
	if !inline {
		typ = "application/octet-stream"
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	h.Set("Content-Type", typ)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data:; frame-ancestors 'self'")
	http.ServeContent(w, r, name, info.ModTime(), f)
}
