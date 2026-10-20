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
	Path   string `json:"path"`
	Parent string `json:"parent,omitempty"`
	Home   string `json:"home,omitempty"`
	Root   string `json:"root"`
	// Sensitive marks a folder that holds the system's own files, where a project would put an
	// agent among them: the browser warns before it is chosen.
	Sensitive bool       `json:"sensitive,omitempty"`
	Dirs      []dirEntry `json:"dirs"`
}

// handleListDirs lists the folders inside a folder, so the project dialogs can browse the disk for
// the folder of a project. With the whole disk allowed it starts at the user's home and reaches
// anywhere; otherwise it only shows the workspace, which is where a project folder goes anyway.
// Only folders are listed, never files, and hidden folders are left out.
func (s *Server) handleListDirs(w http.ResponseWriter, r *http.Request) {
	full := s.fullDisk()
	home, _ := os.UserHomeDir()
	asked := strings.TrimSpace(r.URL.Query().Get("path"))
	if full && asked == drivesPath && len(driveRoots()) > 0 {
		writeJSON(w, http.StatusOK, drivesListing(home))
		return
	}
	dir, root, status, problem := s.browsableDir(asked, home, full)
	if problem != "" {
		writeError(w, status, problem)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "the folder could not be read: "+err.Error())
		return
	}
	out := dirListing{Path: dir, Root: root, Sensitive: systemFolder(dir), Dirs: []dirEntry{}}
	out.Parent = parentOf(dir, root, full)
	if home != "" && (full || pathWithinOrSame(root, home)) {
		out.Home = home
	}
	for _, e := range entries {
		sub := filepath.Join(dir, e.Name())
		if strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "$") || !isDir(sub) {
			continue
		}
		out.Dirs = append(out.Dirs, dirEntry{Name: e.Name(), Path: sub, Git: isDir(filepath.Join(sub, ".git"))})
	}
	sort.Slice(out.Dirs, func(i, j int) bool {
		return strings.ToLower(out.Dirs[i].Name) < strings.ToLower(out.Dirs[j].Name)
	})
	writeJSON(w, http.StatusOK, out)
}

// browsableDir finds the folder a listing was asked for, and the root it is within: the whole
// tree of its drive with the whole disk allowed, the workspace otherwise. When the folder cannot
// be shown it says why, with the status to answer.
func (s *Server) browsableDir(asked, home string, full bool) (dir, root string, status int, problem string) {
	root = s.opts.WorkspaceDir
	if full {
		root = "/"
	}
	if root == "" {
		return "", "", http.StatusNotImplemented, "no workspace directory is configured"
	}
	dir = asked
	if dir == "" {
		dir = root
		if full && home != "" {
			dir = home
		}
	}
	if !filepath.IsAbs(dir) {
		return "", "", http.StatusBadRequest, "the folder must be an absolute path"
	}
	dir = filepath.Clean(dir)
	if full {
		root = volumeRoot(dir)
	}
	if !pathWithinOrSame(root, dir) {
		return "", "", http.StatusForbidden, "that folder is outside what Motita may reach: " +
			"allow the whole disk in Settings → Security"
	}
	dir, ok := resolveDir(root, dir)
	if !ok {
		return "", "", http.StatusNotFound, errNoFolder
	}
	return dir, root, 0, ""
}

// resolveDir finds the folder a client named by walking down from root, one name at a time, and
// answers the path built from the names the disk itself lists, never from the client's text. So a
// request can only ever name a folder that exists under root: no "..", no link that leads out, no
// byte the file system would read differently.
func resolveDir(root, raw string) (string, bool) {
	rel, err := filepath.Rel(root, filepath.Clean(raw))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	cur := root
	if rel == "." {
		return cur, isDir(cur)
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		next, ok := childDir(cur, part)
		if !ok {
			return "", false
		}
		cur = next
	}
	return cur, true
}

// childDir is the folder of dir that is called name, as the disk spells it.
func childDir(dir, name string) (string, bool) {
	entries, _ := os.ReadDir(dir) // an unreadable folder lists nothing, so nothing is found in it
	for _, e := range entries {
		if sameName(e.Name(), name) && isDir(filepath.Join(dir, e.Name())) {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

// drivesPath is what a client asks for to see the machine's drives, on a system that has several
// (Windows). The list it answers has no path of its own: nothing can be chosen there.
const drivesPath = "@drives"

// driveRoots, and the system's trees, are the platform's; variables so a test can stand in for
// a machine with drives.
var (
	driveRoots  = platformDriveRoots
	systemTrees = platformSystemTrees
)

// drivesListing is the list of the machine's drives.
func drivesListing(home string) dirListing {
	out := dirListing{Dirs: []dirEntry{}, Home: home}
	for _, root := range driveRoots() {
		out.Dirs = append(out.Dirs, dirEntry{Name: strings.TrimSuffix(root, `\`), Path: root})
	}
	return out
}

// parentOf is the folder above dir. At the top of a drive it is the list of drives when the
// machine has several; at the top of the one tree, or of the workspace, there is nothing above.
func parentOf(dir, root string, full bool) string {
	if !gitx.SamePath(dir, root) {
		return filepath.Dir(dir)
	}
	if full && len(driveRoots()) > 0 {
		return drivesPath
	}
	return ""
}

// routeDisk registers what is about how much of the disk Motita reaches: the Security setting
// and the folder browser that depends on it.
func (s *Server) routeDisk(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/security", wrap(s.handleGetSecurity))
	mux.Handle("PUT /v1/security", wrap(s.handlePutSecurity))
	mux.Handle("GET /v1/boxes", wrap(s.handleListBoxes))
	mux.Handle("DELETE /v1/boxes", wrap(s.handleStopBoxes))
	mux.Handle("DELETE /v1/boxes/{id}", wrap(s.handleRemoveBox))
	mux.Handle("GET /v1/code-index", wrap(s.handleGetCodeIndex))
	mux.Handle("PUT /v1/code-index", wrap(s.handlePutCodeIndex))
	mux.Handle("GET /v1/autopilot", wrap(s.handleGetAutopilot))
	mux.Handle("PUT /v1/autopilot", wrap(s.handlePutAutopilot))
	mux.Handle("POST /v1/autopilot/run", wrap(s.handleRunAutopilot))
	mux.Handle("DELETE /v1/code-index/cache", wrap(s.handleDeleteCodeIndexCache))
	mux.Handle("GET /v1/fs/dirs", wrap(s.handleListDirs))
	mux.Handle("POST /v1/fs/dirs", wrap(s.handleMakeDir))
}

// simpleFolderName reports whether name is one folder's name: not empty, not a path, not "..".
func simpleFolderName(name string) bool {
	if name == "" || name == "." || strings.Contains(name, "..") {
		return false
	}
	return !strings.ContainsAny(name, badNameChars)
}

// isSystemTree reports whether dir is one of the system's trees itself, not a folder inside one.
func isSystemTree(dir string) bool {
	for _, tree := range systemTrees() {
		if sameFolder(tree, dir) {
			return true
		}
	}
	return false
}

// sameFolder reports whether two paths name the same folder.
func sameFolder(a, b string) bool {
	return pathWithinOrSame(a, b) && pathWithinOrSame(b, a)
}

// sensitiveNeedsConfirm answers 409, and returns false, when dir is inside one of the system's
// trees and the person has not confirmed they mean it.
func sensitiveNeedsConfirm(w http.ResponseWriter, dir string, confirmed bool) bool {
	if confirmed || !systemFolder(dir) {
		return true
	}
	writeError(w, http.StatusConflict, "that folder is inside the system's own files: confirm that you "+
		"want a project there")
	return false
}

// systemFolder reports whether dir is one of the system's trees or inside one.
func systemFolder(dir string) bool {
	for _, tree := range systemTrees() {
		if pathWithinOrSame(tree, dir) {
			return true
		}
	}
	return false
}

// handleMakeDir creates a folder inside the one the browser is on, so a project can get a folder
// of its own without leaving the page. Same reach as the listing: the whole disk only when
// Settings → Security allows it. The name is a simple one, never a path.
func (s *Server) handleMakeDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if !simpleFolderName(name) {
		writeError(w, http.StatusBadRequest, "the folder name must be a simple name, not a path")
		return
	}
	where := strings.TrimSpace(body.Path)
	root, reach := s.reachRoot(where)
	if !reach {
		writeError(w, http.StatusForbidden, "creating folders outside the workspace needs the whole disk "+
			"allowed in Settings → Security")
		return
	}
	parent, ok := resolveDir(root, where)
	if !ok || !filepath.IsAbs(where) {
		writeError(w, http.StatusBadRequest, "the folder to create it in must be an existing absolute path")
		return
	}
	if err := makeChild(parent, name); err != nil {
		writeError(w, http.StatusConflict, "could not create the folder: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, dirEntry{Name: name, Path: filepath.Join(parent, name)})
}

// makeChild creates the folder name inside parent, through a handle on parent that cannot be led
// out of it.
func makeChild(parent, name string) error {
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Mkdir(name, 0o755)
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

// reachRoot is the root a client path is walked from: its drive with the whole disk allowed, the
// workspace otherwise, where only a path inside the workspace is reachable.
func (s *Server) reachRoot(raw string) (string, bool) {
	if s.fullDisk() {
		return volumeRoot(raw), true
	}
	ws := s.opts.WorkspaceDir
	return ws, ws != "" && pathWithinOrSame(ws, filepath.Clean(raw))
}

// externalProjectFolder reads the folder a project was asked to live in. Outside the workspace the
// whole disk must be allowed in Settings → Security; the folder must exist, and it cannot be the
// disk's root, the workspace itself or the folder of a project that is already there. It answers
// the request itself, and returns false, when the folder is not usable.
func (s *Server) externalProjectFolder(w http.ResponseWriter, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !filepath.IsAbs(raw) {
		writeError(w, http.StatusBadRequest, "the folder must be an absolute path")
		return "", false
	}
	root, ok := s.reachRoot(raw)
	if !ok {
		writeError(w, http.StatusForbidden, "a folder outside the workspace needs the whole disk allowed "+
			"in Settings → Security")
		return "", false
	}
	dir, found := resolveDir(root, raw)
	if found {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
	}
	if !found {
		writeError(w, http.StatusBadRequest, errNoFolder)
		return "", false
	}
	if problem := s.projectFolderProblem(dir); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return "", false
	}
	return dir, true
}

// errNoFolder is the answer for a folder the disk does not have.
const errNoFolder = "that folder does not exist"

// projectFolderProblem says why dir cannot be a project's folder, empty when it can.
func (s *Server) projectFolderProblem(dir string) string {
	ws := s.opts.WorkspaceDir
	home, _ := os.UserHomeDir()
	problem := ""
	switch {
	case filepath.Dir(dir) == dir:
		problem = "the root of the disk cannot be a project"
	case home != "" && sameFolder(dir, home):
		problem = "the home folder itself cannot be a project: choose a folder inside it"
	case isSystemTree(dir):
		problem = "that is one of the system's own folders and cannot be a project"
	case ws != "" && pathWithinOrSame(dir, ws):
		problem = "the workspace itself (or a folder around it) cannot be a project"
	case ws != "" && pathWithinOrSame(ws, dir) && reservedFolders[strings.ToLower(filepath.Base(dir))]:
		problem = "that folder is where motita keeps its sessions and shared repositories"
	default:
		problem = ""
	}
	return problem
}

// newExternalFolder is externalProjectFolder for a project being created: the last folder of the
// path may not exist yet, and is then made (by handleCreateProject) inside its existing parent.
// Only that one level is created, with a simple name, so a typo cannot build a deep tree.
func (s *Server) newExternalFolder(w http.ResponseWriter, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	root, ok := s.reachRoot(raw)
	if !ok || !filepath.IsAbs(raw) {
		return s.externalProjectFolder(w, raw)
	}
	if _, found := resolveDir(root, raw); found {
		return s.externalProjectFolder(w, raw)
	}
	parentRaw, name := filepath.Split(filepath.Clean(raw))
	if !simpleFolderName(name) {
		writeError(w, http.StatusBadRequest, "the folder name must be a simple name, not a path")
		return "", false
	}
	parent, found := resolveDir(root, parentRaw)
	if !found {
		writeError(w, http.StatusBadRequest, errNoFolder)
		return "", false
	}
	dir := filepath.Join(parent, name)
	if problem := s.projectFolderProblem(dir); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return "", false
	}
	return dir, true
}
