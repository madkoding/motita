package tui

import "sync"

// steerer is a running turn that takes the person's messages mid-turn: see package steer.
type steerer interface {
	Steer(text string) bool
}

// unreader is a turn that hands back the messages it accepted and ended without reading.
type unreader interface {
	UnreadSteering() []string
}

// liveAgent is the turn in flight (a task's agent or a plan's planner), for the messages typed
// while it runs, and the messages earlier turns accepted and never read.
type liveAgent struct {
	mu     sync.Mutex
	ag     any
	unread []string
}

func (l *liveAgent) set(ag any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if u, ok := l.ag.(unreader); ok {
		l.unread = append(l.unread, u.UnreadSteering()...)
	}
	l.ag = ag
}

func (l *liveAgent) get() any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ag
}

// Steer hands text to the turn in flight and reports whether it took it. Without a turn running,
// or with one that cannot be steered, it reports false and the caller queues the message as before.
func (r *AppRunner) Steer(text string) bool {
	st, ok := r.live.get().(steerer)
	return ok && st.Steer(text)
}

// UnreadSteering returns, and forgets, the messages the turns that ended accepted and never read:
// the caller sends them as the next turn, so a message that was accepted is not lost.
func (r *AppRunner) UnreadSteering() []string {
	r.live.mu.Lock()
	defer r.live.mu.Unlock()
	out := r.live.unread
	r.live.unread = nil
	return out
}
