package gitforge

import (
	"context"
	"errors"
	"net/http"
)

// ErrNoBranchUpdate means the host has no call that brings a pull request's branch up to date with
// its base by merging the base in; the branch is then left as it is.
var ErrNoBranchUpdate = errors.New("this git host cannot update a pull request's branch by itself")

// UpdateBranch asks the host to merge the pull request's base branch into its branch, which is
// what the "Update branch" button does. It never rewrites history: a branch that conflicts with its
// base is refused by the host and stays as it is. GitLab and Bitbucket only offer a rebase, which
// force-pushes, so they answer ErrNoBranchUpdate.
func (a API) UpdateBranch(ctx context.Context, r Remote, n int) error {
	switch a.Service.Kind {
	case KindGitLab, KindBitbucket:
		return ErrNoBranchUpdate
	case KindGitea:
		return a.do(ctx, http.MethodPost, a.pullsPath(r, n)+"/update?style=merge", nil, nil)
	default:
		return a.do(ctx, http.MethodPut, a.pullsPath(r, n)+"/update-branch", map[string]any{}, nil)
	}
}
