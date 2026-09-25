package gitx

import (
	"context"
	"strings"
)

// WorkingTreeChanges counts the uncommitted changes in the working tree at dir:
// files git reports as modified, staged, added, deleted, renamed or untracked.
//
// The count is `-uall`, so an untracked DIRECTORY contributes its files rather
// than the directory itself. Measured on git 2.47.3: a folder with three new
// files is 1 line under plain --porcelain and 3 lines under -uall. The number is
// shown to a user as "how much have I changed", and 1 for three new files is a
// number that misleads them about their own work.
//
// Untracked files COUNT. An agent's new file is work, and a count that skipped
// it would report "no changes" for a session that has plainly written something.
//
// A directory that is not a repository has no working tree to count, and the
// answer is 0 rather than an error: the count decorates a badge, and a badge
// that fails should be absent, not fatal. Losing git IS reported, so a caller
// cannot mistake a broken toolchain for a clean tree.
func WorkingTreeChanges(ctx context.Context, dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	if err := Repo(ctx, dir); err != nil {
		if isNoGit(err) {
			return 0, err
		}
		return 0, nil
	}
	out, err := noGitOr(ctx, "could not read the working tree's changes", dir, "status", "--porcelain", "-uall")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}
