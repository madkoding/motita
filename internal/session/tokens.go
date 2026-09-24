// Package session keeps a conversation across turns.
//
// Without it every turn is amnesiac: the planner built its message list from scratch on
// each call, so a question could not refer to the previous answer, and the only
// concession to growth was to drop the oldest messages without a word. A session owns
// the history, measures it against the model's context window, and compacts it before it
// overflows — replacing what it drops with a summary, so the agent continues the task
// instead of wandering.
package session

import "github.com/madkoding/motita/internal/llm"

// EstimateTokens is how many tokens a message list is likely to cost.
//
// It is an ESTIMATE, and the interface says so: a real count needs the provider's
// tokenizer, and this module depends on nothing. The heuristic is the accepted rule of
// thumb — roughly four characters per token for prose, three for the dense text that
// fills an agent's history (JSON arguments, file paths, code) — plus a flat cost per
// message for the role and framing the protocol adds.
//
// The estimate is deliberately CONSERVATIVE: it over-counts short strings and counts
// every rune, so it errs towards compacting early rather than discovering the overflow
// as a provider error halfway through an answer.
func EstimateTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += estimateMessage(m)
	}
	return total
}

// estimateMessage measures one message, including the tool calls it may carry.
func estimateMessage(m llm.Message) int {
	// The framing the API adds around a message: role, separators, overhead.
	const framing = 4

	n := framing + estimateText(m.Content)
	for _, call := range m.ToolCalls {
		// A tool call is a name plus a JSON argument object plus an id.
		n += framing + estimateText(call.Function.Name) + estimateText(string(call.Function.Arguments)) + estimateText(call.ID)
	}
	return n
}

// estimateText estimates a bare string: roughly four characters per token for ordinary
// prose, three when the text looks like the machine-written material an agent mostly
// carries — JSON, file paths, code.
//
// The distinction matters because the gap between the two is 33%, which on a 32k window is
// ten thousand tokens: the difference between compacting in time and being refused by the
// provider halfway through an answer.
//
// There is deliberately NO "at least one token per word" floor. It looks prudent and is the
// opposite: it makes every unspaced string — a JSON blob, a long path, a hash — cost one
// token per character, so the estimate stops distinguishing dense material from prose and
// over-counts machine text by a factor of three. The estimate is already conservative by
// dividing by three rather than four for exactly that material.
func estimateText(s string) int {
	if s == "" {
		return 0
	}
	runes, dense := 0, 0
	for _, r := range s {
		runes++
		switch {
		case r >= '0' && r <= '9':
			dense++
		case r == '{' || r == '}' || r == '[' || r == ']' || r == '"' || r == ':' ||
			r == ',' || r == ';' || r == '=' || r == '/' || r == '\\' || r == '_':
			dense++
		case r == '\n' || r == '	':
			dense++
		}
	}

	perToken := 4
	if dense*2 > runes {
		perToken = 3
	}
	n := runes / perToken
	if n == 0 {
		// A non-empty string costs something: the framing around it is real even when the
		// text is a single character.
		n = 1
	}
	return n
}
