package llm

// THE USAGE BLOCK OF EACH PROVIDER, read into one Usage.
//
// Every API reports what a call consumed, each in its own words, and they disagree on one thing
// that matters for a count: whether the cached part of the prompt is inside the input figure.
// OpenAI, the Responses API and Gemini include it (and say how much of it was cached); Anthropic
// and claude report it apart. Usage keeps it apart everywhere, so Input + CacheRead is the whole
// prompt on every provider and nothing is counted twice.

// openAIUsage is the usage of a /chat/completions response or of its final stream chunk. The
// servers that speak the protocol name the cached part in their own ways, all inside
// prompt_tokens: OpenAI and Qwen as prompt_tokens_details.cached_tokens, DeepSeek as
// prompt_cache_hit_tokens; and the part WRITTEN to an explicit cache as
// prompt_tokens_details.cache_creation_input_tokens (DashScope) or cache_write_tokens (OpenRouter).
type openAIUsage struct {
	PromptTokens         int64                `json:"prompt_tokens"`
	CompletionTokens     int64                `json:"completion_tokens"`
	PromptCacheHitTokens int64                `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *openAIPromptDetails `json:"prompt_tokens_details"`
}

// openAIPromptDetails is the prompt_tokens_details of a /chat/completions usage.
type openAIPromptDetails struct {
	CachedTokens             int64 `json:"cached_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheWriteTokens         int64 `json:"cache_write_tokens"`
}

func (u *openAIUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	cached, written := u.PromptCacheHitTokens, int64(0)
	if d := u.PromptTokensDetails; d != nil {
		cached = max(cached, d.CachedTokens)
		written = max(d.CacheCreationInputTokens, d.CacheWriteTokens)
	}
	return Usage{Input: max(u.PromptTokens-cached-written, 0), Output: u.CompletionTokens,
		CacheRead: cached, CacheWrite: written}
}

// responsesUsage is the usage of a /responses response; cache_write_tokens is the part written
// to the cache, inside input_tokens like the cached part.
type responsesUsage struct {
	InputTokens        int64                  `json:"input_tokens"`
	OutputTokens       int64                  `json:"output_tokens"`
	InputTokensDetails *responsesInputDetails `json:"input_tokens_details"`
}

// responsesInputDetails is the input_tokens_details of a /responses usage.
type responsesInputDetails struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

func (u *responsesUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	var cached, written int64
	if d := u.InputTokensDetails; d != nil {
		cached, written = d.CachedTokens, d.CacheWriteTokens
	}
	return Usage{Input: max(u.InputTokens-cached-written, 0), Output: u.OutputTokens,
		CacheRead: cached, CacheWrite: written}
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
