package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
)

// prUpdateEvery is how long after asking the host to update a branch it is not asked again: the
// host takes a moment to report the branch as up to date, and a branch that stays behind (a
// failing update) must not be retried at every poll.
const prUpdateEvery = 3 * time.Minute

// updateBase brings the branch of a pull request up to date with its base when the project asks for
// that and the base has moved on. The host merges the base into the branch: nothing is rebased or
// force-pushed, and a branch that conflicts stays as it is for the agent to resolve (see
// conflictTask). The CI runs again on what the host pushed, so the watch follows it from the start.
func (s *Server) updateBase(
	ctx context.Context, c *conversation, w *prWatchState, t prTarget, pr gitforge.PullRequest, m gitforge.MergeState,
) {
	p := s.projectOf(c.projectID)
	if m.Code != gitforge.MergeBehind || p == nil || !p.AutoUpdatePR || time.Since(w.updatedAt) < prUpdateEvery {
		return
	}
	w.updatedAt = time.Now()
	if err := t.api.UpdateBranch(ctx, t.remote, pr.Number); err != nil {
		if s.opts.Log != nil && !errors.Is(err, gitforge.ErrNoBranchUpdate) {
			s.opts.Log.Warn("could not update a pull request's branch",
				"session", c.id, "pr", pr.Number, "error", err.Error())
		}
		return
	}
	if s.opts.Log != nil {
		s.opts.Log.Info("updated a pull request's branch with its base", "session", c.id, "pr", pr.Number)
	}
	w.status = prFollowing
	s.publishPR(c, w, pr.Number)
}
