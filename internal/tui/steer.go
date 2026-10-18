package tui

import "sync"

// steerer is a running agent that takes the person's messages mid-task: see agent.Steer.
type steerer interface {
	Steer(text string) bool
}

// liveAgent is the agent of the task in flight, for the messages typed while it runs.
type liveAgent struct {
	mu sync.Mutex
	ag AgentRunner
}

func (l *liveAgent) set(ag AgentRunner) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ag = ag
}

func (l *liveAgent) get() AgentRunner {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ag
}

// Steer hands text to the task in flight and reports whether one took it. Without a task
// running, or with an agent that cannot be steered, it reports false and the caller queues the
// message as before.
func (r *AppRunner) Steer(text string) bool {
	st, ok := r.live.get().(steerer)
	return ok && st.Steer(text)
}
