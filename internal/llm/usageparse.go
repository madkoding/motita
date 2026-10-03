package llm

// THE USAGE BLOCK OF EACH PROVIDER, read into one Usage.
//
// Every API reports what a call consumed, each in its own words, and they disagree on one thing
// that matters for a count: whether the cached part of the prompt is inside the input figure.
// OpenAI, the Responses API and Gemini include it (and say how much of it was cached); Anthropic
// and claude report it apart. Usage keeps it apart everywhere, so Input + CacheRead is the whole
// prompt on every provider and nothing is counted twice.

// openAIUsage is the usage of a /chat/completions response or of its final stream chunk.
type openAIUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u *openAIUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	var cached int64
	if u.PromptTokensDetails != nil {
		cached = u.PromptTokensDetails.CachedTokens
	}
	return Usage{Input: u.PromptTokens - cached, Output: u.CompletionTokens, CacheRead: cached}
}

// responsesUsage is the usage of a /responses response.
type responsesUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	InputTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (u *responsesUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	var cached int64
	if u.InputTokensDetails != nil {
		cached = u.InputTokensDetails.CachedTokens
	}
	return Usage{Input: u.InputTokens - cached, Output: u.OutputTokens, CacheRead: cached}
}

// anthropicUsage is the usage of a Messages API response, and of claude's stream-json events,
// which carry the same object.
type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

func (u *anthropicUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{Input: u.InputTokens, Output: u.OutputTokens,
		CacheRead: u.CacheReadInputTokens, CacheWrite: u.CacheCreationInputTokens}
}

// geminiUsage is the usageMetadata of a generateContent response. Thinking tokens are billed as
// output, so they are counted as output.
type geminiUsage struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
}

func (u *geminiUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{Input: u.PromptTokenCount - u.CachedContentTokenCount,
		Output: u.CandidatesTokenCount + u.ThoughtsTokenCount, CacheRead: u.CachedContentTokenCount}
}
