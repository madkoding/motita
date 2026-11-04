package llm

// The claude CLI refreshes its own OAuth token. When it says it could not, only a new login on
// that machine helps: this file names that, and remembers it for the settings.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// claudeSignInHint is what a lost sign-in tells the person to do. claude refreshes its own OAuth
// token; when it says the refresh failed, only a new login on that machine can fix it.
const claudeSignInHint = "the Claude subscription sign-in has expired or was revoked: " +
	"run `claude auth login` on the machine motita runs on, as the same user, then try again"

// claudeSignIn remembers whether claude's last answer said its sign-in is gone, so the settings can
// show the provider as signed out instead of ready. Any answer that gets through clears it.
var claudeSignIn struct {
	sync.Mutex
	lost time.Time
}

// ClaudeSignInLost reports whether claude last refused for want of a valid sign-in, and since when.
func ClaudeSignInLost() (time.Time, bool) {
	claudeSignIn.Lock()
	defer claudeSignIn.Unlock()
	return claudeSignIn.lost, !claudeSignIn.lost.IsZero()
}

// noteClaudeSignIn records whether claude's sign-in works, from the answer it just gave.
func noteClaudeSignIn(ok bool) {
	claudeSignIn.Lock()
	defer claudeSignIn.Unlock()
	switch {
	case ok:
		claudeSignIn.lost = time.Time{}
	case claudeSignIn.lost.IsZero():
		claudeSignIn.lost = time.Now()
	default:
		// Already lost: keep since when.
	}
}

// isSignInFailure says whether claude's error is about its login rather than the request: the
// authentication_failed kind, or the words claude prints when its token cannot be refreshed.
func isSignInFailure(kind, text string) bool {
	if kind == "authentication_failed" {
		return true
	}
	low := strings.ToLower(text)
	for _, w := range []string{"failed to authenticate", "oauth session expired", "please run /login",
		"invalid api key", "oauth token has expired"} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// signInError is claude's own report followed by what to do about it, recorded as a lost sign-in.
func signInError(report string) error {
	noteClaudeSignIn(false)
	return fatalError{fmt.Errorf("%s (%s)", report, claudeSignInHint)}
}

// resultError is a result event that ends a turn in failure, as a lost sign-in when it is one.
func resultError(subtype, text string) error {
	err := fmt.Errorf("claude failed (%s): %s", subtype, text)
	if isSignInFailure("", text) {
		return signInError(err.Error())
	}
	return err
}
