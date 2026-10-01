package gitx

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Change is one uncommitted change in a working tree, named so a person can read
// it.
//
// It exists because a COUNT is not enough to decide with. The count warns; the
// list is what lets someone answer "do I care about these?". Measured on the
// case that produced this: the two changes were a build artefact and a lock
// file, and no number could have said so.
type Change struct {
	// Path is the file's path, relative to the working tree, exactly as it is on
	// disk: no quoting, no escaped bytes.
	Path string `json:"path"`
	// Kind is one of: modified, added, deleted, renamed, copied, untracked,
	// conflicted, typechanged.
	Kind string `json:"kind"`
	// From is the previous path of a rename or a copy, and empty otherwise.
	From string `json:"from,omitempty"`
	// Code is git's own two-column status, kept because the caller may want to
	// distinguish a STAGED change from an unstaged one and the kind alone cannot.
	Code string `json:"code"`
}

// WorkingTreeChangeList names every uncommitted change in the working tree at
// dir, the way a person would read `git status`.
//
// It is the companion of WorkingTreeChanges, which counts the same thing. The
// two commands must not drift: both use `-uall` so an untracked directory
// contributes its files rather than itself, and the rule about a directory that
// is not a repository is the same - no working tree, no changes, and that is an
// empty list rather than an error.
//
// `-z` is what makes the paths usable. Without it git QUOTES a path that holds a
// space and escapes a non-ASCII byte as octal, so the name shown to a user is
// not the name on disk. Measured: `?? "dd/name with space.txt"`, quotes
// included. `-z` also terminates each record with a NUL and, for a rename,
// emits the two paths as two records - which is why the parser below reads
// records rather than lines.
//
// The output is read from an UNTRIMMED seam. For an unstaged modification git's
// status is " M", with a leading space, and the package's usual execute trims
// its output - which would drop that space from whichever record happens to
// come first. The parser would then read a staged and an unstaged file alike
// depending on their position in the list, which is a lie that changes with
// alphabetical order.
func WorkingTreeChangeList(ctx context.Context, dir string) ([]Change, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	if err := Repo(ctx, dir); err != nil {
		if isNoGit(err) {
			return nil, err
		}
		// Not a repository, or gone: no working tree, so no changes. The same
		// rule the count follows, for the same reason - this decorates a
		// confirmation, and decoration that fails should be absent.
		return nil, nil
	}
	raw, err := executeRaw(ctx, dir, "status", "--porcelain", "-uall", "-z")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrNoGit
		}
		return nil, describe("could not read the working tree's changes", string(raw), err)
	}
	return parseChangeList(string(raw)), nil
}

// executeRaw is execute WITHOUT the trim, for the one command whose output
// carries meaning in its leading whitespace.
func executeRaw(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	return execCommand(c, "git", append([]string{"-C", dir}, args...)...)
}

// parseChangeList reads `git status --porcelain -uall -z`.
//
// Each NUL-terminated record begins with a two-column status, a space, and the
// path. A rename or a copy is TWO records: the status record, then the original
// path as a record of its own. Reading the original as if it were another
// change would list a file twice and name a path the user never touched.
func parseChangeList(out string) []Change {
	records := strings.Split(out, "\x00")
	changes := make([]Change, 0, len(records))
	for i := 0; i < len(records); i++ {
		rec := records[i]
		// The trailing NUL leaves one empty record, and an empty record is not a
		// change.
		if rec == "" {
			continue
		}
		// A record shorter than "XY p" cannot be a change. Truncated output is
		// dropped rather than guessed at.
		if len(rec) < 4 {
			continue
		}
		code := rec[:2]
		path := rec[3:]
		// What a tool wrote in its HOME is not the user's work. A rename names a second path in
		// the next record, which is consumed with it.
		if IsToolHome(path) {
			if (code[0] == 'R' || code[0] == 'C' || code[1] == 'R' || code[1] == 'C') && i+1 < len(records) {
				i++
			}
			continue
		}
		c := Change{Path: path, Code: code, Kind: changeKind(code)}
		// For a rename or a copy, the NEXT record is the original path.
		if c.Kind == "renamed" || c.Kind == "copied" {
			if i+1 < len(records) && records[i+1] != "" {
				c.From = records[i+1]
				i++
			}
		}
		changes = append(changes, c)
	}
	return changes
}

// changeKind turns git's two-column status into one word.
//
// The columns are the index and the worktree, and the KIND is whichever of the
// two is set: "M " and " M" are both a modification, and reporting them
// differently would describe where the change sits rather than what it is.
func changeKind(code string) string {
	index, worktree := code[0], code[1]
	switch {
	case index == '?' && worktree == '?':
		return "untracked"
	case index == 'U' || worktree == 'U' ||
		(index == 'A' && worktree == 'A') || (index == 'D' && worktree == 'D'):
		return "conflicted"
	case index == 'R' || worktree == 'R':
		return "renamed"
	case index == 'C' || worktree == 'C':
		return "copied"
	case index == 'A' || worktree == 'A':
		return "added"
	case index == 'D' || worktree == 'D':
		return "deleted"
	case index == 'T' || worktree == 'T':
		return "typechanged"
	default:
		return "modified"
	}
}
