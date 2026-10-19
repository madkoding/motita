package gateway

import (
	"context"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/gitx"
)

// pendingForgeTimeout bounds the question to the git host: the preview must stay quick even when the
// host is slow, and an unanswered question only means the dialog does not offer the merge.
const pendingForgeTimeout = 5 * time.Second

// pendingCommit is one commit of the session's branch that the project's branch does not have.
type pendingCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// pendingIntegration is the work a session holds in commits that were never integrated.
type pendingIntegration struct {
	Base    string          `json:"base"`
	Count   int             `json:"count"`
	Commits []pendingCommit `json:"commits"`
}

// pendingPR is the pull request of the session's branch that is still open.
type pendingPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// pendingIntegrationOf names the commits the session's branch has and the project's base branch lacks.
//
// It answers nil for a session that has nothing to integrate, and for every case where it cannot tell:
// a session already merged, outside a project, or whose branch git cannot list. The dialog then asks
// only the deletion question, which is what it asked before this existed.
func (s *Server) pendingIntegrationOf(ctx context.Context, c *conversation) *pendingIntegration {
	if c.isMerged() || c.workspace == "" || c.projectDir == "" {
		return nil
	}
	base := gitx.Display(ctx, c.projectDir)
	// No base branch makes the listing fail, which is the same answer as nothing to integrate.
	ahead, _, err := gitx.CommitsBetween(ctx, c.projectDir, base, sessionBranch(c.id))
	if err != nil || len(ahead) == 0 {
		return nil
	}
	out := &pendingIntegration{Base: base, Count: len(ahead), Commits: []pendingCommit{}}
	for _, cm := range ahead {
		out.Commits = append(out.Commits, pendingCommit{SHA: cm.SHA, Subject: cm.Subject})
	}
	return out
}

// pendingPROf finds the session's open pull request, if the host says there is one.
//
// A host that is not connected, a repository without a remote or a slow answer all give nil: the
// preview never fails because of the host, it just does not offer to merge.
func (s *Server) pendingPROf(ctx context.Context, c *conversation) *pendingPR {
	if c.isMerged() || c.workspace == "" || c.projectID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, pendingForgeTimeout)
	defer cancel()
	t, perr := s.prTargetOf(ctx, c)
	if perr != nil {
		return nil
	}
	pr, found, err := t.api.FindPR(ctx, t.remote, t.branch)
	if err != nil || !found {
		return nil
	}
	return openPR(pr)
}

// openPR is the part of a pull request the dialog shows.
func openPR(pr gitforge.PullRequest) *pendingPR {
	return &pendingPR{Number: pr.Number, URL: pr.URL, Title: pr.Title}
}
