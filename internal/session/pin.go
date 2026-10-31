package session

import (
	"strings"

	"github.com/madkoding/motita/internal/llm"
)

// The pinned plan: the plan the person is working from, kept verbatim in every request. A
// compaction summarises the conversation, and however well it is told to keep the plan, a
// summary is a retelling; the plan is the one text the agent must have word for word, so it
// stays outside what compaction touches.
//
// A plan is pinned in one of two states. PROPOSED is a plan the agent gave because the person
// asked for one: it is kept so a long discussion does not lose it, and it is framed as not yet
// approved, so the agent does not start carrying it out. ACTIVE is a plan the person approved (a
// go-ahead) or wrote themselves: the agent follows it until it is done, then it is let go.

// Pin states, as the gateway reports them.
const (
	PinActive   = "active"
	PinProposed = "proposed"
)

// maxPinnedRunes bounds the pin, so a runaway answer cannot fill the window by itself.
const maxPinnedRunes = 6000

// boundPin trims text and cuts it at maxPinnedRunes.
func boundPin(text string) string {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > maxPinnedRunes {
		text = string(r[:maxPinnedRunes]) + "\n[the plan continues; it was cut here]"
	}
	return text
}

// Pin keeps text as the active plan, replacing the one pinned before. Blank unpins.
func (s *Session) Pin(text string) {
	text = boundPin(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned, s.proposed = text, false
}

// Propose keeps text as a plan the person has not approved yet. It never replaces an active
// plan: a new proposal is only an idea until the person approves it, and then it replaces the
// old plan through Pin.
func (s *Session) Propose(text string) {
	text = boundPin(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	if text == "" || (s.pinned != "" && !s.proposed) {
		return
	}
	s.pinned, s.proposed = text, true
}

// Pinned is the pinned plan, or "".
func (s *Session) Pinned() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pinned
}

// PinState is PinActive or PinProposed, or "" when nothing is pinned.
func (s *Session) PinState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.pinned == "":
		return ""
	case s.proposed:
		return PinProposed
	default:
		return PinActive
	}
}

// Release lets go of plan once it is done: the pin goes only when it is still that plan, so a
// plan the person changed meanwhile stays. It answers whether it let go.
func (s *Session) Release(plan string) bool {
	plan = boundPin(plan)
	s.mu.Lock()
	defer s.mu.Unlock()
	if plan == "" || s.pinned != plan {
		return false
	}
	s.pinned, s.proposed = "", false
	return true
}

// pinnedNote frames the pin as the plan in force, or as one still waiting for the person, never
// as something the user just said.
func (s *Session) pinnedNote() llm.Message {
	if s.proposed {
		return llm.Message{Role: "system", Content: "## The proposed plan\n\n" +
			"The plan you proposed earlier in this session, kept word for word so it is not lost " +
			"when older messages are summarised. The person has NOT approved it yet: discuss or " +
			"revise it when asked, and do not treat it as work to carry out.\n\n" + s.pinned}
	}
	return llm.Message{Role: "system", Content: "## The active plan\n\n" +
		"The plan the person approved earlier in this session, kept word for word so it is not " +
		"lost when older messages are summarised. Follow it unless the person changes it; a newer " +
		"approved plan replaces it, and it is let go once its work is done.\n\n" + s.pinned}
}

// pinShownLocked says whether the plan is still in the conversation word for word, in which case
// a second copy would only spend the window. It goes back in once a compaction folds it away.
// The caller holds the lock.
func (s *Session) pinShownLocked() bool {
	for _, m := range s.messages {
		if m.Role == "assistant" && strings.Contains(m.Content, s.pinned) {
			return true
		}
	}
	return false
}

// Carried is what a session holds beyond its messages: the summary of what compaction folded and
// the pinned plan. They are what a conversation needs to be continued after a restart; PinState
// says whether the plan was approved.
func (s *Session) Carried() (summary, pinned string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summary, s.pinned
}

// Restore puts back what Carried returned, in a session built again after a restart. A plan that
// was only proposed comes back proposed.
func (s *Session) Restore(summary, pinned string, proposed bool) {
	pinned = boundPin(pinned)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summary = strings.TrimSpace(summary)
	s.pinned, s.proposed = pinned, proposed && pinned != ""
}
