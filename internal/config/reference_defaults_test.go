package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// docs/REFERENCE.md quotes two defaults an operator plans around: how many rounds a task gets,
// and where the shared skills live.
func TestTheReferenceQuotesTheRealDefaults(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "REFERENCE.md"))
	if err != nil {
		t.Fatalf("read REFERENCE.md: %v", err)
	}
	ref := string(raw)
	def := Default()

	m := regexp.MustCompile("`agent.max_steps` \\(default (\\d+)\\)").FindStringSubmatch(ref)
	if m == nil {
		t.Error("REFERENCE.md no longer quotes the agent.max_steps default; change this test with it")
	} else if m[1] != fmt.Sprint(def.Agent.MaxSteps) {
		t.Errorf("REFERENCE.md says agent.max_steps defaults to %s; Default() sets %d", m[1], def.Agent.MaxSteps)
	}

	rel, err := filepath.Rel(HomeDir(), def.Skills.Dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "~/" + filepath.ToSlash(rel)
	m = regexp.MustCompile("(?s)`skills.dir` is the procedure library.*?It defaults to `([^`]+)`").FindStringSubmatch(ref)
	if m == nil {
		t.Error("REFERENCE.md no longer quotes the skills.dir default; change this test with it")
	} else if m[1] != want {
		t.Errorf("REFERENCE.md says skills.dir defaults to %q; Default() uses %s", m[1], want)
	}
}
