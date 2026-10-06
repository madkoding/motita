package gateway

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/schedule"
)

// The gateway follows the CI of a session's pull request on its own, so the loop does not depend
// on a browser tab staying open: when the CI fails it sends the agent to fix it, push after push,
// and it stops when the CI passes, when it has tried too many times, or when there is no CI.

// What the gateway is doing about a pull request.
const (
	prFollowing = "following" // waiting for the CI
	prFixing    = "fixing"    // the CI failed and the agent has been sent to fix it
	prPassed    = "passed"    // the CI passed: ready to merge
	prGaveUp    = "gave_up"   // the agent could not fix it in the allowed attempts
	prNoCI      = "no_ci"     // the repository has no checks to wait for
	prMerged    = "merged"    // the user merged it from here
	prBaseRed   = "base_red"  // the CI fails on the base branch too: not this change's fault
	prClosed    = "closed"    // it was closed on the host without being merged
)

const (
	defaultPRPollEvery = 20 * time.Second
	defaultPRMaxFixes  = 5
	// prNoneLimit is how many polls in a row may find no checks. A host reports them late for a
	// moment after a pull request opens; a repository with no CI reports none for ever.
	prNoneLimit = 6
	// prIdleLimit is how many polls a pull request whose CI passed is kept an eye on, waiting for
	// it to be merged or closed somewhere else: a day at the slow pace.
	prIdleLimit = 1440
	// prErrorLimit is how many polls in a row may fail before the host is given up on.
	prErrorLimit = 10
)

// PRWatchView is what the session list and the pull request endpoint say about the watch.
type PRWatchView struct {
	Status   string `json:"status"`
	Attempts int    `json:"attempts"`
	Max      int    `json:"max"`
	Number   int    `json:"number,omitempty"`
	// Next is the session opened by itself when the pull request was merged (the project's
	// auto_continue); empty otherwise. It is not kept across a restart.
	Next string `json:"next,omitempty"`
}

// prWatchRecord is the part of a watch that is saved with the session.
type prWatchRecord struct {
	Status   string `json:"status"`
	Attempts int    `json:"attempts"`
	Number   int    `json:"number,omitempty"`
	FixedKey string `json:"fixed_key,omitempty"`
}

// restorePRWatch gives a restored session the watch it had.
func (c *conversation) restorePRWatch(r *prWatchRecord, max int) {
	if r == nil {
		return
	}
	c.stateMu.Lock()
	c.prWatch = &PRWatchView{Status: r.Status, Attempts: r.Attempts, Max: max, Number: r.Number}
	c.prFixedKey = r.FixedKey
	c.stateMu.Unlock()
}

// prWatchState is the memory of one watch.
type prWatchState struct {
	status    string
	attempts  int
	fixedKey  string
	noneCount int
	errors    int
	idle      int
	// sig is a fingerprint of the last reading of the CI and same how many readings in a row
	// matched it: a CI that has not moved is asked about less often.
	sig  string
	same int
}

const (
	// prMaxQuiet and prMaxBackoff bound how far the pace may slow: a quiet CI is asked about at most
	// six times less often, a host that answers with errors (or a rate limit) up to sixteen times.
	prMaxQuiet   = 6
	prMaxBackoff = 16
)

// wait is how long to leave the host alone before the next reading. The base pace is for a CI that
// is moving; it slows while the readings do not change, and it backs off hard while the host fails,
// so many sessions do not run into the host's rate limit. A pull request whose CI passed is only
// waiting to be merged, and goes at a third of the pace.
func (w *prWatchState) wait(every time.Duration) time.Duration {
	switch {
	case w.errors > 0:
		f := 1 << min(w.errors, 4)
		return every * time.Duration(min(f, prMaxBackoff))
	case w.status == prPassed:
		return every * 3
	}
	return every * time.Duration(min(1+w.same/2, prMaxQuiet))
}

// see records a reading of the CI and reports whether it is the same as the one before.
func (w *prWatchState) see(ci *gitforge.CIStatus) {
	var b strings.Builder
	b.WriteString(ci.State + "|" + ci.Rev)
	for _, c := range ci.Checks {
		b.WriteString("|" + c.Name + "=" + c.State)
	}
	if sig := b.String(); sig == w.sig {
		w.same++
	} else {
		w.sig, w.same = sig, 0
	}
}

// ciKey identifies one run of the CI, so a failure is answered once per push and not once per
// poll. A host that gives no revision is told apart by which jobs failed.
func ciKey(ci *gitforge.CIStatus) string {
	if ci.Rev != "" {
		return ci.Rev
	}
	var failed []string
	for _, c := range ci.Checks {
		if c.State == gitforge.StateFailure {
			failed = append(failed, c.Name)
		}
	}
	sort.Strings(failed)
	return strings.Join(failed, ",")
}

// step folds one reading of the CI into the watch. It returns what happened that ends the watch
// (prPassed, prGaveUp, prNoCI), "fix" when the agent is to be sent, and "" otherwise.
func (w *prWatchState) step(ci *gitforge.CIStatus, max int) string {
	switch ci.State {
	case gitforge.StatePending:
		w.noneCount = 0
		w.status = prFollowing
	case gitforge.StateSuccess:
		w.status = prPassed
		return prPassed
	case gitforge.StateFailure:
		w.noneCount = 0
		key := ciKey(ci)
		if key == w.fixedKey {
			return "" // this push was already handed to the agent
		}
		w.fixedKey = key
		if w.attempts >= max {
			w.status = prGaveUp
			return prGaveUp
		}
		w.attempts++
		w.status = prFixing
		return "fix"
	default:
		w.noneCount++
		if w.noneCount >= prNoneLimit {
			w.status = prNoCI
			return prNoCI
		}
	}
	return ""
}

// maxPRFixesLimit is the most attempts a project may ask for: past it the agent is not fixing, it is looping.
const maxPRFixesLimit = 20

// prMaxFixes is how many times the agent is sent to fix this session's CI: the project's own
// setting, else the gateway's, else the default.
func (s *Server) prMaxFixes(c *conversation) int {
	if p := s.projectOf(c.projectID); p != nil && p.PRMaxFixes > 0 {
		return p.PRMaxFixes
	}
	if s.opts.PRMaxFixes > 0 {
		return s.opts.PRMaxFixes
	}
	return defaultPRMaxFixes
}

func (c *conversation) setPRWatch(v *PRWatchView) {
	c.stateMu.Lock()
	c.prWatch = v
	c.stateMu.Unlock()
}

func (c *conversation) prNumber() int {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.prWatch == nil {
		return 0
	}
	return c.prWatch.Number
}

func (c *conversation) prWatchView() *PRWatchView {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.prWatch == nil {
		return nil
	}
	v := *c.prWatch
	return &v
}

// publishPR makes the watch visible, and saves the session when it moved: the watch is what a
// restart resumes from.
func (s *Server) publishPR(c *conversation, w *prWatchState, number int) {
	view := &PRWatchView{Status: w.status, Attempts: w.attempts, Max: s.prMaxFixes(c), Number: number}
	c.stateMu.Lock()
	changed := c.prWatch == nil || c.prWatch.Status != view.Status || c.prWatch.Attempts != view.Attempts || c.prWatch.Number != view.Number
	c.prWatch, c.prFixedKey = view, w.fixedKey
	c.stateMu.Unlock()
	if changed {
		s.saveSession(c)
	}
}

// startPRWatch begins following the session's pull request, from the memory given (nil for a new
// watch). A watch already running is left alone.
func (s *Server) startPRWatch(c *conversation, from *prWatchState) {
	s.prMu.Lock()
	if s.prWatching == nil {
		s.prWatching = map[string]bool{}
	}
	if s.prWatching[c.id] {
		s.prMu.Unlock()
		return
	}
	s.prWatching[c.id] = true
	s.prMu.Unlock()

	w := from
	if w == nil {
		w = &prWatchState{status: prFollowing}
	}
	s.publishPR(c, w, c.prNumber())
	every := s.opts.PRPollEvery
	if every <= 0 {
		every = defaultPRPollEvery
	}
	go func() {
		defer func() {
			s.prMu.Lock()
			delete(s.prWatching, c.id)
			s.prMu.Unlock()
		}()
		for {
			// The pace follows what the host is doing (see wait).
			select {
			case <-s.baseCtx.Done():
				return
			case <-time.After(w.wait(every)):
			}
			if s.pollPR(c, w) {
				return
			}
		}
	}()
}

// pollPR asks the host once and acts on the answer. It reports whether the watch is over.
func (s *Server) pollPR(c *conversation, w *prWatchState) bool {
	if c.merged {
		if v := c.prWatchView(); v == nil || v.Status != prMerged {
			c.setPRWatch(nil)
		}
		return true
	}
	ctx := s.baseCtx
	t, perr := s.prTargetOf(ctx, c)
	if perr != nil {
		return s.prFailed(c, w)
	}
	if w.status == prPassed {
		return s.pollPassed(c, w, t)
	}
	pr, found, err := t.api.FindPR(ctx, t.remote, t.branch)
	if err != nil {
		return s.prFailed(c, w)
	}
	w.errors = 0
	if !found {
		// The agent is still opening it; when it has stopped and there is none, nothing is left to follow.
		if !c.isRunning() {
			c.setPRWatch(nil)
			return true
		}
		return false
	}
	ci, err := t.api.CI(ctx, t.remote, pr.Number)
	if err != nil {
		return s.prFailed(c, w)
	}
	w.see(&ci)
	verdict := w.step(&ci, s.prMaxFixes(c))
	s.publishPR(c, w, pr.Number)
	switch verdict {
	case prPassed:
		// Not over: unless the project merged it, the pull request is watched until it is merged or closed.
		s.autoMerge(c, t, pr)
		return c.merged
	case prGaveUp, prNoCI:
		return true
	case "fix":
		// A failure the base branch has too is not this change's to fix: spending the attempts on
		// it would only end in a pull request that cannot be made green from here.
		if names := ci.FailedNames(); len(names) > 0 && pr.Base != "" {
			if base, err := s.branchCI(ctx, t, pr.Base); err == nil && coversAll(base.FailedNames(), names) {
				w.attempts--
				w.status = prBaseRed
				s.publishPR(c, w, pr.Number)
				return true
			}
		}
		s.sendToAgent(c, ciFixTask(t.branch, pr))
	}
	return false
}

// pollPassed is the watch of a pull request whose CI passed: it waits for the pull request to be
// merged or closed, by the user in the host's own page or by anyone else, and says so when it is.
func (s *Server) pollPassed(c *conversation, w *prWatchState, t prTarget) bool {
	ctx := s.baseCtx
	if _, found, err := t.api.FindPR(ctx, t.remote, t.branch); err != nil {
		return s.prFailed(c, w)
	} else if found {
		w.errors = 0
		w.idle++
		return w.idle >= prIdleLimit
	}
	number := c.prNumber()
	if number == 0 {
		c.setPRWatch(nil)
		return true
	}
	st, err := t.api.PRState(ctx, t.remote, number)
	if err != nil {
		return s.prFailed(c, w)
	}
	switch st.State {
	case gitforge.PRMerged:
		s.finishMerge(ctx, c, number, st.SHA, "")
		return true
	case gitforge.PRClosed:
		w.status = prClosed
		s.publishPR(c, w, number)
		return true
	}
	return false
}

// coversAll reports whether every name in want is in have.
func coversAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// prFailed counts a poll the host did not answer, and ends the watch when it keeps not answering.
func (s *Server) prFailed(c *conversation, w *prWatchState) bool {
	w.errors++
	if w.errors < prErrorLimit {
		return false
	}
	c.setPRWatch(nil)
	return true
}

// sendToAgent gives the agent a task: it starts a run, or queues behind the one in flight.
func (s *Server) sendToAgent(c *conversation, task string) (started bool, position int) {
	if _, ok := s.startDetachedRun(c, task, schedule.KindTask, "", s.approverFactory(c)); ok {
		return true, 0
	}
	return false, c.pushQueue(task, false)
}

// handleMergePR merges the session's pull request once its CI has passed. It is the user's click:
// the agent never merges.
func (s *Server) handleMergePR(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	t, perr := s.prTargetOf(r.Context(), c)
	if perr != nil {
		s.writePRError(w, perr)
		return
	}
	pr, found, err := t.api.FindPR(r.Context(), t.remote, t.branch)
	if err != nil {
		s.writePRError(w, prHostError(t.api.Service, err))
		return
	}
	if !found {
		writeError(w, http.StatusConflict, "this session has no open pull request to merge")
		return
	}
	ci, err := t.api.CI(r.Context(), t.remote, pr.Number)
	if err != nil {
		s.writePRError(w, prHostError(t.api.Service, err))
		return
	}
	if ci.State == gitforge.StateFailure || ci.State == gitforge.StatePending {
		writeError(w, http.StatusConflict, "the CI of this pull request has not passed: it is "+ci.State)
		return
	}
	if state, err := t.api.MergeState(r.Context(), t.remote, pr.Number); err == nil && state.Refuses() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the host would not merge this pull request now: " + mergeRefusals[state.Code], "code": "pr_" + state.Code})
		return
	}
	if err := s.doMerge(r.Context(), c, t, pr); err != nil {
		s.writePRError(w, prHostError(t.api.Service, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"merged": true, "pr": pr})
}

// mergeRefusals say in words why the host would turn a merge down; the keys are the states that refuse.
var mergeRefusals = map[string]string{
	gitforge.MergeBlocked:  "it is missing an approval, a resolved discussion or a required check",
	gitforge.MergeConflict: "it conflicts with the branch it merges into",
	gitforge.MergeDraft:    "it is still a draft",
}

// doMerge merges the pull request the way the project asks, and from then on the session is the
// integrated one: read-only, and continuable from the updated base branch.
func (s *Server) doMerge(ctx context.Context, c *conversation, t prTarget, pr gitforge.PullRequest) error {
	opts := gitforge.MergeOptions{DeleteBranch: true, Branch: t.branch}
	if p := s.projectOf(c.projectID); p != nil {
		opts.Method = p.MergeMethod
	}
	sha, err := t.api.MergePR(ctx, t.remote, pr.Number, opts)
	if err != nil {
		return err
	}
	s.finishMerge(ctx, c, pr.Number, sha, pr.Base)
	return nil
}

// finishMerge is what a merged pull request leaves behind, whoever merged it: the session is the
// integrated one (read-only, continuable), the watch says merged, and the project's checkout is
// brought up to date so the next session starts from what was merged.
func (s *Server) finishMerge(ctx context.Context, c *conversation, number int, sha, base string) {
	if sha == "" {
		sha = fmt.Sprintf("pr-%d", number)
	}
	c.setPRWatch(&PRWatchView{Status: prMerged, Max: s.prMaxFixes(c), Number: number})
	c.setMerged(sha)
	s.saveSession(c)
	s.syncProject(ctx, c, base)
	s.autoContinue(ctx, c, number)
}

// autoContinue opens the next session when the project asks for that.
func (s *Server) autoContinue(ctx context.Context, c *conversation, number int) {
	p := s.projectOf(c.projectID)
	if p == nil || !p.AutoContinue {
		return
	}
	next, _, err := s.continueFrom(ctx, c)
	if err != nil {
		if s.opts.Log != nil {
			s.opts.Log.Warn("could not continue a merged session by itself", "session", c.id, "error", err.Error())
		}
		return
	}
	c.setPRWatch(&PRWatchView{Status: prMerged, Max: s.prMaxFixes(c), Number: number, Next: next.id})
}

// autoMerge merges a pull request whose CI just passed, when the project asks for that and the host
// accepts the merge. Anything else is left to the user: the watch ends at "passed" and the toast and
// the Merge button say so.
func (s *Server) autoMerge(c *conversation, t prTarget, pr gitforge.PullRequest) {
	p := s.projectOf(c.projectID)
	if p == nil || !p.AutoMerge {
		return
	}
	if state, err := t.api.MergeState(s.baseCtx, t.remote, pr.Number); err != nil || state.Refuses() {
		return
	}
	if err := s.doMerge(s.baseCtx, c, t, pr); err != nil && s.opts.Log != nil {
		s.opts.Log.Warn("could not merge a pull request automatically", "session", c.id, "pr", pr.Number, "error", err.Error())
	}
}

// baseCITTL is how long the CI of a base branch is remembered: every session of a repository asks
// the same question about the same branch, and its answer does not move by the minute.
const baseCITTL = time.Minute

type baseCIEntry struct {
	at time.Time
	ci gitforge.CIStatus
}

// branchCI is BranchCI shared between the sessions that follow pull requests into the same branch.
func (s *Server) branchCI(ctx context.Context, t prTarget, branch string) (gitforge.CIStatus, error) {
	key := t.api.Service.ID + " " + t.remote.Path + "@" + branch
	s.prMu.Lock()
	if e, ok := s.baseCI[key]; ok && time.Since(e.at) < baseCITTL {
		s.prMu.Unlock()
		return e.ci, nil
	}
	s.prMu.Unlock()
	ci, err := t.api.BranchCI(ctx, t.remote, branch)
	if err != nil {
		return ci, err
	}
	s.prMu.Lock()
	if s.baseCI == nil || len(s.baseCI) > 256 {
		s.baseCI = map[string]baseCIEntry{}
	}
	s.baseCI[key] = baseCIEntry{at: time.Now(), ci: ci}
	s.prMu.Unlock()
	return ci, nil
}
