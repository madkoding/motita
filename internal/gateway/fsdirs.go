package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// fullDisk reports whether Settings → Security lets the agent reach the whole disk. It reads the
// same configuration the Security tab shows, and a configuration that cannot be read is the
// workspace: the narrow answer is the safe one.
func (s *Server) fullDisk() bool {
	cfg, _, err := s.securityConfig()
	return err == nil && cfg.Sandbox.FullDisk()
}

// dirEntry is one folder of a listing.
type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Git marks a folder that is a git repository, so the browser can point the user at it.
	Git bool `json:"git,omitempty"`
}

// dirListing is the answer of GET /v1/fs/dirs: the folder asked for, the one above it (empty at
// the top of what the browser may reach), and its sub-folders.
type dirListing struct {
	Path   string     `json:"path"`
	Parent string     `json:"parent,omitempty"`
	Home   string     `json:"home,omitempty"`
	Root   string     `json:"root"`
	Dirs   []dirEntry `json:"dirs"`
}

// handleListDirs lists the folders inside a folder, so the project dialogs can browse the disk for
// the folder of a project. With the whole disk allowed it starts at the user's home and reaches
// anywhere; otherwise it only shows the workspace, which is where a project folder goes anyway.
// Only folders are listed, never files, and hidden folders are left out.
func (s *Server) handleListDirs(w http.ResponseWriter, r *http.Request) {
	root := "/"
	if !s.fullDisk() {
		root = s.opts.WorkspaceDir
	}
	if root == "" {
		writeError(w, http.StatusNotImplemented, "no workspace directory is configured")
		return
	}
	home, _ := os.UserHomeDir()
	dir := strings.TrimSpace(r.URL.Query().Get("path"))
	if dir == "" {
		dir = root
		if root == "/" && home != "" {
			dir = home
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil || !filepath.IsAbs(dir) || !pathWithinOrSame(root, dir) {
		writeError(w, http.StatusForbidden, "that folder is outside what Motita may reach: "+
			"allow the whole disk in Settings → Security")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "the folder could not be read: "+err.Error())
		return
	}
	out := dirListing{Path: dir, Root: root, Dirs: []dirEntry{}}
	if !gitx.SamePath(dir, root) {
		out.Parent = filepath.Dir(dir)
	}
	if home != "" && pathWithinOrSame(root, home) {
		out.Home = home
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if strings.HasPrefix(e.Name(), ".") || !isDir(full) {
			continue
		}
		out.Dirs = append(out.Dirs, dirEntry{Name: e.Name(), Path: full, Git: isDir(filepath.Join(full, ".git"))})
	}
	sort.Slice(out.Dirs, func(i, j int) bool {
		return strings.ToLower(out.Dirs[i].Name) < strings.ToLower(out.Dirs[j].Name)
	})
	writeJSON(w, http.StatusOK, out)
}

// isDir reports whether path is a folder, following a symbolic link.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// pathWithinOrSame reports whether path is root or inside it.
func pathWithinOrSame(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || !relLeavesRoot(rel))
}

// externalProjectFolder reads the folder a project was asked to live in outside the workspace. The
// whole disk must be allowed in Settings → Security, the folder must exist, and it cannot be the
// disk's root, the workspace itself or the folder of a project that is already there. It answers
// the request itself, and returns false, when the folder is not usable.
func (s *Server) externalProjectFolder(w http.ResponseWriter, raw string) (string, bool) {
	if !s.fullDisk() {
		writeError(w, http.StatusForbidden, "a folder outside the workspace needs the whole disk allowed "+
			"in Settings → Security")
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if !filepath.IsAbs(raw) {
		writeError(w, http.StatusBadRequest, "the folder must be an absolute path")
		return "", false
	}
	dir := filepath.Clean(raw)
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	switch {
	case !isDir(dir):
		writeError(w, http.StatusBadRequest, "that folder does not exist")
	case filepath.Dir(dir) == dir:
		writeError(w, http.StatusBadRequest, "the root of the disk cannot be a project")
	case s.opts.WorkspaceDir != "" && pathWithinOrSame(dir, s.opts.WorkspaceDir):
		writeError(w, http.StatusBadRequest, "the workspace itself (or a folder around it) cannot be a project")
	case s.opts.WorkspaceDir != "" && pathWithinOrSame(s.opts.WorkspaceDir, dir) &&
		reservedFolders[strings.ToLower(filepath.Base(dir))]:
		writeError(w, http.StatusBadRequest, "that folder is where motita keeps its sessions and shared repositories")
	default:
		return dir, true
	}
	return "", false
}
