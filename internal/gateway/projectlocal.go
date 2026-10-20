package gateway

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// Repositories the user already has on the machine, used where they are, and the dependencies
// their projects need.

// isLocalRepo reports whether dir is the folder of one of the project's local repositories.
func (p *Project) isLocalRepo(dir string) bool {
	return slices.ContainsFunc(p.Repos, func(r ProjectRepo) bool { return r.Local && gitx.SamePath(r.Dir, dir) })
}

// missingDeps are the dependency folders the project's repositories need and do not have: the
// main repository's, and each other repository's where it is (the folder sessions link theirs from).
// Paths are relative to the project's folder, which is where a session finds them too.
func missingDeps(p *Project) []gitx.MissingDependency {
	if p == nil || p.Dir == "" {
		return nil
	}
	nested := map[string]bool{}
	for _, repo := range p.Repos {
		nested[strings.SplitN(filepath.ToSlash(repo.relPath(p.Dir)), "/", 2)[0]] = true
	}
	var out []gitx.MissingDependency
	for _, m := range gitx.MissingDependencies(p.Dir) {
		if !nested[strings.SplitN(m.Path, "/", 2)[0]] {
			out = append(out, m)
		}
	}
	for _, repo := range p.Repos {
		rel := filepath.ToSlash(repo.relPath(p.Dir))
		for _, m := range gitx.MissingDependencies(repo.Dir) {
			if m.Path != "." {
				m.Path = rel + "/" + m.Path
			} else {
				m.Path = rel
			}
			out = append(out, m)
		}
	}
	return out
}

// dependenciesNote tells a session's agent what is not installed, and what to do about it. "" when
// everything the project's manifests ask for is there.
//
// A missing dependency folder is the one thing that makes a check impossible to run, and the
// failure it invites is a check made to look as if it ran: a stand-in test runner that exits 0
// reports a pass that nobody measured. So the note says plainly to install, and to report when it
// cannot.
func dependenciesNote(p *Project) string {
	missing := missingDeps(p)
	if len(missing) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThese dependencies are NOT installed, so the project's tests and builds cannot run until they are:")
	for _, m := range missing {
		fmt.Fprintf(&b, "\n- %s: %s/ is missing, install it with `%s` in that folder", m.Path, m.Dir, m.Command)
	}
	b.WriteString("\nInstall them with the project's own tool first and run its own commands (for example " +
		"`php artisan test` in a Laravel project). If the install fails - no network, a registry that does not " +
		"answer - stop and tell the user exactly that; the check was NOT RUN. Never write a stand-in executable " +
		"or script that exits 0 in place of a missing test runner, and never report a check as passed that did " +
		"not run.")
	return b.String()
}
