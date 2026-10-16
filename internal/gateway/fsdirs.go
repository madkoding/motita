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
	if err != nil || !pathWithinOrSame(root, dir) {
		writeError(w, http.StatusForbidden, "that folder is outside what Motita may reach: "+
			"allow the whole disk in Settings → Security")
		return
	}
	dir, ok := resolveDir(root, dir)
	if !ok {
		writeError(w, http.StatusNotFound, "that folder does not exist")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "the folder could not be read: "+err.Error())
		return
	}
	out := dirListing{Path: dir, Root: root, Sensitive: systemFolder(dir), Dirs: []dirEntry{}}
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
		if e.Name() == name && isDir(filepath.Join(dir, e.Name())) {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

// systemTrees are where the system keeps its own files.
var systemTrees = []string{
	"/etc", "/proc", "/sys", "/dev", "/boot", "/var", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/run",
}

// routeFolderBrowser registers the folder browser's two routes.
func (s *Server) routeFolderBrowser(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/fs/dirs", wrap(s.handleListDirs))
	mux.Handle("POST /v1/fs/dirs", wrap(s.handleMakeDir))
}

// simpleFolderName reports whether name is one folder's name: not empty, not a path, not "..".
func simpleFolderName(name string) bool {
	if name == "" || name == "." || strings.Contains(name, "..") {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00")
}

// systemFolder reports whether dir is one of the system's trees or inside one.
func systemFolder(dir string) bool {
	for _, tree := range systemTrees {
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
	if !s.fullDisk() {
		writeError(w, http.StatusForbidden, "creating folders outside the workspace needs the whole disk "+
			"allowed in Settings → Security")
		return
	}
	name := strings.TrimSpace(body.Name)
	if !simpleFolderName(name) {
		writeError(w, http.StatusBadRequest, "the folder name must be a simple name, not a path")
		return
	}
	parent, ok := resolveDir("/", strings.TrimSpace(body.Path))
	if !ok || !filepath.IsAbs(body.Path) {
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
	dir, found := resolveDir("/", raw)
	if found {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
	}
	switch {
	case !found:
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
