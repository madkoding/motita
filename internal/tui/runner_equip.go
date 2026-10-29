package tui

import (
	"context"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/reward"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// equipAgent hands the agent the same library and the same ledger Plan mode uses.
//
// Both modes reach one shelf of procedures: a procedure written down while working is
// available whichever mode does the work next, and a verdict lands on the same skills
// either way. Two libraries would be two bodies of knowledge that drift apart.
func (r *AppRunner) equipAgent(ag AgentRunner) {
	if lib := r.library(); lib != nil {
		if setter, ok := ag.(interface{ SetLibrary(*skills.Library) }); ok {
			setter.SetLibrary(lib)
		}
	}
	if setter, ok := ag.(interface{ SetMemory(*memory.Store) }); ok {
		setter.SetMemory(config.OpenMemory(r.Cfg.CodeIndex))
	}
	r.equipThreads(ag)
	if led := r.rewardOrNil(); led != nil {
		if setter, ok := ag.(interface{ SetReward(*reward.Ledger) }); ok {
			setter.SetReward(led)
		}
	}
	// The same sidecar the library screen reads, so what this run consults is counted.
	if u := r.procedures().Usage; u != nil {
		if setter, ok := ag.(interface{ SetUsage(*usage.Ledger) }); ok {
			setter.SetUsage(u)
		}
	}
}

// SetThreads installs the router the thread action goes through; the gateway sets it on the main
// session of a project only. Every agent the runner builds from then on is given it.
func (r *AppRunner) SetThreads(route func(command string) string) {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	r.threads = route
}

// equipThreads gives an agent the thread action, when the session has one.
func (r *AppRunner) equipThreads(ag AgentRunner) {
	setter, ok := ag.(interface{ SetThreads(func(string) string) })
	if !ok {
		return
	}
	r.sessionMu.Lock()
	route := r.threads
	r.sessionMu.Unlock()
	setter.SetThreads(route)
}
