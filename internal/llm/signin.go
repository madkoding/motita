package llm

// SIGN-INS THAT STOPPED WORKING. A provider reached through a login (the claude CLI's own, or a
// stored Copilot, Codex, Qwen or Google login) works until its refresh token dies; from then on
// only a new sign-in helps. This remembers which logins are in that state, so the settings show
// them as expired and the page can offer to sign in again.

import (
	"sync"
	"time"
)

var signIns = struct {
	sync.Mutex
	lost map[string]time.Time
}{lost: map[string]time.Time{}}

// SignInLost reports whether provider's sign-in was last refused for good, and since when.
func SignInLost(provider string) (time.Time, bool) {
	signIns.Lock()
	defer signIns.Unlock()
	since, lost := signIns.lost[provider]
	return since, lost
}

// NoteSignIn records whether provider's sign-in works: a request or renewal that got through, a
// new sign-in or a disconnect (ok), or a refusal no retry can fix.
func NoteSignIn(provider string, ok bool) {
	signIns.Lock()
	defer signIns.Unlock()
	_, was := signIns.lost[provider]
	switch {
	case ok:
		delete(signIns.lost, provider)
	case !was:
		signIns.lost[provider] = time.Now()
	default:
		// Already lost: keep since when.
	}
}
