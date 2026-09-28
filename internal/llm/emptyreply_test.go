package llm

// The message that explains an empty reply, and the retry policy that follows from it.
//
// A real run failed three times with:
//
//	could not obtain the action from the LLM: all 3 attempts were exhausted:
//	OpenAI returned an empty response (finish_reason="tool_calls")
//
// Two defects are in that one sentence. The message said `tool_calls` while the call list was
// empty — a contradiction the reader cannot resolve — and the run retried it twice more, when a
// parse defect produces the same result every time.
//
// These tests are written against the message and the retryability, because those are what the
// next person to hit this has to work with.

import (
	"strings"
	"testing"
)

// TestTheEmptyReplyMessageNamesTheCause: each cause leads somewhere different, so the message
// has to say which one it was. A bare finish_reason does not: it is a word from the provider's
// vocabulary, and the reader is left to guess what it implies.
func TestTheEmptyReplyMessageNamesTheCause(t *testing.T) {
	cases := []struct {
		name         string
		finishReason string
		content      string
		toolCalls    int
		mustSay      []string
	}{
		{
			// The reported case. The message must not leave the reader with a contradiction:
			// it has to say the calls did not arrive AND that retrying will not help.
			name: "tool_calls that never arrived", finishReason: "tool_calls",
			toolCalls: 0,
			mustSay:   []string{"tool_calls", "did not arrive", "retrying cannot help"},
		},
		{
			name: "cut off by the budget", finishReason: "length", content: "partial answ",
			mustSay: []string{"length", "token budget", "truncate again"},
		},
		{
			name: "blocked by the provider", finishReason: "content_filter",
			mustSay: []string{"content_filter", "blocked"},
		},
		{
			name: "genuinely nothing", finishReason: "stop",
			mustSay: []string{"stop", "no text", "no tools"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := emptyReason(c.finishReason, c.content, c.toolCalls)
			for _, want := range c.mustSay {
				if !strings.Contains(got, want) {
					t.Errorf("the reason must mention %q, got %q", want, got)
				}
			}
		})
	}
}

// TestAParseDefectIsNotRetried: the retry policy is what turned a single defect into three
// identical failures and a confusing final message. A reply this side could not understand is
// not a transient failure, and `fatalError` is the mechanism that already says so in this
// package.
func TestAParseDefectIsNotRetried(t *testing.T) {
	// The shape callTools returns for the reported case.
	err := fatalError{errFromReason("tool_calls")}
	if retryable(err) {
		t.Error("a reply the provider says held tool calls, with none parsed, must not be retried: " +
			"the same parse happens again and the user waits three times as long for the same answer")
	}
}

// errFromReason builds the error the way callOpenAI does, so the test is about the real value
// rather than about a hand-written one that could drift from it.
func errFromReason(finishReason string) error {
	return &wrappedReason{emptyReason(finishReason, "", 0)}
}

type wrappedReason struct{ s string }

func (w *wrappedReason) Error() string { return "OpenAI returned no usable content: " + w.s }
