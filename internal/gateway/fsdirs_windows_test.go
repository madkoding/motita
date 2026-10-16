//go:build windows

package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheFolderBrowserWorksOnWindowsDrives: on a real Windows machine the browser starts on a
// drive, lists its folders with the drive as the top, offers the drives above it and creates a
// folder, all through the paths Windows spells.
func TestTheFolderBrowserWorksOnWindowsDrives(t *testing.T) {
	srv, _ := fullDiskServer(t)
	base, err := filepath.EvalSymlinks(t.TempDir()) // the runner's temp folder is spelled with 8.3 names
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "Mis Proyectos"), 0o755); err != nil {
		t.Fatal(err)
	}
	drive := volumeRoot(base)
	if !strings.HasSuffix(drive, `:\`) {
		t.Fatalf("the top of a drive is C:\\ shaped: %q", drive)
	}

	code, out, body := listDirs(t, srv, "")
	if code != 200 || out.Root == "" {
		t.Fatalf("the default start: %d %s", code, body)
	}

	code, out, body = listDirs(t, srv, base)
	if code != 200 || out.Root != drive || len(out.Dirs) != 1 || out.Dirs[0].Name != "Mis Proyectos" {
		t.Fatalf("a folder of the temp drive: %d %s", code, body)
	}
	if code, out, _ = listDirs(t, srv, drive); code != 200 || out.Parent != drivesPath {
		t.Errorf("above a drive are the drives: %d %+v", code, out)
	}
	if code, out, _ = listDirs(t, srv, drivesPath); code != 200 || !hasDrive(out, drive) {
		t.Errorf("the drives list holds %s: %d %+v", drive, code, out)
	}

	for _, bad := range []string{`a:b`, `a*b`, `a?b`, `a|b`, `a\b`, `con<`} {
		if simpleFolderName(bad) {
			t.Errorf("%q cannot name a Windows folder", bad)
		}
	}
	escaped := strings.ReplaceAll(base, `\`, `\\`)
	if code := makeDir(t, srv, `{"path":"`+escaped+`","name":"nuevo"}`); code != 201 {
		t.Errorf("a folder is created: %d", code)
	}
	if _, err := os.Stat(filepath.Join(base, "nuevo")); err != nil {
		t.Errorf("the folder exists on disk: %v", err)
	}
}

func hasDrive(l dirListing, root string) bool {
	for _, d := range l.Dirs {
		if strings.EqualFold(d.Path, root) {
			return true
		}
	}
	return false
}

// TestWindowsSystemFoldersAreMarked: the folder of Windows itself is the system's.
func TestWindowsSystemFoldersAreMarked(t *testing.T) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		t.Skip("no SystemRoot")
	}
	if !systemFolder(root) || !systemFolder(filepath.Join(root, "System32")) {
		t.Errorf("%s is the system's", root)
	}
	if systemFolder(t.TempDir()) {
		t.Error("a temp folder is not")
	}
}
