package config

import "time"

// defaultLLM is the llm block of Default().
func defaultLLM() LLM {
	return LLM{
		Provider:        "openai",
		Model:           "gpt-4o-mini",
		BaseURL:         "https://api.openai.com/v1",
		MaxTokens:       16384,
		Timeout:         90 * time.Second,
		MaxAttempts:     5,
		BackoffInitial:  time.Second,
		BackoffMax:      60 * time.Second,
		TransientBudget: 5 * time.Minute,
		RetryAfterMax:   2 * time.Minute,
		Jitter:          0.25,
		Reasoning: Reasoning{
			Enabled: false,
			Level:   "medium",
		},
	}
}
