package intent

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// TestCorpus runs the classifier over testdata/corpus.tsv: messages as people write them, in
// English and Spanish, each with the mode it should run in. A wrong guess found in use goes
// there, so the rules are tuned against every guess they ever got wrong.
func TestCorpus(t *testing.T) {
	f, err := os.Open("testdata/corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, text, ok := strings.Cut(line, "\t")
		if !ok || (kind != KindPlan && kind != KindTask) {
			t.Fatalf("bad corpus line %q", line)
		}
		n++
		d := Classify(text)
		if d.Kind != kind {
			t.Errorf("%q: %s (%s, %s), want %s", text, d.Kind, d.Source, d.Reason, kind)
		}
		if d.Source == SourceDefault {
			t.Errorf("%q: no rule placed it", text)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 100 {
		t.Errorf("the corpus has %d messages; it should only grow", n)
	}
}
