package gateway

import "testing"

// A model the provider lists but the catalogue lacks gets the tier and cost its name supports; one
// the catalogue knows, one named twice and one whose name says nothing add no entry.
func TestWithInferredFacts(t *testing.T) {
	known := []llmModelView{{ID: "gpt-5-mini", Label: "GPT-5 mini", Tier: "mid", Cost: "low"}}
	got := withInferredFacts("openai", known, []string{"gpt-5-mini", "gpt-5-nano", "gpt-5-nano", "whisper-1"})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if want := (llmModelView{ID: "gpt-5-nano", Label: "gpt-5-nano", Tier: "basic", Cost: "low"}); got[1] != want {
		t.Errorf("got %+v, want %+v", got[1], want)
	}
	// A local model with no size in its name is still known to be local.
	local := withInferredFacts("ollama", nil, []string{"mystery"})
	if len(local) != 1 || local[0].Cost != "local" || local[0].Tier != "" {
		t.Errorf("local: %+v", local)
	}
}
