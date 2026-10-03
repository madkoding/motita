package agent

import (
	"sync"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

// THE FLEET: every agent of one run, and the snapshot the interfaces draw.
//
// A run that starts background agents (spawn_agent) has several things spending at once, and the
// person watching sees one progress line. The fleet is the list: the main agent and each agent it
// started, what each is for, how long it has been at it and what it has spent. It is published as
// an AgentsLine through the main agent's Progress - throttled, because a busy agent changes its
// activity several times a second, and ticked, because tokens and elapsed time move even when
// nothing else does.
//
// Nothing is published for a run that has no agents of its own and has not spent a counted token:
// a provider that reports no usage, on a task with no background agents, has nothing to show, and
// a list with one silent row would be noise on every task.

// The fleet's timing, as variables so a test does not wait seconds for a line.
var (
	// fleetThrottle is the shortest gap between two published snapshots.
	fleetThrottle = time.Second
	// fleetTick is how often a snapshot is published while any agent is running, so the clock
	// and the token counts move on screen even when no agent changes state.
	fleetTick = 2 * time.Second
	// fleetNow is the clock.
	fleetNow = time.Now
)

// fleetMember is one agent of the run. Its fields are guarded by the fleet's mutex; the meter is
// safe on its own, because the agent's model calls record into it without the fleet knowing.
type fleetMember struct {
	id, parent, purpose string
	state               string
	started, finished   time.Time
	meter               *llm.Meter
	round               int
	activity            string
	branch              string
	summary             string
}

// Fleet is the agents of one run. Every method is safe on a nil *Fleet, which is what an agent
// running outside a run (a phase called directly, a test) has.
type Fleet struct {
	mu          sync.Mutex
	members     []*fleetMember
	publish     func(line string)
	spawned     bool // an agent of the run's own was ever started
	lastPublish time.Time
	timerSet    bool
	closed      bool
	// emitMu orders the publications: a throttled one fired by a timer must not overtake a newer
	// one, or an interface would show the list going backwards.
	emitMu sync.Mutex
	stop   chan struct{}
	ticker sync.WaitGroup
}

// newFleet starts a fleet whose snapshots go to publish.
func newFleet(publish func(line string)) *Fleet {
	f := &Fleet{publish: publish, stop: make(chan struct{})}
	f.ticker.Add(1)
	go f.tick()
	return f
}

// tick publishes a snapshot every fleetTick while any agent is running.
func (f *Fleet) tick() {
	defer f.ticker.Done()
	t := time.NewTicker(fleetTick)
	defer t.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-t.C:
			if f.anyRunning() {
				f.publishNow()
			}
		}
	}
}

// add registers an agent and publishes the change.
func (f *Fleet) add(parent, id, purpose, branch string) *fleetMember {
	if f == nil {
		return nil
	}
	m := &fleetMember{id: id, parent: parent, purpose: purpose, branch: branch,
		state: AgentRunning, started: fleetNow(), meter: &llm.Meter{}}
	f.mu.Lock()
	f.members = append(f.members, m)
	if parent != "" {
		f.spawned = true
	}
	f.mu.Unlock()
	f.changed()
	return m
}

// update changes a member under the fleet's lock and publishes the change.
func (f *Fleet) update(m *fleetMember, change func(m *fleetMember)) {
	if f == nil || m == nil {
		return
	}
	f.mu.Lock()
	change(m)
	f.mu.Unlock()
	f.changed()
}

// setActivity records what an agent is doing now.
func (f *Fleet) setActivity(m *fleetMember, line string) {
	f.update(m, func(m *fleetMember) { m.activity = truncate(firstLineOf(line), 160) })
}

// setRound records which round an agent is on.
func (f *Fleet) setRound(m *fleetMember, round int) {
	f.update(m, func(m *fleetMember) { m.round = round })
}

// finish records how an agent ended.
func (f *Fleet) finish(m *fleetMember, state, summary string) {
	f.update(m, func(m *fleetMember) {
		m.state, m.finished = state, fleetNow()
		if summary != "" {
			m.summary = truncate(summary, 600)
		}
	})
}

// changed publishes now, or schedules a publication for when the throttle allows one.
func (f *Fleet) changed() {
	f.mu.Lock()
	if f.closed || !f.worthLocked() || f.timerSet {
		f.mu.Unlock()
		return
	}
	wait := fleetThrottle - fleetNow().Sub(f.lastPublish)
	if wait > 0 {
		f.timerSet = true
		time.AfterFunc(wait, func() {
			f.mu.Lock()
			f.timerSet = false
			f.mu.Unlock()
			f.publishNow()
		})
	}
	f.mu.Unlock()
	if wait <= 0 {
		f.publishNow()
	}
}

// worthLocked reports whether there is anything to show: an agent of the run's own, or tokens.
func (f *Fleet) worthLocked() bool {
	if f.spawned {
		return true
	}
	for _, m := range f.members {
		if m.meter.Calls() > 0 {
			return true
		}
	}
	return false
}

// publishNow sends the current snapshot, unless the fleet is closed or has nothing to show.
func (f *Fleet) publishNow() {
	f.emitMu.Lock()
	defer f.emitMu.Unlock()
	f.mu.Lock()
	if f.closed || !f.worthLocked() || f.publish == nil {
		f.mu.Unlock()
		return
	}
	f.lastPublish = fleetNow()
	snap := f.snapshotLocked()
	f.mu.Unlock()
	f.publish(AgentsLine(snap))
}

// anyRunning reports whether an agent of the run is still running.
func (f *Fleet) anyRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.members {
		if m.state == AgentRunning {
			return true
		}
	}
	return false
}

// Snapshot is the run's agents as the interfaces see them, in the order they started.
func (f *Fleet) Snapshot() []AgentInfo {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshotLocked()
}

func (f *Fleet) snapshotLocked() []AgentInfo {
	now := fleetNow()
	out := make([]AgentInfo, 0, len(f.members))
	for _, m := range f.members {
		end := now
		if !m.finished.IsZero() {
			end = m.finished
		}
		out = append(out, AgentInfo{
			ID: m.id, Parent: m.parent, Purpose: m.purpose, State: m.state,
			Started: m.started, Finished: m.finished, ElapsedMS: end.Sub(m.started).Milliseconds(),
			Tokens: m.meter.Usage(), Calls: m.meter.Calls(), Round: m.round,
			Activity: m.activity, Branch: m.branch, Summary: m.summary,
		})
	}
	return out
}

// close stops the ticker and publishes the final snapshot; nothing is published after it.
func (f *Fleet) close() {
	if f == nil {
		return
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()
	close(f.stop)
	f.ticker.Wait()
	f.publishNow()
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}
