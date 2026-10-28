package memory

import (
	"testing"
	"time"
)

func TestParseMetaReadsFrontMatter(t *testing.T) {
	mod := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	body := "---\nauthor: \"Ana\"\nDate: 2026-03-10\ntags: [salary, #offers, \"x\"]\n" +
		"no colon here\nmood: fine\n---\n# Title\n"
	m, rest, skipped := ParseMeta(body, mod)
	if m.Author != "Ana" || m.Date.Format("2006-01-02") != "2026-03-10" || len(m.Tags) != 3 || m.Tags[1] != "offers" {
		t.Errorf("meta = %+v", m)
	}
	if rest != "# Title\n" || skipped != 7 {
		t.Errorf("rest = %q, skipped = %d", rest, skipped)
	}
}

func TestParseMetaFallsBackToTheFile(t *testing.T) {
	mod := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, body := range []string{
		"", "plain", "---\nauthor: x\nnever closed\n", "---\n\n---",
		"line\n---\nauthor: x\n---\n",
	} {
		m, rest, skipped := ParseMeta(body, mod)
		if body == "---\n\n---" {
			if skipped != 3 {
				t.Errorf("empty front matter skipped = %d", skipped)
			}
			continue
		}
		if m.Author != "" || !m.Date.Equal(mod) || rest != body || skipped != 0 {
			t.Errorf("%q = %+v %q %d", body, m, rest, skipped)
		}
	}
	// a date that cannot be read keeps the file's
	m, _, _ := ParseMeta("---\ndate: someday\n---\nx", mod)
	if !m.Date.Equal(mod) {
		t.Errorf("date = %v", m.Date)
	}
	// a divider far down is not front matter
	far := "---\n"
	for i := 0; i < maxFrontLines+2; i++ {
		far += "k: v\n"
	}
	if _, _, skipped := ParseMeta(far+"---\nx", mod); skipped != 0 {
		t.Errorf("far divider skipped = %d", skipped)
	}
}

func TestParseDateLayouts(t *testing.T) {
	for _, s := range []string{"2026-03-10", "2026-03-10 14:30", "2026-03-10T14:30", "2026-03-10T14:30:00+02:00"} {
		if _, ok := ParseDate(s); !ok {
			t.Errorf("%q", s)
		}
	}
	if _, ok := ParseDate("March"); ok {
		t.Error("March is no date")
	}
}

func TestStampKeepsWhatThePersonWrote(t *testing.T) {
	now := time.Date(2026, 10, 28, 23, 0, 0, 0, time.UTC)
	if got := Stamp("# A", "motita", now); got != "---\nauthor: motita\ndate: 2026-10-28\n---\n\n# A" {
		t.Errorf("stamp = %q", got)
	}
	mine := "---\nauthor: Ana\n---\n# A"
	if got := Stamp(mine, "motita", now); got != mine {
		t.Errorf("stamped over the person's properties: %q", got)
	}
}

func TestNamespaceIsTheFirstFolder(t *testing.T) {
	for in, want := range map[string]string{"a.md": "", "HR/a.md": "HR", "HR/x/a.md": "HR", "/a.md": ""} {
		if got := Namespace(in); got != want {
			t.Errorf("Namespace(%q) = %q", in, got)
		}
	}
}

func TestTermsFoldAccentsStopwordsAndPlurals(t *testing.T) {
	got := terms("La política de Vacaciones, ÑOÑO café üñ boss class 7 a") // spanish-fixture: accent folding
	want := []string{"politica", "vacacione", "nono", "cafe", "boss", "class"}
	if len(got) != len(want) {
		t.Fatalf("terms = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("terms[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for i, w := range terms("áéíóú àèìòù äëïöü âêîôû") { // spanish-fixture: accent folding
		if w != "aeiou" {
			t.Errorf("folded[%d] = %q", i, w)
		}
	}
}
