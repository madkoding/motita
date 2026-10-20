package repeats

import (
	"strings"
	"testing"
)

func TestDetectFindsARunRepeatedAcrossTurns(t *testing.T) {
	got := Detect([]Turn{
		{Ask: "fix a", Steps: []string{"git status", "go test ./a", "git add -A", "git commit -m a"}},
		{Ask: "fix b", Steps: []string{"ls", "go test ./b", "git add -A", "git commit -m b"}},
		{Ask: "fix c", Steps: []string{"cat x"}},
	})
	if len(got) != 1 {
		t.Fatalf("patterns = %+v", got)
	}
	p := got[0]
	if p.Turns != 2 || len(p.Steps) != 3 || p.Steps[0] != "go test ./a" || p.Steps[2] != "git commit -m a" {
		t.Errorf("pattern = %+v", p)
	}
	if len(p.Asks) != 2 || p.Asks[0] != "fix a" {
		t.Errorf("asks = %v", p.Asks)
	}
}

func TestDetectIgnoresWhatIsNotAProcedure(t *testing.T) {
	for name, turns := range map[string][]Turn{
		"nothing":          nil,
		"one turn":         {{Steps: []string{"a b", "c d", "a b", "c d"}}},
		"a single command": {{Steps: []string{"ls"}}, {Steps: []string{"ls"}}},
		"the same command": {{Steps: []string{"go test", "go test"}}, {Steps: []string{"go test", "go test"}}},
		"different runs":   {{Steps: []string{"a x", "b x"}}, {Steps: []string{"b x", "a x"}}},
	} {
		if got := Detect(turns); len(got) != 0 {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestDetectReportsAProcedureOnceAndMostRepeatedFirst(t *testing.T) {
	long := []string{"npm run build", "npm run test", "git push", "gh pr", "echo done", "ls -l"}
	turns := []Turn{{Ask: "1", Steps: long}, {Ask: "2", Steps: long}, {Ask: "3", Steps: long},
		{Ask: "4", Steps: []string{"x y", "z w"}}, {Ask: "5", Steps: []string{"x y", "z w"}}}
	// "x y","z w" in 2 turns; the long run (capped at MaxSteps) in 3 turns, its pieces dropped.
	got := Detect(turns)
	if len(got) != 3 {
		t.Fatalf("patterns = %+v", got)
	}
	if got[0].Turns != 3 || len(got[0].Steps) != MaxSteps || got[1].Turns != 3 || got[2].Turns != 2 {
		t.Errorf("order = %+v", got)
	}
	// "npm run build" and "npm run test" are different steps: the script's name is kept.
	if shape("npm run build ./x") == shape("npm run test") || shape("go test ./a") != shape("go test ./b") {
		t.Error("shape")
	}
	if shape("") != "" || shape("ls") != "ls" {
		t.Error("shape of a short command")
	}
}

func TestDetectCapsTheAsksKept(t *testing.T) {
	steps := []string{"a b", "c d"}
	var turns []Turn
	for i := 0; i < 6; i++ {
		turns = append(turns, Turn{Ask: "ask", Steps: steps})
	}
	if got := Detect(turns); len(got) != 1 || got[0].Turns != 6 || len(got[0].Asks) != maxAsks {
		t.Errorf("patterns = %+v", got)
	}
}

func TestSummary(t *testing.T) {
	if Summary(nil) != "" {
		t.Error("an empty summary is empty")
	}
	long := strings.Repeat("é", 300)
	s := Summary([]Pattern{{Steps: []string{"go test\nsecond line", long}, Turns: 2, Asks: []string{"fix it\nplease"}}})
	for _, want := range []string{"Pattern 1 (repeated in 2 turns)", "- go test\n", "asked: fix it\n", "…"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "second line") || strings.Contains(s, "please") {
		t.Errorf("only the first line of each is kept:\n%s", s)
	}
}
