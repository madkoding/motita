package sandbox

import "path/filepath"

// WithDir returns a sandbox identical to s - same limits, same isolation, same tools directory -
// whose commands run in dir.
//
// It is what a background agent runs in: its own worktree, under exactly the rules of the agent
// that started it. Building a second sandbox from the configuration would detect the isolation
// again and could land on a different answer (a cgroup the first one already holds), and the
// rules an agent runs under must not depend on which agent it is.
//
// The copy SHARES s's cgroup, so it is never closed: s owns it, and closing it twice would pull
// the group out from under the agent still using it. A relative dir is taken against s's base.
func (s *Sandbox) WithDir(dir string) *Sandbox {
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.base, dir)
	}
	c := *s
	c.op.Dir = dir
	c.base = filepath.Clean(dir)
	return &c
}
