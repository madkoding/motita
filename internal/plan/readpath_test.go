package plan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rootedRunner is a CommandRunner that knows a working directory, the way the real agent does:
// it resolves a read tool's path against root and refuses anything outside it.
type rootedRunner struct {
	root string
	ran  []string
}

func (r *rootedRunner) RunCommand(_ context.Context, command string) (string, int, error) {
	r.ran = append(r.ran, command)
	return "", 1, nil
}

func (r *rootedRunner) ReadPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.root, p)
	}
	if rel, err := filepath.Rel(r.root, p); err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New(p + " is outside the working directory " + r.root)
	}
	return p, nil
}

// TestTheReadToolsWorkInTheSessionsDirectory: read_file, list_directory and search_in_files run
// in this process, whose directory is not the session's. Through a runner that knows the session's
// working directory they read THERE, and refuse a path the session may not read.
func TestTheReadToolsWorkInTheSessionsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("needle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &rootedRunner{root: root}
	p := &Planner{runner: r, commandTimeout: defaultCommandTimeout}

	if got := p.toolListDirectory(json.RawMessage(`{}`)); got != "notes.txt" {
		t.Errorf("list_directory with no path lists the working directory: %q", got)
	}
	if got := p.toolReadFile(json.RawMessage(`{"path":"notes.txt"}`)); got != "needle here\n" {
		t.Errorf("read_file takes a relative path against the working directory: %q", got)
	}
	if got := p.toolReadFile(json.RawMessage(`{"path":"missing.txt"}`)); !strings.HasPrefix(got, "Error accessing the file") {
		t.Errorf("a missing file inside the working directory: %q", got)
	}
	for name, got := range map[string]string{
		"list_directory": p.toolListDirectory(json.RawMessage(`{"path":"/etc"}`)),
		"read_file":      p.toolReadFile(json.RawMessage(`{"path":"../other/secret"}`)),
		"search":         p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"x","path":"/etc"}`)),
	} {
		if !strings.HasPrefix(got, "Error: ") || !strings.Contains(got, "outside the working directory") {
			t.Errorf("%s must refuse a path outside: %q", name, got)
		}
	}
	if len(r.ran) != 0 {
		t.Errorf("a refused search must run nothing: %v", r.ran)
	}

	// grep keeps the path it was given (it runs in the working directory); the Go search reads
	// the resolved one, because this process is somewhere else.
	p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"needle"}`))
	if len(r.ran) != 1 || !strings.HasSuffix(r.ran[0], " '.'") {
		t.Errorf("grep runs on the path as given: %v", r.ran)
	}
	searchNatively = true
	defer func() { searchNatively = false }()
	got := p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"needle"}`))
	if !strings.Contains(got, filepath.Join(root, "notes.txt")+":1:needle here") {
		t.Errorf("the Go search reads the working directory: %q", got)
	}
}

// TestWithoutAResolverAPathIsKeptAsGiven: a planner whose runner knows no working directory keeps
// the old behaviour, an empty path meaning ".".
func TestWithoutAResolverAPathIsKeptAsGiven(t *testing.T) {
	p := &Planner{}
	if got, err := p.readPath("  "); got != "." || err != nil {
		t.Errorf("readPath(blank) = %q, %v", got, err)
	}
	if got, _ := p.readPath(" a/b "); got != "a/b" {
		t.Errorf("readPath = %q", got)
	}
}
