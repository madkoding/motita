package gitx

import (
	"context"
	"strconv"
	"strings"
)

// FileChange is one file a run changed, in the terms an end user reads: what happened to it
// and how much. The lines themselves are NOT here - they are secondary, and a client that wants
// them asks for the diff.
type FileChange struct {
	Path string `json:"path"`
	// Status is "added", "modified", "deleted" or "renamed".
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
}

// HeadRev returns the commit dir is on, or "" when there is none (not a repository, or no commit
// yet). It is the mark a run takes before it starts, so what it changed is measured against
// where it began - commits it makes on the way included.
func HeadRev(ctx context.Context, dir string) string {
	out, err := execute(ctx, dir, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// ChangesSince lists the files that differ from rev in the tree at dir: committed since rev,
// modified in the working tree, and untracked. rev == "" measures against the empty tree, so
// a repository with no commit before the run reports everything as added.
func ChangesSince(ctx context.Context, dir, rev string) ([]FileChange, error) {
	if dir == "" {
		return nil, nil
	}
	if err := Repo(ctx, dir); err != nil {
		if isNoGit(err) {
			return nil, err
		}
		return nil, nil
	}
	base := rev
	if base == "" {
		// git's empty tree is not an object until something writes it: naming its hash in a
		// repository that never had one fails with "bad object".
		empty, err := noGitOr(ctx, "could not build the empty tree", dir, "hash-object", "-t", "tree", "-w", "--stdin")
		if err != nil {
			return nil, err
		}
		base = empty
	}
	status, err := noGitOr(ctx, "could not read the changed files", dir, "diff", "--name-status", "-M", base)
	if err != nil {
		return nil, err
	}
	stats, err := noGitOr(ctx, "could not count the changed lines", dir, "diff", "--numstat", "-M", base)
	if err != nil {
		return nil, err
	}
	untracked, err := noGitOr(ctx, "could not list the new files", dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return parseChanges(status, stats, untracked), nil
}

// parseChanges merges the three listings into one. Kept apart from the git calls so the
// format handling is testable without a repository.
func parseChanges(nameStatus, numstat, untracked string) []FileChange {
	counts := map[string][2]int{}
	for _, line := range strings.Split(numstat, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		a, _ := strconv.Atoi(f[0]) // "-" for binary files: counts as 0
		d, _ := strconv.Atoi(f[1])
		counts[lastPath(f[2])] = [2]int{a, d}
	}
	var out []FileChange
	seen := map[string]bool{}
	for _, line := range strings.Split(nameStatus, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		path := f[len(f)-1]
		st := "modified"
		switch f[0][0] {
		case 'A':
			st = "added"
		case 'D':
			st = "deleted"
		case 'R':
			st = "renamed"
		}
		c := counts[path]
		out = append(out, FileChange{Path: path, Status: st, Added: c[0], Deleted: c[1]})
		seen[path] = true
	}
	for _, p := range strings.Split(untracked, "\n") {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			out = append(out, FileChange{Path: p, Status: "added"})
		}
	}
	return out
}

// lastPath reads the destination of a numstat path, which for a rename is "old => new" or
// "dir/{old => new}/file".
func lastPath(p string) string {
	if i := strings.Index(p, "{"); i >= 0 {
		if j := strings.Index(p, "}"); j > i {
			inner := p[i+1 : j]
			if k := strings.Index(inner, " => "); k >= 0 {
				return strings.ReplaceAll(p[:i]+inner[k+4:]+p[j+1:], "//", "/")
			}
		}
	}
	if k := strings.Index(p, " => "); k >= 0 {
		return p[k+4:]
	}
	return p
}
