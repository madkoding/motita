package gateway

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ArtifactDir is where the agent leaves what it produced for the person - a report, a page,
// a diagram, a data file - relative to its workspace. The prompt tells it to; the gateway
// copies the folder out when a run ends, because a session's worktree is removed with the
// session and the work would go with it.
const ArtifactDir = ".motita/artifacts"

const (
	// maxArtifacts and maxArtifactBytes bound what one folder keeps: the files are served
	// back whole, and a runaway loop writing files must not fill the disk.
	maxArtifacts     = 50
	maxArtifactBytes = 5 << 20
	// maxArtifactTotalBytes bounds everything the gateway keeps, across every session and
	// project: the per-folder limits alone still allow a thousand sessions to fill a disk.
	maxArtifactTotalBytes = 500 << 20
)

// artifactTypes covers what the platform's table does not know. Without it a Markdown file is
// served as opaque binary, and the page could only offer to download it.
var artifactTypes = map[string]string{
	".md":       "text/markdown; charset=utf-8",
	".markdown": "text/markdown; charset=utf-8",
	".csv":      "text/csv; charset=utf-8",
	".svg":      "image/svg+xml",
}

// readArtifactFile reads a file the agent left. A variable so a test can make the read fail
// for any user, root included - a permission trick only works for one of them.
var readArtifactFile = os.ReadFile

// errArtifactRefused says why a file was not kept, so the upload can answer with it.
type errArtifactRefused string

func (e errArtifactRefused) Error() string { return string(e) }

// Artifact is one saved file, as the list reports it.
type Artifact struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Type     string    `json:"type"`
	Modified time.Time `json:"modified"`
}

// validArtifactName accepts a plain file name: no path, nothing hidden. The name comes from
// the URL and from the agent's own folder, and either could carry "../".
func validArtifactName(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.HasPrefix(name, ".") &&
		!strings.ContainsAny(name, "/\\\x00") && len(name) <= 200
}

// artifactDirFor is the folder one session's artifacts live in, or "" when the gateway keeps
// none (an empty root means "not available", like the project directory).
func (s *Server) artifactDirFor(sessionID string) string {
	if s.opts.ArtifactDir == "" || !validArtifactName(sessionID) {
		return ""
	}
	return filepath.Join(s.opts.ArtifactDir, sessionID)
}

// projectArtifactDirFor is the folder the artifacts of a whole project live in, shared by its
// sessions. The "project-" prefix cannot collide with a session id, which is generated.
func (s *Server) projectArtifactDirFor(projectID string) string {
	if projectID == "" {
		return ""
	}
	return s.artifactDirFor("project-" + projectID)
}

// scopedArtifactDir is the folder a request is about: the session's own, or the project's
// when the request says `?scope=project` and the session belongs to one.
func (s *Server) scopedArtifactDir(c *conversation, r *http.Request) string {
	if r.URL.Query().Get("scope") == "project" {
		return s.projectArtifactDirFor(c.projectID)
	}
	return s.artifactDirFor(c.id)
}

// artifactBytesUnder adds up the size of every file below root. A missing root is zero.
func artifactBytesUnder(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// storeArtifact writes one file into dir under every limit: a plain name, a size, a count per
// folder and a total for the gateway. It reports whether the file is new or changed, so a run
// is only told about what it actually produced; an identical file is not a change.
func (s *Server) storeArtifact(dir, name string, data []byte) (changed bool, err error) {
	if dir == "" || !validArtifactName(name) {
		return false, errArtifactRefused("the artifact name is not valid")
	}
	if len(data) > maxArtifactBytes {
		return false, errArtifactRefused("the artifact is larger than the limit")
	}
	path := filepath.Join(dir, name)
	old, readErr := os.ReadFile(path)
	exists := readErr == nil
	if exists && bytes.Equal(old, data) {
		return false, nil
	}
	if !exists && len(listArtifacts(dir)) >= maxArtifacts {
		return false, errArtifactRefused("this folder already holds the most artifacts it can")
	}
	if artifactBytesUnder(s.opts.ArtifactDir)-int64(len(old))+int64(len(data)) > maxArtifactTotalBytes {
		return false, errArtifactRefused("the artifact store is full")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, data, 0o644)
}

// collectArtifacts copies the files the agent left in the workspace into the session's own
// folder, and into its project's, so the sessions of a project can see each other's. It
// returns what is new or changed, for the run's `done` event. Everything degrades to "nothing
// saved": a failure here must not fail the turn. Symlinks are skipped, so a link cannot make
// the gateway copy a file from outside the workspace.
func (s *Server) collectArtifacts(c *conversation) []Artifact {
	dst := s.artifactDirFor(c.id)
	if dst == "" || c.workspace == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(c.workspace, filepath.FromSlash(ArtifactDir)))
	if err != nil {
		return nil
	}
	projectDst := s.projectArtifactDirFor(c.projectID)
	var fresh []Artifact
	for _, e := range entries {
		src := filepath.Join(c.workspace, filepath.FromSlash(ArtifactDir), e.Name())
		fi, err := os.Lstat(src)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxArtifactBytes {
			continue
		}
		data, err := readArtifactFile(src)
		if err != nil {
			continue
		}
		changed, err := s.storeArtifact(dst, e.Name(), data)
		if err != nil {
			continue
		}
		if projectDst != "" {
			_, _ = s.storeArtifact(projectDst, e.Name(), data)
		}
		if changed {
			fresh = append(fresh, Artifact{Name: e.Name(), Size: int64(len(data)), Type: artifactType(e.Name()), Modified: time.Now()})
		}
	}
	return fresh
}

// listArtifacts reads a session's folder, newest first. A missing folder is the normal case.
func listArtifacts(dir string) []Artifact {
	out := []Artifact{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, Artifact{Name: e.Name(), Size: fi.Size(), Type: artifactType(e.Name()), Modified: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// artifactType is the media type a file is served as. Unknown extensions are plain binary,
// which the browser downloads instead of interpreting.
func artifactType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if t, ok := artifactTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}

// handleListArtifacts answers the files this session (or, with `?scope=project`, its
// project) has saved.
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": listArtifacts(s.scopedArtifactDir(convOf(r), r))})
}

// artifactPath resolves the {name} of a request to a file in the folder it is about, answering
// the error itself when there is none.
func (s *Server) artifactPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	dir := s.scopedArtifactDir(convOf(r), r)
	if dir == "" || !validArtifactName(name) {
		writeError(w, http.StatusNotFound, "there is no artifact with that name")
		return "", false
	}
	path := filepath.Join(dir, name)
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "there is no artifact with that name")
		return "", false
	}
	return path, true
}

// handlePutArtifact saves a file the person uploads, as the raw body, into the session's
// folder. It takes the same limits as what the agent leaves.
func (s *Server) handlePutArtifact(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	dir := s.artifactDirFor(c.id)
	if dir == "" {
		writeError(w, http.StatusNotImplemented, "this gateway does not keep artifacts")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxArtifactBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the artifact is larger than the limit")
		return
	}
	if _, err := s.storeArtifact(dir, r.PathValue("name"), data); err != nil {
		status := http.StatusInternalServerError
		var refused errArtifactRefused
		if errors.As(err, &refused) {
			status = http.StatusBadRequest
		}
		writeError(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetArtifact serves one file. It is content the AGENT wrote, so it is never trusted:
// nosniff stops the browser from guessing a more dangerous type, and the sandbox policy takes
// scripts and same-origin access away from a page that is opened directly.
func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	path, ok := s.artifactPath(w, r)
	if !ok {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "there is no artifact with that name")
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", artifactType(path))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	_, _ = io.Copy(w, f)
}

// handleDeleteArtifact removes one file.
func (s *Server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	path, ok := s.artifactPath(w, r)
	if !ok {
		return
	}
	if err := os.Remove(path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeArtifacts deletes everything a session saved, when the session goes.
func (s *Server) removeArtifacts(sessionID string) {
	if dir := s.artifactDirFor(sessionID); dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// removeProjectArtifacts deletes what a project's sessions shared, when the project goes.
func (s *Server) removeProjectArtifacts(projectID string) {
	if dir := s.projectArtifactDirFor(projectID); dir != "" {
		_ = os.RemoveAll(dir)
	}
}
