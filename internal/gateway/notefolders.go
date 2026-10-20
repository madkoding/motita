package gateway

import (
	"context"
	"net/http"
)

// The folders of the vault that a scan writes, and what can be done with a whole folder: delete
// it, or write it again. A folder the person made is theirs: it can be deleted, not rescanned.

// What a scan-written folder holds.
const (
	folderGuide    = "guide"
	folderProjects = "projects"
	folderSessions = "sessions"
)

// noteFolder is a folder of the vault that a scan writes. Project and Title name the project a
// guide documents.
type noteFolder struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Project string `json:"project,omitempty"`
	Title   string `json:"title,omitempty"`
}

// scanFolders are the folders a scan writes: one guide per project, the notes of the projects and
// the notes of the sessions.
func (s *Server) scanFolders() []noteFolder {
	out := []noteFolder{
		{Path: scanProjectsFolder, Kind: folderProjects},
		{Path: scanSessionsFolder, Kind: folderSessions},
	}
	if s.projects == nil {
		return out
	}
	projects, err := s.projects.loadAll()
	if err != nil {
		return out
	}
	names := projectNoteNames(projects)
	for _, p := range projects {
		out = append(out, noteFolder{
			Path: manualRoot(names[p.ID], manualSuffix), Kind: folderGuide, Project: p.ID, Title: p.Title,
		})
	}
	return out
}

// scanFolder finds the scan-written folder at path.
func (s *Server) scanFolder(path string) (noteFolder, bool) {
	for _, f := range s.scanFolders() {
		if f.Path == path {
			return f, true
		}
	}
	return noteFolder{}, false
}

// handleDeleteNoteFolder removes the notes of a folder, sub-folders included, and answers how many.
// A guide or a folder of sessions comes back with the next scan; nothing else does.
func (s *Server) handleDeleteNoteFolder(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	n, err := v.DeleteFolder(r.URL.Query().Get("path"))
	if err != nil {
		writeNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

// handleRescanNoteFolder writes a scan-written folder again, in the background and with the model
// options of the last scan: a guide is written again for its project, the projects' and sessions'
// folders for all of them.
func (s *Server) handleRescanNoteFolder(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	f, ok := s.scanFolder(r.URL.Query().Get("path"))
	if !ok {
		writeError(w, http.StatusBadRequest, "this folder was not written by a scan, so there is nothing to write again")
		return
	}
	q := s.againQuery(f.Project)
	s.startScan(w, func(ctx context.Context, t *scanTracker) (noteScan, error) {
		return s.runScan(ctx, v, q, t)
	})
}
