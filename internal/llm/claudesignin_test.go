package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// resetClaudeSignIn forgets what an earlier test recorded about claude's sign-in.
func resetClaudeSignIn(t *testing.T) {
	t.Helper()
	noteClaudeSignIn(true)
	t.Cleanup(func() { noteClaudeSignIn(true) })
}

// TestClaudeCodeExpiredSignInSaysWhatToDo: claude's own refresh failing is reported with the
// command that fixes it, is not retried, and marks the sign-in lost until a turn gets through.
func TestClaudeCodeExpiredSignInSaysWhatToDo(t *testing.T) {
	expired := fmt.Sprintf(`{"type":"assistant","error":"authentication_failed","message":{"content":[{"type":"text","text":%q}]}}`,
		"Failed to authenticate: OAuth session expired and could not be refreshed")
	for name, script := range map[string]string{
		"assistant error": expired,
		"error result": `{"type":"result","subtype":"error_during_execution","is_error":true,` +
			`"result":"Invalid API key · Please run /login"}`,
	} {
		t.Run(name, func(t *testing.T) {
			resetClaudeSignIn(t)
			c, _ := claudeTestClient(t, script, nil)
			_, err := c.callClaudeCode(context.Background(), hi, nil, nil)
			if err == nil || !strings.Contains(err.Error(), "run `claude auth login` on the machine motita runs on") {
				t.Fatalf("err = %v, want the way out named", err)
			}
			if retryable(err) {
				t.Errorf("err = %v: a lost sign-in cannot be retried away", err)
			}
			since, lost := ClaudeSignInLost()
			if !lost || time.Since(since) > time.Minute {
				t.Fatalf("lost = %v since %v, want it recorded now", lost, since)
			}
			// A second refusal keeps the first time.
			_, _ = c.callClaudeCode(context.Background(), hi, nil, nil)
			if again, _ := ClaudeSignInLost(); !again.Equal(since) {
				t.Errorf("since moved from %v to %v", since, again)
			}
		})
	}
}

// TestClaudeCodeAnswerClearsALostSignIn: once claude answers again, the sign-in is fine.
func TestClaudeCodeAnswerClearsALostSignIn(t *testing.T) {
	resetClaudeSignIn(t)
	noteClaudeSignIn(false)
	c, _ := claudeTestClient(t, lines(assistantText("ok"), `{"type":"result","subtype":"success","num_turns":1}`), nil)
	if _, err := c.callClaudeCode(context.Background(), hi, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, lost := ClaudeSignInLost(); lost {
		t.Error("the sign-in is still marked lost after an answer")
	}
}

func TestIsSignInFailure(t *testing.T) {
	for _, tc := range []struct {
		kind, text string
		want       bool
	}{
		{"authentication_failed", "", true},
		{"", "Failed to authenticate. API Error: 401", true},
		{"", "OAuth token has expired", true},
		{"rate_limit", "slow down", false},
		{"", "the model is busy", false},
	} {
		if got := isSignInFailure(tc.kind, tc.text); got != tc.want {
			t.Errorf("isSignInFailure(%q, %q) = %v, want %v", tc.kind, tc.text, got, tc.want)
		}
	}
}
