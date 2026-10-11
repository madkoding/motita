package gateway

import (
	"context"
	"encoding/base64"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// PreviewDir is where the agent leaves screenshots of what it changed, relative to its
// workspace. The prompt tells it to; the gateway reads the folder when the run ends.
const PreviewDir = ".motita/previews"

const (
	// maxPreviews and maxPreviewBytes bound what one `done` event carries: the images travel
	// inline, and an event of tens of megabytes is one a slow client never finishes reading.
	maxPreviews     = 4
	maxPreviewBytes = 1 << 20
)

// previewEvent is one picture of the result, inline so the client needs no second request
// (and no path to a file it cannot reach - the workspace can live on another machine).
type previewEvent struct {
	Name string `json:"name"`
	// URL is a data: URL.
	URL string `json:"url"`
}

// changeReport is what the user is shown when a run that changed files ends: the
// pictures first, then the files as a summary. The code itself stays in the diff.
type changeReport struct {
	Files    []gitx.FileChange `json:"files,omitempty"`
	Previews []previewEvent    `json:"previews,omitempty"`
}

var previewExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// buildChangeReport measures what the run changed in workspace since rev and gathers its
// pictures: the screenshots the agent left in PreviewDir, then any image file the run added or
// changed. Everything degrades to "nothing to show" - a report that fails must not fail the turn.
func buildChangeReport(ctx context.Context, workspace, rev string) changeReport {
	var rep changeReport
	if workspace == "" {
		return rep
	}
	// A git failure leaves the list empty (ChangesSince returns nil with its error): the
	// screenshots below are still worth showing, and the summary is a courtesy, not the result.
	files, _ := gitx.ChangesSince(ctx, workspace, rev)
	var pics []string
	if entries, err := os.ReadDir(filepath.Join(workspace, filepath.FromSlash(PreviewDir))); err == nil {
		for _, e := range entries {
			if !e.IsDir() && previewExts[strings.ToLower(filepath.Ext(e.Name()))] {
				pics = append(pics, filepath.Join(workspace, filepath.FromSlash(PreviewDir), e.Name()))
			}
		}
		sort.Strings(pics)
	}
	for _, f := range files {
		// The screenshots are evidence, not a change the user asked for.
		if strings.HasPrefix(f.Path, PreviewDir+"/") || strings.HasPrefix(f.Path, ArtifactDir+"/") {
			continue
		}
		rep.Files = append(rep.Files, f)
		if f.Status != "deleted" && previewExts[strings.ToLower(filepath.Ext(f.Path))] {
			pics = append(pics, filepath.Join(workspace, filepath.FromSlash(f.Path)))
		}
	}
	for _, p := range pics {
		if len(rep.Previews) == maxPreviews {
			break
		}
		if ev, ok := readPreview(workspace, p); ok {
			rep.Previews = append(rep.Previews, ev)
		}
	}
	return rep
}

// readPreview loads one picture as a data: URL, or reports that it is not usable (missing,
// too large, not a regular file, or outside workspace).
//
// The workspace is written by the agent, so a "screenshot" can be a symlink to any file this
// process can read. Lstat refuses a symlinked file, and the folder it sits in must resolve inside
// the workspace, which refuses a symlinked PreviewDir as well.
func readPreview(workspace, path string) (previewEvent, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > maxPreviewBytes {
		return previewEvent{}, false
	}
	if !insideWorkspace(workspace, filepath.Dir(path)) {
		return previewEvent{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return previewEvent{}, false
	}
	typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if typ == "" {
		typ = "image/png"
	}
	return previewEvent{
		Name: filepath.Base(path),
		URL:  "data:" + typ + ";base64," + base64.StdEncoding.EncodeToString(b),
	}, true
}

// insideWorkspace reports whether dir, with every symlink resolved, is workspace or below it.
func insideWorkspace(workspace, dir string) bool {
	root, errRoot := filepath.EvalSymlinks(workspace)
	real, errReal := filepath.EvalSymlinks(dir)
	if errRoot != nil || errReal != nil {
		return false
	}
	rel, err := filepath.Rel(root, real)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// clearPreviews empties the screenshot folder before a run. A missing folder is the normal case.
func clearPreviews(workspace string) {
	if workspace == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(workspace, filepath.FromSlash(PreviewDir)))
}
