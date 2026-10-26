package gateway

import (
	"errors"
	"io"
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

// createAttachment opens a new file in dir under name, adding -2, -3... before the extension
// when the name is taken: two screenshots both called image.png must both survive.
func createAttachment(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = stem + "-" + strconv.Itoa(i) + ext
		}
		if !validArtifactName(candidate) {
			return nil, "", errors.New("the attachment name is not valid")
		}
		f, err := os.OpenFile(filepath.Join(dir, candidate), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, candidate, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
}

// attachmentWorkspace is the folder the session's agent works in: its workspace, or the
// process's own directory for a session with none.
func attachmentWorkspace(c *conversation) string {
	if ws := c.workspaceDir(); ws != "" {
		return ws
	}
	wd, _ := os.Getwd()
	return wd
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
	if ws == "" {
		writeError(w, http.StatusNotImplemented, "this session has no workspace to keep the attachment in")
		return
	}
	dir := filepath.Join(ws, filepath.FromSlash(AttachmentDir))
	if err := os.MkdirAll(dir, 0o755); err != nil || !insideWorkspace(ws, dir) {
		writeError(w, http.StatusInternalServerError, "the attachments folder cannot be used")
		return
	}
	if entries, _ := os.ReadDir(dir); len(entries) >= maxAttachments {
		writeError(w, http.StatusConflict, "this session already holds the most attachments it can")
		return
	}
	f, saved, err := createAttachment(dir, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, maxAttachmentBytes))
	_ = f.Close()
	if err != nil || n == 0 {
		_ = os.Remove(f.Name())
		if n == 0 && err == nil {
			writeError(w, http.StatusBadRequest, "the attachment is empty")
			return
		}
		writeError(w, http.StatusRequestEntityTooLarge, "the attachment is larger than the limit")
		return
	}
	writeJSON(w, http.StatusCreated, attachmentView{
		Name: saved,
		Path: AttachmentDir + "/" + saved,
		Size: n,
		Kind: attachmentKind(saved),
	})
}
