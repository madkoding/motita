package gitforge

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Following a pull request is the same questions asked again and again until one answer changes in
// a way somebody has to act on. Asking is mechanical and costs nothing but a few requests; acting
// is what needs the model. So the asking lives here, behind one interface every host implements,
// and a caller (the gateway, or `motita forge pr watch` in the agent's shell) only wakes the model
// for an Event.

// PRSource is what a watcher asks a host about a pull request. API implements it for every host it
// knows (GitHub, GitLab, Bitbucket, Gitea), each in its own dialect; a test or a new host only has
// to answer these four.
type PRSource interface {
	PRState(ctx context.Context, r Remote, n int) (PRState, error)
	CI(ctx context.Context, r Remote, n int) (CIStatus, error)
	MergeState(ctx context.Context, r Remote, n int) (MergeState, error)
	Activity(ctx context.Context, r Remote, n int) (Activity, error)
}

var _ PRSource = API{}

// Snapshot is one reading of a pull request.
type Snapshot struct {
	State    PRState
	CI       CIStatus
	Merge    MergeState
	Activity Activity
}

// ReadSnapshot reads a pull request once. One that is no longer open is only its state: there is
// nothing else left to act on.
func ReadSnapshot(ctx context.Context, src PRSource, r Remote, n int) (Snapshot, error) {
	var s Snapshot
	var err error
	if s.State, err = src.PRState(ctx, r, n); err != nil || s.State.State != PROpen {
		return s, err
	}
	if s.CI, err = src.CI(ctx, r, n); err != nil {
		return s, err
	}
	if s.Merge, err = src.MergeState(ctx, r, n); err != nil {
		return s, err
	}
	s.Activity, err = src.Activity(ctx, r, n)
	return s, err
}

// What can happen to a pull request that somebody may have to act on.
const (
	EventCIFailed = "ci_failed" // the CI of a new push failed
	EventCIPassed = "ci_passed" // the CI of a new push passed
	EventNoCI     = "no_ci"     // nothing has reported checks for a while: there is no CI to wait for
	EventConflict = "conflict"  // the branch stopped merging cleanly into its base
	EventReview   = "review"    // somebody commented or reviewed
	EventMerged   = "merged"
	EventClosed   = "closed" // closed without being merged
)

// Event is one thing that happened to a pull request.
type Event struct {
	Kind string `json:"kind"`
	// CI is the reading behind a CI event.
	CI *CIStatus `json:"ci,omitempty"`
	// Comments are the new ones behind a review event.
	Comments []Comment `json:"comments,omitempty"`
	// SHA is the merge commit of a merged pull request, when the host names it.
	SHA string `json:"sha,omitempty"`
}

// Actionable reports whether the event asks somebody to change the pull request.
func (e Event) Actionable() bool {
	return e.Kind == EventCIFailed || e.Kind == EventConflict || e.Kind == EventReview
}

// NoCILimit is how many readings in a row may find no checks before there is taken to be no CI. A
// host reports them late for a moment after a push; a repository with no CI reports none for ever.
const NoCILimit = 6

// Tracker remembers what was already reported about a pull request, so each thing is news once: a
// failure once per push, a conflict once until it clears, a comment once. The zero value is ready;
// its first reading is the baseline, and what was said before it is not news.
type Tracker struct {
	started   bool
	seen      map[string]bool
	ciKey     string
	conflict  bool
	noneCount int
}

// Observe folds a reading into the tracker and returns what is news in it, the end of the pull
// request first.
func (t *Tracker) Observe(s Snapshot) []Event {
	switch s.State.State {
	case PRMerged:
		return []Event{{Kind: EventMerged, SHA: s.State.SHA}}
	case PRClosed:
		return []Event{{Kind: EventClosed}}
	default:
		// Open: read on.
	}
	var out []Event
	if news := t.NewComments(s.Activity); len(news) > 0 {
		out = append(out, Event{Kind: EventReview, Comments: news})
	}
	if t.Conflict(s.Merge) {
		out = append(out, Event{Kind: EventConflict})
	}
	if e, ok := t.ci(s.CI); ok {
		out = append(out, e)
	}
	return out
}

// NewComments returns the comments the tracker has not seen, and remembers them. The first call
// only takes the baseline.
func (t *Tracker) NewComments(a Activity) []Comment {
	first := !t.started
	t.started = true
	if t.seen == nil {
		t.seen = map[string]bool{}
	}
	var news []Comment
	for _, c := range a.Comments {
		if t.seen[c.ID] {
			continue
		}
		t.seen[c.ID] = true
		if !first {
			news = append(news, c)
		}
	}
	return news
}

// Conflict reports whether the branch has just started to conflict with its base.
func (t *Tracker) Conflict(m MergeState) bool {
	was := t.conflict
	t.conflict = m.Code == MergeConflict
	return t.conflict && !was
}

// ci reports a CI that ended on a push not reported yet, or one that never shows up.
func (t *Tracker) ci(ci CIStatus) (Event, bool) {
	switch ci.State {
	case StateFailure, StateSuccess:
		t.noneCount = 0
		key := ci.State + "@" + CIKey(ci)
		if key == t.ciKey {
			return Event{}, false
		}
		t.ciKey = key
		kind := EventCIPassed
		if ci.State == StateFailure {
			kind = EventCIFailed
		}
		return Event{Kind: kind, CI: &ci}, true
	case StatePending:
		t.noneCount = 0
	default:
		t.noneCount++
		if t.noneCount == NoCILimit {
			return Event{Kind: EventNoCI}, true
		}
	}
	return Event{}, false
}

// CIKey identifies one run of the CI, so it is answered once per push and not once per reading. A
// host that gives no revision is told apart by which jobs failed.
func CIKey(ci CIStatus) string {
	if ci.Rev != "" {
		return ci.Rev
	}
	failed := ci.FailedNames()
	sort.Strings(failed)
	return strings.Join(failed, ",")
}

// Pace is how often a pull request is read: the base pace for a pull request that is moving, slower
// while the readings do not change, and backing off hard while the host fails, so that many watches
// stay inside the host's rate limit.
type Pace struct {
	Every time.Duration
	// MaxQuiet and MaxBackoff bound the slowing down: the factor a quiet pull request and a failing
	// host may reach.
	MaxQuiet, MaxBackoff int

	sig  string
	same int
}

// DefaultPace is the pace of a watch nobody tuned: twenty seconds, up to six times slower when
// quiet and sixteen times when the host fails.
func DefaultPace() Pace { return Pace{Every: 20 * time.Second, MaxQuiet: 6, MaxBackoff: 16} }

// See records a reading and whether it is the same as the one before.
func (p *Pace) See(s Snapshot) {
	var b strings.Builder
	b.WriteString(s.State.State + "|" + s.CI.State + "|" + s.CI.Rev + "|" + s.Merge.Code)
	for _, c := range s.CI.Checks {
		b.WriteString("|" + c.Name + "=" + c.State)
	}
	for _, c := range s.Activity.Comments {
		b.WriteString("|" + c.ID)
	}
	if sig := b.String(); sig == p.sig {
		p.same++
	} else {
		p.sig, p.same = sig, 0
	}
}

// Wait is how long to leave the host alone after a reading, given how many readings in a row failed.
func (p *Pace) Wait(errors int) time.Duration {
	if errors > 0 {
		return p.Every * time.Duration(min(1<<min(errors, 4), p.MaxBackoff))
	}
	return p.Every * time.Duration(min(1+p.same/2, p.MaxQuiet))
}

// ErrorLimit is how many readings in a row may fail before a watch gives the host up.
const ErrorLimit = 10

// Watcher follows one pull request until something happens. It asks the host, never the model.
type Watcher struct {
	Src    PRSource
	Remote Remote
	Number int
	Pace   Pace
	// Sleep waits between readings; nil is time.After. A test replaces it.
	Sleep func(time.Duration) <-chan time.Time
	// OnRead, when set, is called after every reading that answered, with the reading.
	OnRead func(Snapshot)

	Tracker Tracker
	errors  int
}

// Next reads the pull request until there is news and returns it. It returns the last error when
// the host keeps failing, and the context's error when it ends first. The first reading is taken at
// once and is the baseline for comments; its CI is news like any other.
func (w *Watcher) Next(ctx context.Context) ([]Event, error) {
	sleep := w.Sleep
	if sleep == nil {
		sleep = time.After
	}
	if w.Pace.Every <= 0 {
		w.Pace = DefaultPace()
	}
	first := true
	for {
		if !first {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-sleep(w.Pace.Wait(w.errors)):
			}
		}
		first = false
		s, err := ReadSnapshot(ctx, w.Src, w.Remote, w.Number)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			w.errors++
			if w.errors >= ErrorLimit {
				return nil, err
			}
			continue
		}
		w.errors = 0
		w.Pace.See(s)
		if w.OnRead != nil {
			w.OnRead(s)
		}
		if evs := w.Tracker.Observe(s); len(evs) > 0 {
			return evs, nil
		}
	}
}
