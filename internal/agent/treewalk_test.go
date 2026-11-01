package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOneWalkTakesBothFingerprints: the walk that checks the kept reads also gives the sources'
// fingerprint, which a round reuses until an action that may write runs; an output directory and a
// lock file change only the first.
func TestOneWalkTakesBothFingerprints(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main")
	m := &workMemory{}
	m.sync(dir)
	src, ok := m.sources(dir)
	if want, _ := treeFingerprint(dir, true); !ok || src != want {
		t.Fatal("the sync's walk must give the sources' fingerprint")
	}
	write("dist/app.js", "x")
	write("go.sum", "y")
	m.sync(dir)
	if again, _ := m.sources(dir); again != src {
		t.Error("an output directory and a lock file are not sources")
	}
	m.unsynced = true
	write("lib.go", "package main")
	if fresh, _ := m.sources(dir); fresh == src {
		t.Error("after an action that may write, the sources are walked again")
	}
}
