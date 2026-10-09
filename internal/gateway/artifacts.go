package gateway

import (
	"bytes"
	"encoding/json"
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
	// Pinned files are never pruned. ExpiresAt is when an unpinned file will be, and is absent
	// when artifacts are kept for ever.
	Pinned    bool       `json:"pinned,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// pinFile holds the names of the pinned artifacts of one folder. It starts with a dot, which a
// valid artifact name cannot, so it can never be mistaken for one.
const pinFile = ".pinned"

// readPinned reads the set of pinned names of a folder. A missing or damaged file is "none":
// a pin is a courtesy, and losing one must not break the list.
func readPinned(dir string) map[string]bool {
	pinned := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(dir, pinFile))
	if err != nil {
		return pinned
	}
	var names []string
	if json.Unmarshal(b, &names) != nil {
		return pinned
	}
	for _, n := range names {
		pinned[n] = true
	}
	return pinned
}

// setPinned pins or unpins one name, keeping the file sorted so it does not churn.
func setPinned(dir, name string, pin bool) error {
	pinned := readPinned(dir)
	if pinned[name] == pin {
		return nil
	}
	if pin {
		pinned[name] = true
	} else {
		delete(pinned, name)
	}
	names := make([]string, 0, len(pinned))
	for n := range pinned {
		names = append(names, n)
	}
	sort.Strings(names)
	b, _ := json.Marshal(names)
	return os.WriteFile(filepath.Join(dir, pinFile), b, 0o644)
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

// projectArtifactName is the name a session's file takes in its project's shared folder: the
// session's short id in front, so two sessions that both save "report.md" keep both instead of
// the last one silently replacing the first. A session still replaces its OWN earlier file.
func projectArtifactName(sessionID, name string) string {
	short := sessionID
	if len(short) > 8 {
		short = short[:8]
	}
	return short + "-" + name
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
			_, _ = s.storeArtifact(projectDst, projectArtifactName(c.id, e.Name()), data)
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
		if err != nil || !fi.Mode().IsRegular() || strings.HasPrefix(e.Name(), ".") {
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
	dir := s.scopedArtifactDir(convOf(r), r)
	list := listArtifacts(dir)
	pinned := readPinned(dir)
	for i := range list {
		list[i].Pinned = pinned[list[i].Name]
		if !list[i].Pinned && s.opts.ArtifactDays > 0 {
			at := list[i].Modified.AddDate(0, 0, s.opts.ArtifactDays)
			list[i].ExpiresAt = &at
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": list})
}

// handlePinArtifact pins or unpins one file: a pinned file is exempt from the retention.
func (s *Server) handlePinArtifact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pinned bool `json:"pinned"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	// The file is looked up UNDER the lock the prune deletes under: a file found before the lock
	// may be gone by the time the pin is written, and the pin would be inherited by the next
	// file of that name.
	s.artifactsMu.Lock()
	defer s.artifactsMu.Unlock()
	path, ok := s.artifactPath(w, r)
	if !ok {
		return
	}
	if err := setPinned(filepath.Dir(path), filepath.Base(path), body.Pinned); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	// frame-ancestors 'self': the interface shows an artifact in a frame of its own page, and
	// no other site may frame it.
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'; frame-ancestors 'self'")
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
	s.artifactsMu.Lock()
	_ = setPinned(filepath.Dir(path), filepath.Base(path), false)
	s.artifactsMu.Unlock()
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

// artifactPruneInterval is how often the store is cleaned once the gateway is up.
const artifactPruneInterval = 24 * time.Hour

// sessionIsKnown reports whether a session still exists: held in memory, or saved on disk
// (sessions beyond the in-memory ceiling are only on disk and come back on demand).
func (s *Server) sessionIsKnown(id string) bool {
	s.sessionsMu.Lock()
	_, held := s.sessions[id]
	s.sessionsMu.Unlock()
	if held {
		return true
	}
	if s.store == nil {
		return false
	}
	_, err := os.Stat(s.store.path(id))
	return err == nil
}

// pruneArtifacts deletes what nobody can reach or wants any more: the folders of sessions and
// projects that are gone, and files older than the retention. It only ever deletes under the
// artifact root, and a failure to delete one thing leaves the rest to be tried next time.
func (s *Server) pruneArtifacts(now time.Time) {
	root := s.opts.ArtifactDir
	if root == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var cutoff time.Time
	if s.opts.ArtifactDays > 0 {
		cutoff = now.AddDate(0, 0, -s.opts.ArtifactDays)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if id, isProject := strings.CutPrefix(e.Name(), "project-"); isProject {
			if s.projects != nil && s.projectOf(id) == nil {
				_ = os.RemoveAll(dir)
				continue
			}
		} else if !s.sessionIsKnown(e.Name()) {
			_ = os.RemoveAll(dir)
			continue
		}
		if cutoff.IsZero() {
			continue
		}
		for _, a := range listArtifacts(dir) {
			if a.Modified.Before(cutoff) {
				s.removeIfUnpinned(dir, a.Name)
			}
		}
	}
}

// removeIfUnpinned deletes one file unless it is pinned, deciding and deleting under the same
// lock a pin is written under. Without it the prune read the pins once and deleted later, and a
// file the person pinned in between was lost: the pin was recorded and the file was gone.
func (s *Server) removeIfUnpinned(dir, name string) {
	s.artifactsMu.Lock()
	defer s.artifactsMu.Unlock()
	if !readPinned(dir)[name] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// startArtifactPruner cleans the store now and then once a day until the gateway closes.
func (s *Server) startArtifactPruner(interval time.Duration) {
	if s.opts.ArtifactDir == "" {
		return
	}
	go func() {
		s.pruneArtifacts(time.Now())
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.baseCtx.Done():
				return
			case <-ticker.C:
				s.pruneArtifacts(time.Now())
			}
		}
	}()
}
