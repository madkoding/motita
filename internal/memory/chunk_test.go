package memory

import (
	"strings"
	"testing"
)

func TestCutBuildsParentsPerSectionAndChildrenPerParagraph(t *testing.T) {
	text := "# Hiring\n\nHow we hire.\n\n## Offers\n\nOne.\n\nTwo.\n\n### Bands\n\nTop 90k.\n"
	parents, kids := cut(7, "Hiring", text, 4)
	if len(parents) != 3 {
		t.Fatalf("parents = %+v", parents)
	}
	third := parents[2]
	if got := strings.Join(third.trail, "|"); got != "Hiring|Offers|Bands" || third.line != 15 || third.entry != 7 {
		t.Errorf("third parent = %+v", parents[2])
	}
	if len(kids) != 3 || kids[0].text != "How we hire." || kids[0].line != 7 || kids[0].parent != 0 {
		t.Errorf("kids = %+v", kids)
	}
	if kids[1].text != "One.\n\nTwo." && kids[1].text != "One.\nTwo." {
		t.Logf("kids[1] = %q", kids[1].text)
	}
	if kids[2].ctx != "Hiring › Offers › Bands" || kids[2].parent != 2 {
		t.Errorf("context = %+v", kids[2])
	}
	if kids[0].ctx != "Hiring" {
		t.Errorf("an entry that opens with its own title says it once: %q", kids[0].ctx)
	}
}

func TestSectionsIgnoreHeadingsInCodeAndEmptyOnes(t *testing.T) {
	text := "\n\nintro\n\n# \nnot a heading\n\n```\n# comment in code\n```\n\n#nospace\n\n####### seven\n\n## Real\ntext\n"
	got := sections(text, 0)
	if len(got) != 2 || len(got[0].trail) != 0 || strings.Join(got[1].trail, "") != "Real" {
		t.Fatalf("sections = %+v", got)
	}
	if !strings.Contains(strings.Join(got[0].lines, "\n"), "# comment in code") {
		t.Errorf("code stays in its section: %+v", got[0])
	}
	// text that is only blank lines has no parent at all
	if ps, ks := cut(0, "T", "\n\n   \n", 0); len(ps) != 0 || len(ks) != 0 {
		t.Errorf("blank = %v %v", ps, ks)
	}
	// a skipped heading level is left out of the trail
	if s := sections("### Deep\nx", 0); len(s) != 1 || strings.Join(s[0].trail, "|") != "Deep" {
		t.Errorf("skipped levels = %+v", s)
	}
}

func TestLongSectionsAndParagraphsAreCut(t *testing.T) {
	para := strings.Repeat("word ", 300) // 1500 runes
	text := "# Long\n\n" + para + "\n\n" + para + "\n\n" + para + "\n\n" + strings.Repeat("x", parentMax+100)
	parents, kids := cut(0, "Long", text, 0)
	if len(parents) < 3 {
		t.Fatalf("a section over parentMax is cut: %d parents", len(parents))
	}
	for _, p := range parents {
		if n := len([]rune(p.text)); n > parentMax+len("# Long\n\n") {
			t.Errorf("a parent of %d runes", n)
		}
	}
	for _, k := range kids {
		if n := len([]rune(k.text)); n > childMax+len(para) && n > 3*childMax {
			t.Errorf("a child of %d runes", n)
		}
	}
}

func TestSplitRunesCutsAtSpacesAndHardWhenThereIsNone(t *testing.T) {
	for _, p := range splitRunes(strings.Repeat("ab ", 400), 100) {
		if len([]rune(p)) > 100 || strings.HasSuffix(p, " ") {
			t.Errorf("piece %q", p)
		}
	}
	if got := splitRunes("short", 100); len(got) != 1 || got[0] != "short" {
		t.Errorf("short = %v", got)
	}
	// no space to cut at: cut hard
	if got := splitRunes(strings.Repeat("y", 250), 100); len(got) != 3 || len(got[0]) != 100 {
		t.Errorf("hard cut = %v", got)
	}
}

func TestChildrenJoinShortParagraphsUntilTheyFill(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString(strings.Repeat("p", 90) + "\n\n")
	}
	_, kids := cut(0, "T", b.String(), 0)
	if len(kids) < 3 || len(kids) > 6 {
		t.Errorf("20 paragraphs of 90 runes make a few children, got %d", len(kids))
	}
	for _, k := range kids {
		if len([]rune(k.text)) > childMax+2 {
			t.Errorf("child of %d", len([]rune(k.text)))
		}
	}
	// a parent without a heading line keeps all its lines
	ps, ks := cut(0, "T", "plain text", 0)
	if len(ps) != 1 || len(ks) != 1 || ks[0].line != 1 {
		t.Errorf("headless = %v %v", ps, ks)
	}
}
