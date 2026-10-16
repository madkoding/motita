package onboard

import "testing"

func TestInferFromTheNameOfTheModel(t *testing.T) {
	cases := []struct{ provider, id, tier, cost string }{
		{"openai", "gpt-5-nano", "basic", "low"},
		{"gemini", "gemini-2.0-flash-lite", "basic", "low"},
		{"openai", "gpt-5-mini-2025", "mid", "low"},
		{"anthropic", "claude-haiku-9", "mid", "low"},
		{"anthropic", "Claude-Opus-9", "frontier", "high"},
		{"anthropic", "claude-sonnet-9", "frontier", "mid"},
		{"gemini", "gemini-3-pro", "frontier", "mid"},
		{"openai", "gpt-5.4", "frontier", "mid"},
		{"openai", "gpt-4.1-2025", "mid", "mid"},
		{"openai", "whisper-1", "", ""},
		{"ollama", "gpt-oss:120b", "frontier", "local"},
		{"ollama", "qwen3:30b", "mid", "local"},
		{"ollama", "gemma4:7b", "basic", "local"},
		{"ollama", "gemma4:2.5b-q4", "basic", "local"},
		{"ollama", "mystery", "", "local"},
		// Hosted open models are billed: the size gives the tier and the cost stays unknown.
		{"ollama-cloud", "gpt-oss:120b", "frontier", ""},
		{"ollama-cloud", "mystery", "", ""},
	}
	for _, c := range cases {
		tier, cost := Infer(c.provider, c.id)
		if tier != c.tier || cost != c.cost {
			t.Errorf("%s/%s = %q, %q; want %q, %q", c.provider, c.id, tier, cost, c.tier, c.cost)
		}
	}
}
