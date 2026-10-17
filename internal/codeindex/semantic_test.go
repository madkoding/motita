package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corpus: "roll" and "rotate" never appear together, but both appear next to "segment" and
// "journal", the way two names for one idea do in a codebase.
func semanticProject(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "wal/roll.go", "func rollJournal() {\n// close the segment and start a new journal segment\n}\n")
	write(t, root, "wal/rotate.go", "func rotateJournal() {\n// close the segment and start a new journal segment\n}\n")
	write(t, root, "wal/trim.go", "func trimJournal() {\n// drop the old journal segment after a checkpoint\n}\n")
	write(t, root, "net/dial.go", "func dialPeer() {\n// open a socket to the peer and send the handshake\n}\n")
	write(t, root, "net/listen.go", "func listenPeer() {\n// accept a socket from the peer and read the handshake\n}\n")
	write(t, root, "net/close.go", "func closePeer() {\n// close the socket to the peer\n}\n")
	write(t, root, "wal/roll_test.go", "func TestRoll() {\n// roll the journal and check the segment\n}\n")
	write(t, root, "ui/button.go", "func drawButton() {\n// paint the label inside the border\n}\n")
	write(t, root, "ui/menu.go", "func drawMenu() {\n// list the entries under the title\n}\n")
	write(t, root, "ui/theme.go", "func loadTheme() {\n// read the colours from the palette\n}\n")
	write(t, root, "ui/font.go", "func loadFont() {\n// read the glyphs from the atlas\n}\n")
	return root
}

func TestSemanticFindsWhatSharesContextEvenWithoutTheWord(t *testing.T) {
	root := semanticProject(t)
	lexical, _ := Build(context.Background(), root, Options{})
	withMeaning, _ := Build(context.Background(), root, Options{})
	withMeaning.sem = buildSemantic(withMeaning)

	paths := func(hits []Hit) string {
		var p []string
		for _, h := range hits {
			p = append(p, h.Path)
		}
		return strings.Join(p, ",")
	}
	// "roll" is in roll.go and roll_test.go only; rotate.go shares their whole context.
	if got := paths(lexical.Search("roll", 5)); strings.Contains(got, "rotate.go") {
		t.Fatalf("BM25 alone should not reach rotate.go: %s", got)
	}
	if got := paths(withMeaning.Search("roll", 5)); !strings.Contains(got, "rotate.go") {
		t.Errorf("the semantic ranking should bring rotate.go in: %s", got)
	}
	if got := paths(withMeaning.Search("roll", 2)); !strings.Contains(got, "roll.go") {
		t.Errorf("the exact word must still rank first: %s", got)
	}
}

func TestSemanticEdges(t *testing.T) {
	var none *semantic
	if none.rank([]string{"x"}) != nil {
		t.Error("a nil semantic must rank nothing")
	}
	ix, _ := Build(context.Background(), semanticProject(t), Options{})
	ix.sem = buildSemantic(ix)
	if ix.sem.rank([]string{"zzzunknown"}) != nil {
		t.Error("a query of unknown terms must rank nothing")
	}
	many := []string{"journal", "segment", "peer", "socket", "handshake", "close"}
	if got := ix.sem.rank(many); len(got) > semCandidate {
		t.Errorf("candidates not capped: %d", len(got))
	}
	if eligible(1, 10) || eligible(6, 10) || !eligible(2, 10) || !eligible(5, 10) {
		t.Error("eligible must keep terms seen in 2 windows up to half of them")
	}
	if normalize(make([]float32, semDim)) {
		t.Error("a zero vector cannot be normalized")
	}
	if dot([]float32{1, 2}, []float32{3}) != 3 {
		t.Error("dot must stop at the shorter vector")
	}
	if v := randomVector("abc"); len(v) != semDim || randomVector("abc")[0] != v[0] {
		t.Error("random vectors must be fixed per term")
	}
}

func TestATermWithNoCompanyHasNoVector(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "solo\n")
	write(t, root, "b.txt", "solo\n")
	write(t, root, "c.txt", "other words here\n")
	write(t, root, "d.txt", "different content there\n")
	write(t, root, "e.txt", "more text everywhere\n")
	ix, _ := Build(context.Background(), root, Options{})
	s := buildSemantic(ix)
	if _, ok := s.ctx["solo"]; ok {
		t.Error("a term that only ever appears alone must not keep a zero vector")
	}
}

func TestFuseWithoutSemanticKeepsBM25(t *testing.T) {
	ix := &Index{}
	in := []ranked{{1, 2}, {0, 1}}
	if got := ix.fuse(in, nil); len(got) != 2 || got[0].i != 1 {
		t.Errorf("fuse = %+v", got)
	}
	got := ix.fuse([]ranked{{1, 9}, {2, 8}}, []ranked{{2, 0.9}, {1, 0.5}, {3, 0.4}})
	if len(got) != 3 || got[0].i != 1 && got[0].i != 2 {
		t.Errorf("fused = %+v", got)
	}
	tie := ix.fuse([]ranked{{5, 1}}, []ranked{{4, 1}})
	if tie[0].i != 5 {
		t.Errorf("a lexical first place beats a semantic first place: %+v", tie)
	}
}

func TestSearcherWithSemanticAndFilesGoneMissing(t *testing.T) {
	root := semanticProject(t)
	s := NewSearcher(Options{Semantic: true})
	hits, err := s.Search(context.Background(), root, "roll", 5)
	if err != nil || len(hits) < 2 {
		t.Fatalf("hits = %+v err = %v", hits, err)
	}
	ix := s.byAt[root]
	if ix.sem == nil {
		t.Fatal("the semantic index was not built")
	}
	// A file removed after indexing is skipped, and a file that shrank too.
	if err := os.Remove(filepath.Join(root, "wal/roll.go")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "wal/rotate.go", "x")
	for _, h := range ix.Search("roll", 5) {
		if strings.HasSuffix(h.Path, "roll.go") || strings.HasSuffix(h.Path, "rotate.go") {
			t.Errorf("a passage that is no longer in the file was returned: %+v", h)
		}
	}
}
