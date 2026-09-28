// Package projectskills scopes a procedure library to ONE project.
//
// A procedure is knowledge about a place. "How this repository builds", "which gate decides
// here", "the fixture that lies" — none of it is true for the next project, and offering it
// while the user works somewhere else is worse than offering nothing: the model reads a
// confident procedure about a different codebase and acts on it. That is not hypothetical. A
// procedure written for one repository outranked the whole library while the user was working
// on another, and the answer came back describing a project the user was not in.
//
// The rule this package implements:
//
//   - A session with NO project sees the shared shelf and the shipped procedures.
//   - A session INSIDE a project sees its own documents, plus the shared shelf, plus the
//     shipped procedures — with its own document winning over a shared or shipped one of the
//     same name.
//   - Nothing written inside a project is visible to another project, or to a session with no
//     project at all.
//
// It is a separate package rather than a field on skills.Library because the library has no
// business knowing what a project is, and because the two things it needs — which project a
// session belongs to, and the shared directory — are properties of the SESSION, not of the
// shelf. internal/skills stays the mechanism; this is the policy.
//
// The scoping is done by OVERLaying a second directory onto the library, which is the one
// arrangement that satisfies all of the above without the library knowing anything new:
//
//   - reads (Get, List, Search) consult the project's directory FIRST and fall back to the
//     shared one, so a project document wins and a shared document is still reachable;
//   - writes (Save, Archive, Delete) go to the project's directory, so a document learned
//     while working on a project is BORN scoped and cannot leak by being written somewhere
//     shared.
//
// The scoping is a function, so it is set on the library and can be replaced. It is never
// nil in a running gateway: a session that belongs to no project gets the shared directory as
// its only layer, which makes the "no project" case the degenerate one rather than a special
// case every call site has to remember.
package projectskills

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/skills"
)

// SharedDirName is the directory inside a project that holds the procedures written while
// working on it.
//
// It is dotted on purpose: the index skips dot-names, so a document here can never be offered
// to the model as if it were in the library the library was rooted at, and a project directory
// that is itself a procedure library cannot collide with it.
const SharedDirName = ".motita/skills"

// Scope makes a library serve a PROJECT's own procedures, layered over what it already serves.
//
// projectDir is the project's checkout. An empty projectDir is the "no project" case and is
// not an error: the shared shelf is then the project's whole scope, so a session with no
// project behaves exactly as it did before this package existed.
//
// The shared directory the library was rooted at is captured BEFORE the overlay is applied, so
// the two never become each other: applying this twice is harmless and the second call sees
// the same shared directory as the first.
func Scope(l *skills.Library, projectDir string) {
	if l == nil {
		return
	}
	if strings.TrimSpace(projectDir) == "" {
		// No project: a single layer, so every operation lands on the shared shelf and the
		// behaviour is the unscoped one.
		l.Overlay = nil
		return
	}
	shared := l.Root()
	l.Overlay = &skills.Overlay{Secondary: shared, Primary: filepath.Join(projectDir, SharedDirName)}
}

// Prepare creates the project's procedure directory. It is separated from Scope because
// scoping a library is a read-shaped act — it decides what is VISIBLE — while this one writes
// to the filesystem, and a session that only reads must not create directories as a side
// effect of looking at its shelf.
//
// It is idempotent and is called by the paths that WRITE: a save, an archive. The library
// already creates a directory on first write, so this exists only so the PROJECT directory is
// the one created.
func Prepare(projectDir string) error {
	if strings.TrimSpace(projectDir) == "" {
		return nil
	}
	return os.MkdirAll(filepath.Join(projectDir, SharedDirName), 0o755)
}

// ProjectDirFor picks the directory a session's procedures belong to, from the two paths a
// session can carry.
//
// The PROJECT's own checkout wins over the session's workspace. That ordering is the whole
// point: a session works inside a git worktree of its project, so the workspace is a temporary
// copy that is removed when the session ends — a procedure written there would be lost with
// it. The project's checkout is where the work outlives the session.
//
// Either being empty is normal: a session with no project has neither, and the caller gets ""
// which means "the shared shelf only".
func ProjectDirFor(projectDir, workspace string) string {
	if d := strings.TrimSpace(projectDir); d != "" {
		return d
	}
	return strings.TrimSpace(workspace)
}
