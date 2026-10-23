package intent

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// corpusLine is one message of testdata/corpus.tsv and the mode it should run in.
type corpusLine struct{ kind, text string }

// readCorpus reads testdata/corpus.tsv.
func readCorpus(t *testing.T) []corpusLine {
	t.Helper()
	f, err := os.Open("testdata/corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []corpusLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, text, ok := strings.Cut(line, "\t")
		if !ok || (kind != KindPlan && kind != KindTask) {
			t.Fatalf("bad corpus line %q", line)
		}
		lines = append(lines, corpusLine{kind, text})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// TestCorpus runs the classifier over testdata/corpus.tsv: messages as people write them, in
// English and Spanish, each with the mode it should run in. A wrong guess found in use goes
// there, so the rules are tuned against every guess they ever got wrong.
func TestCorpus(t *testing.T) {
	lines := readCorpus(t)
	for _, c := range lines {
		d := Classify(c.text)
		if d.Kind != c.kind {
			t.Errorf("%q: %s (%s, %s), want %s", c.text, d.Kind, d.Source, d.Reason, c.kind)
		}
		if d.Source == SourceDefault {
			t.Errorf("%q: no rule placed it", c.text)
		}
	}
	if len(lines) < 100 {
		t.Errorf("the corpus has %d messages; it should only grow", len(lines))
	}
}
