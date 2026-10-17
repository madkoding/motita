package policy

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// Where a write lands
// ---------------------------------------------------------------------------

// writingForm decides the writers whose arguments say WHERE the change goes.
//
// The rule is the one a person would use: inside the directory the user pointed the agent
// at is work; outside it is a confirmation. The workspace is not a wall — the agent is
// often asked to touch a sibling — but crossing it is exactly the moment a person would
// want to be asked, and it is the only moment this package is trying to find.
//
// It returns false when the command's arguments do not locate the change at all, leaving
// the caller to give the unclassified answer. That keeps "I could not tell" separate from
// "I checked and it is outside".
func writingForm(command string, args []string, dir string) (Decision, bool) {
	name := baseName(command)
	targets := writeTargets(name, args)
	if len(targets) == 0 {
		return Decision{}, false
	}
	if outside := firstOutside(targets, dir); outside != "" {
		return Decision{Ask, fmt.Sprintf("%q writes to %q, which is outside the directory this task "+
			worksInFormat, name, outside, dir), "write-outside-workspace", false}, true
	}
	return Decision{Allow, fmt.Sprintf("%q writes inside the workspace (%s)", name, dir),
		"write-inside-workspace", false}, true
}

// writeTargets extracts the operands of the writers whose destination is in their
// arguments. The ones whose destination is decided by flags (`git` writing the index,
// `systemctl` changing a unit) are not here: they have no operand that locates the change,
// and they are answered by the unclassified rule rather than being guessed at.
func writeTargets(name string, args []string) []string {
	operands := rmTargets(args)
	switch name {
	case "chmod", "chown", "chgrp":
		// The first operand is a mode or an owner, not a path: reading `chmod 644` as
		// "writes to the file 644" is how a policy invents a target and then reports it
		// as being outside the workspace.
		if len(operands) > 0 {
			operands = operands[1:]
		}
	case "rm", "rmdir", "mkdir", "touch", "truncate", "tee", "mkfifo", "mknod":
		// Everything the command names is written to, which is what rmTargets already
		// returned.
	case "mv", "cp", "ln", "install":
		// The LAST operand is where it lands; the ones before it are what is moved, and
		// moving something is harmless next to writing where it is moved to. A flag's own
		// value (`install -m 755 a dst`) sits before the destination and does not change
		// which operand is last.
		if len(operands) > 0 {
			operands = operands[len(operands)-1:]
		}
	default:
		return nil
	}
	return operands
}

// systemTrees are the trees whose files the agent must never overwrite.
var systemTrees = []string{"/etc", "/usr", "/bin", "/sbin", "/boot", "/lib", "/lib64", "/var", "/root", "/opt", "/srv"}

// isRootWriteTarget decides whether a path that is about to be WRITTEN is one the agent must
// never write, whatever the operator chose.
//
// It is the write-side counterpart of isRootLike, and it is separate because the two ask
// different questions: isRootLike asks "may this be deleted", which is about trees; this asks
// "may this be overwritten", which is about a single file inside a system tree. `/etc/passwd`
// is the second without being the first.
func isRootWriteTarget(target string) bool {
	t := strings.TrimSpace(target)
	if t == "" {
		return false
	}
	if isHarmlessDevice(t) {
		return false
	}
	if t == "/" || t == "~" || t == "~/" || strings.HasPrefix(t, "~/") {
		return true
	}
	for _, abs := range writtenAndResolved(t) {
		if strings.HasPrefix(abs, userTempTree+"/") {
			continue
		}
		for _, tree := range systemTrees {
			if abs == tree || strings.HasPrefix(abs, tree+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// userTempTree holds each user's own temporary and cache directories on macOS ($TMPDIR is
// under it). It is inside /var, but it belongs to the user, not to the system.
const userTempTree = "/var/folders"

// writtenAndResolved returns a path as written (cleaned) and as it resolves through its links.
//
// Both are judged, because on macOS /etc, /var and /tmp are links into /private: `/etc` as
// written names the system tree and resolves to /private/etc, while a link to /private/etc
// names nothing and resolves to it. The resolved form is named by the tree users write.
func writtenAndResolved(path string) []string {
	written := filepath.Clean(path)
	resolved := written
	if r, err := filepath.EvalSymlinks(written); err == nil {
		resolved = r
	}
	for _, top := range []string{"/etc", "/var", "/tmp"} {
		if rest, ok := strings.CutPrefix(resolved, "/private"+top); ok && (rest == "" || rest[0] == '/') {
			resolved = top + rest
		}
	}
	return []string{written, resolved}
}

// isHarmlessDevice reports whether a device path is one of the streams or the null device,
// where writing goes nowhere or straight to a terminal the user is looking at.
//
// The distinction matters because the alternative is a policy that refuses `> /dev/null`,
// which appears in shell code constantly, and a policy that cries wolf on the common case is
// switched off before it ever protects anything.
func isHarmlessDevice(path string) bool {
	switch strings.TrimSuffix(path, "/") {
	case "/dev/null", "/dev/zero", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/full":
		return true
	default:
		// `/dev/fd/N` and `/dev/pts/N` are a descriptor and a terminal, not a disk.
		return strings.HasPrefix(path, "/dev/fd/") || strings.HasPrefix(path, "/dev/pts/")
	}
}

// firstOutside returns the first target that resolves outside dir, or "" when every one of
// them is inside.
//
// A target that does not exist yet (a file about to be created) is resolved as far as it
// can be: the deepest ancestor that does exist is followed through its links, and the
// remaining path is re-joined to it. Stopping at "the file is not there, so use it as
// written" is how a symlink INSIDE the workspace becomes a way to write outside it — the
// link is the part that exists, and it is the part that says where the write really lands.
func firstOutside(targets []string, dir string) string {
	if dir == "" {
		return ""
	}
	base := filepath.Clean(dir)
	if !filepath.IsAbs(base) {
		// firstOutside compares ABSOLUTE paths, so a relative workspace is resolved against
		// the process's own directory first. A caller is expected to hand in an absolute
		// workspace; this keeps the comparison from silently treating every target as
		// outside when it does not.
		if abs, err := filepath.Abs(base); err == nil {
			base = abs
		}
	}
	// The workspace itself is resolved once, and the same way as the targets: a workspace
	// reached through a link (on macOS, /tmp is one) must not make every path inside it look
	// like it is outside, even before the workspace exists.
	base = resolveAsFarAsPossible(base)
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "~") {
			// A home path written with a tilde is outside by construction.
			return t
		}
		abs := t
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, abs)
		}
		abs = resolveAsFarAsPossible(abs)
		if abs != base && !strings.HasPrefix(abs, base+string(filepath.Separator)) {
			return t
		}
	}
	return ""
}

// resolveAsFarAsPossible resolves the links of the deepest part of a path that exists, and
// re-joins the part that does not.
//
// The walk is bounded by the root, and the root is always resolvable, so the loop always
// terminates by finding an ancestor that exists: either the path exists, or one of its
// parents does, or the root does.
func resolveAsFarAsPossible(abs string) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	remainder := ""
	current := filepath.Clean(abs)
	for {
		parent := filepath.Dir(current)
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder))
		}
	}
}

// isInsideWorkspace reports whether a path resolves to something inside dir. It reuses the
// same resolution as the write checks, so a workspace reached through a symlink does not make
// every path inside it look like it is outside.
//
// With no workspace it answers FALSE, and that matters: firstOutside compares against dir and
// returns "" (meaning "nothing was outside") when dir is empty, so delegating the empty case
// would make every path in the system look like it lives inside "nothing". The caller guards
// this too, and a helper that lies about the empty case is exactly the kind of thing that gets
// copied somewhere the guard is missing.
func isInsideWorkspace(path, dir string) bool {
	if dir == "" {
		return false
	}
	if strings.HasPrefix(filepath.Clean(path), "~") {
		return false
	}
	return firstOutside([]string{path}, dir) == ""
}
