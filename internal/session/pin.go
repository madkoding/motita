package session

import (
	"strings"

	"github.com/madkoding/motita/internal/llm"
)

// The pinned plan: the plan the person is working from, kept verbatim in every request. A
// compaction summarises the conversation, and however well it is told to keep the plan, a
// summary is a retelling; the plan is the one text the agent must have word for word, so it
// stays outside what compaction touches.

// maxPinnedRunes bounds the pin, so a runaway answer cannot fill the window by itself.
const maxPinnedRunes = 6000

// Pin keeps text as the active plan, replacing the one pinned before. Blank unpins.
func (s *Session) Pin(text string) {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > maxPinnedRunes {
		text = string(r[:maxPinnedRunes]) + "\n[the plan continues; it was cut here]"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned = text
}

// Pinned is the active plan, or "".
func (s *Session) Pinned() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pinned
}

// pinnedNote frames the pin as the plan in force, not as something the user just said.
func (s *Session) pinnedNote() llm.Message {
	return llm.Message{Role: "system", Content: "## The active plan\n\n" +
		"The plan given earlier in this session, kept word for word so it is not lost when " +
		"older messages are summarised. Follow it unless the person changes it; a newer plan " +
		"in the conversation replaces it.\n\n" + s.pinned}
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
