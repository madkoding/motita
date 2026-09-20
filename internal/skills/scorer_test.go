package skills

import (
	"testing"
)

// The scoring hook: the library must use a value when it is given one, ignore it when it is
// not, and never let it outrank relevance.

// fakeScorer is the smallest thing that satisfies the interface, so the library's use of it can
// be tested without the reward package.
type fakeScorer map[string]float64

func (f fakeScorer) Value(name string) (float64, bool) {
	v, ok := f[name]
	return v, ok
}

// twoEqual builds a library with two documents that match a query identically, so the only
// thing that can separate them is the value.
func twoEqual(t *testing.T, a, b string) *Library {
	t.Helper()
	lib := New(t.TempDir())
	for _, n := range []string{a, b} {
		if _, err := lib.Save(n, "# Flash board\nHow to flash the board over serial.\n"); err != nil {
			t.Fatalf("Save(%s): %v", n, err)
		}
	}
	return lib
}

func TestScorerBreaksATie(t *testing.T) {
	lib := twoEqual(t, "alpha", "zeta")
	lib.Scorer = fakeScorer{"zeta": 0.9, "alpha": -0.5}

	hits, err := lib.Search("flash board serial", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected both, got %d", len(hits))
	}
	if hits[0].Name != "zeta" {
		t.Errorf("the higher value must win the tie, got %q first", hits[0].Name)
	}
}

func TestScorerWithEqualValuesFallsBackToName(t *testing.T) {
	// Deterministic: two skills the same value must not swap places between runs.
	lib := twoEqual(t, "alpha", "zeta")
	lib.Scorer = fakeScorer{"zeta": 0.5, "alpha": 0.5}

	hits, err := lib.Search("flash board serial", 5)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].Name != "alpha" {
		t.Errorf("equal values must sort by name, got %q first", hits[0].Name)
	}
}

func TestASkillWithNoHistorySortsAsZero(t *testing.T) {
	// "Never used" is not "failed": it sits at zero, so a skill with any positive history wins
	// the tie and one with negative history loses it.
	lib := twoEqual(t, "fresh", "proven")
	lib.Scorer = fakeScorer{"proven": 0.4} // "fresh" is absent

	hits, err := lib.Search("flash board serial", 5)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].Name != "proven" {
		t.Errorf("a proven skill must beat an unused one, got %q first", hits[0].Name)
	}
}

func TestAFreshSkillBeatsAFailedOne(t *testing.T) {
	lib := twoEqual(t, "fresh", "failed")
	lib.Scorer = fakeScorer{"failed": -0.7}

	hits, err := lib.Search("flash board serial", 5)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].Name != "fresh" {
		t.Errorf("an unused skill must beat a failed one, got %q first", hits[0].Name)
	}
}

func TestValueNeverOutranksRelevance(t *testing.T) {
	// The rule that keeps this from becoming a popularity ranking: a skill about something else
	// does not come first because it has been used a lot.
	lib := New(t.TempDir())
	if _, err := lib.Save("about-zephyr", "# Zephyr\nBuilding the zephyr firmware.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Save("about-bread", "# Bread\nBaking bread with zephyr flour.\n"); err != nil {
		t.Fatal(err)
	}
	// The less relevant one has a perfect record.
	lib.Scorer = fakeScorer{"about-bread": 1.0, "about-zephyr": 0.0}

	hits, err := lib.Search("zephyr firmware build", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected matches")
	}
	// Both mention zephyr; the one whose NAME and title match comes first regardless of value.
	if hits[0].Name != "about-zephyr" {
		t.Errorf("relevance must come first, got %q", hits[0].Name)
	}
}

func TestScorerIsOnlyConsultedForMatches(t *testing.T) {
	// A document that does not match the query is not scored at all: asking for a value for
	// every document in the library would be work for an answer that is thrown away.
	asked := map[string]bool{}
	lib := New(t.TempDir())
	for _, n := range []string{"matches", "unrelated"} {
		if _, err := lib.Save(n, "# Flash board\nHow to flash the board.\n"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.Save("nothing-like-it", "# Sourdough\nBaking bread.\n"); err != nil {
		t.Fatal(err)
	}
	lib.Scorer = countingScorer{asked: asked}

	_, err := lib.Search("flash board", 5)
	if err != nil {
		t.Fatal(err)
	}
	if asked["nothing-like-it"] {
		t.Error("a non-matching document must not be scored")
	}
	if !asked["matches"] {
		t.Error("a matching document must be scored")
	}
}

// countingScorer records who it was asked about.
type countingScorer struct{ asked map[string]bool }

func (c countingScorer) Value(name string) (float64, bool) {
	c.asked[name] = true
	return 0, false
}
