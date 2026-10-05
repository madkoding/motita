package gateway

import (
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
	// maxArtifacts and maxArtifactBytes bound what one session keeps: the files are served
	// back whole, and a runaway loop writing files must not fill the disk.
	maxArtifacts     = 50
	maxArtifactBytes = 5 << 20
)

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

// collectArtifacts copies the files the agent left in the workspace into the session's own
// folder. Everything degrades to "nothing saved": a failure here must not fail the turn.
// Symlinks are skipped, so a link cannot make the gateway copy a file from outside the
// workspace.
func (s *Server) collectArtifacts(sessionID, workspace string) {
	dst := s.artifactDirFor(sessionID)
	if dst == "" || workspace == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(workspace, filepath.FromSlash(ArtifactDir)))
	if err != nil {
		return
	}
	have := len(listArtifacts(dst))
	for _, e := range entries {
		src := filepath.Join(workspace, filepath.FromSlash(ArtifactDir), e.Name())
		fi, err := os.Lstat(src)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxArtifactBytes || !validArtifactName(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dst, e.Name())); err != nil {
			if have >= maxArtifacts {
				continue
			}
			have++
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return
		}
		if b, err := os.ReadFile(src); err == nil {
			_ = os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)
		}
	}
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
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}

// handleListArtifacts answers the files this session has saved.
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": listArtifacts(s.artifactDirFor(c.id))})
}

// artifactPath resolves the {name} of a request to a file of this session, answering the
// error itself when there is none.
func (s *Server) artifactPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	c := convOf(r)
	name := r.PathValue("name")
	dir := s.artifactDirFor(c.id)
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
