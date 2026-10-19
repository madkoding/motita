package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRaw(v *Vault, p string, b []byte) error {
	return os.WriteFile(filepath.Join(v.Root(), p), b, 0o644)
}

func TestSearchRanksNamesFirstAndNeedsEveryWord(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "cooking.md", "first line\nBake the Bread slowly\n")
	write(t, v, "bread.md", strings.Repeat("z", 200)+" bread")
	write(t, v, "empty-body.md", "")
	hits := v.Search("bread", 0)
	if len(hits) != 2 || hits[0].Path != "bread.md" || hits[1].Path != "cooking.md" {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[1].Line != 2 || hits[1].Snippet != "Bake the Bread slowly" {
		t.Errorf("snippet = %d %q", hits[1].Line, hits[1].Snippet)
	}
	if !strings.HasSuffix(hits[0].Snippet, "…") {
		t.Errorf("long snippet = %q", hits[0].Snippet)
	}
	if hits := v.Search("bread slowly", 0); len(hits) != 1 {
		t.Errorf("two words = %+v", hits)
	}
	if hits := v.Search("bread", 1); len(hits) != 1 {
		t.Errorf("limit = %+v", hits)
	}
	// Only the name matches: no line.
	if hits := v.Search("empty-body", 0); len(hits) != 1 || hits[0].Line != 0 {
		t.Errorf("by name = %+v", hits)
	}
	if hits := v.Search("   ", 0); hits != nil {
		t.Errorf("blank = %+v", hits)
	}
}

func TestSearchSkipsNotesThatCannotBeRead(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "needle")
	writeRaw(v, "huge.md", make([]byte, MaxNoteBytes+1))
	if hits := v.Search("needle", 0); len(hits) != 1 {
		t.Errorf("%+v", hits)
	}
}
