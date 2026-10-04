package llm

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// The output limit of a model is not published by every provider (OpenAI lists model ids
// and nothing else), but a provider that refuses a request for asking too much says what
// the limit is. The client reads that number from the refusal, asks again once within it,
// and remembers it for the rest of its life, so a configured max_tokens higher than the
// model allows costs one refused request instead of every request.

// tokenKeys are the request fields that carry the output limit, at the top level of the body.
var tokenKeys = []string{"max_tokens", "max_completion_tokens", "max_output_tokens"}

// outputLimitField finds the output limit a request body asks for and returns it with a
// function that replaces it. Gemini nests it under generationConfig.
func outputLimitField(body map[string]any) (int, func(int)) {
	for _, k := range tokenKeys {
		if v, ok := body[k].(float64); ok {
			return int(v), func(n int) { body[k] = n }
		}
	}
	if gc, ok := body["generationConfig"].(map[string]any); ok {
		if v, ok := gc["maxOutputTokens"].(float64); ok {
			return int(v), func(n int) { gc["maxOutputTokens"] = n }
		}
	}
	return 0, nil
}

// clampOutputLimit lowers the output limit in an encoded request to the one learned from
// the provider. It returns the data unchanged when nothing is learned or nothing exceeds it.
func (c *Client) clampOutputLimit(data []byte) []byte {
	limit := int(c.outputCap.Load())
	if limit <= 0 {
		return data
	}
	return rewriteOutputLimit(data, func(asked int) (int, bool) { return limit, asked > limit })
}

// rewriteOutputLimit decodes data, lets decide pick a new limit from the asked one, and
// re-encodes. Anything that is not a JSON object with an output limit is returned as is.
func rewriteOutputLimit(data []byte, decide func(asked int) (int, bool)) []byte {
	var body map[string]any
	if json.Unmarshal(data, &body) != nil {
		return data
	}
	asked, set := outputLimitField(body)
	if set == nil {
		return data
	}
	n, ok := decide(asked)
	if !ok {
		return data
	}
	set(n)
	// A map decoded from JSON always encodes again.
	out, _ := json.Marshal(body)
	return out
}

// requestedOutputLimit is the output limit an encoded request asks for, 0 when it has none.
func requestedOutputLimit(data []byte) int {
	var body map[string]any
	if json.Unmarshal(data, &body) != nil {
		return 0
	}
	asked, _ := outputLimitField(body)
	return asked
}

var numberInText = regexp.MustCompile(`\d[\d,]*\d|\d`)

// limitFromRefusal reads the output limit out of a provider's refusal of a request that
// asked for `asked` tokens, or 0 when the message is not about the output limit.
//
// The messages differ ("supports at most 16384 completion tokens", "65536 > 32000, which
// is the maximum allowed number of output tokens"), so the rule is by shape: the refusal
// must talk about output tokens, must not be the context window, and the limit is the
// largest number in it that is below what was asked.
func limitFromRefusal(asked int, message string) int {
	m := strings.ToLower(message)
	about := false
	for _, w := range []string{"max_tokens", "max_completion_tokens", "maxoutputtokens", "max_output_tokens", "completion tokens", "output tokens"} {
		about = about || strings.Contains(m, w)
	}
	if !about || strings.Contains(m, "context length") || strings.Contains(m, "context window") {
		return 0
	}
	best := 0
	for _, s := range numberInText.FindAllString(message, -1) {
		n, err := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
		if err == nil && n >= 256 && n < asked && n > best {
			best = n
		}
	}
	return best
}
