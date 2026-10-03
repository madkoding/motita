package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/gitx"
)

// THE ANCHOR'S BASELINE: was a failing check already failing before the run?
//
// Reported from a real session: the project's gate (`make check`) already failed on the commit
// the run started from - tests that needed a tool the machine did not have, commits that the
// system git config insisted on signing. No change could ever pass it. The run was sent back
// three times, then spent about fifteen rounds proving by hand what the program can measure:
// `git stash`, a clean worktree in /tmp, the same tests run twice and the counts compared.
//
// So the run records the tree as it found it (a Snapshot: a commit of the working tree,
// uncommitted and untracked work included, that moves nothing the user can see), and when the
// anchor refuses a claim, ONLY the failing checks are run again on a clean checkout of that
// commit. A check that failed there too is not the run's doing; one that passed there is, and
// the rejection says so. It is lazy on purpose: a green gate never pays for it, and a red one
// pays once per check per run.

// baselineSeq makes the reference of each run unique within this process; the pid and the
// clock make it unique across processes sharing one repository (session worktrees share refs).
var baselineSeq atomic.Int64

// The steps that touch the repository and the disk, as variables so a test can make each one fail.
// Every failure is a state a real machine reaches (a locked index, a full disk, a commit pruned by
// hand) and each must leave the verdict exactly as the anchor gave it.
var (
	baselineSnapshot = gitx.Snapshot
	baselineCheckout = gitx.AddDetachedWorktree
	baselineTempDir  = os.MkdirTemp
)

// runBaseline is one run's record of the tree it started from, and what it has measured there.
type runBaseline struct {
	a     *Agent
	dir   string
	ref   string
	snap  string
	known map[string]bool // check name -> passed on the baseline
}

// startBaseline snapshots the working tree, or returns nil when there is no baseline to take:
// it is turned off, there is no anchor, or the workspace is not the root of a repository with
// a commit (a Snapshot needs one to hang from). nil is a valid *runBaseline: every method on it
// is a no-op, so the loop does not branch on it.
//
// Only the PROJECT'S OWN gate (anchor.kind auto) is compared. A gate the project declares - its
// tests, its lint, its build - is meant to be green before anyone touches it, so a red one is the
// environment's doing. A check written into the configuration (kind command, or checks) is the
// opposite: it usually states what the task must achieve, and it fails before the run BY DESIGN.
// Measured on a real run whose anchor was `test -s a.txt && test -s b.txt`: the baseline found it
// failing on the starting tree, called it "already failing", and accepted a claim of done with
// neither file written.
func (a *Agent) startBaseline(ctx context.Context) *runBaseline {
	if !a.cfg.Anchor.Baseline || !strings.EqualFold(a.cfg.Anchor.Kind, "auto") {
		return nil
	}
	dir := a.cfg.Agent.WorkspaceDir
	ref := fmt.Sprintf("refs/motita/baseline/%d-%d-%d", os.Getpid(), time.Now().UnixNano(), baselineSeq.Add(1))
	_, snap, err := baselineSnapshot(ctx, dir, ref)
	if err != nil || snap == "" {
		if err != nil {
			a.log.Warn("the anchor baseline could not be recorded; failing checks will not be compared",
				"dir", dir, "error", err)
		}
		return nil
	}
	return &runBaseline{a: a, dir: dir, ref: ref, snap: snap, known: map[string]bool{}}
}

// close drops the reference that kept the snapshot alive.
func (b *runBaseline) close(ctx context.Context) {
	if b == nil {
		return
	}
	gitx.DropRef(ctx, b.dir, b.ref)
}

// judge compares a refused validation with the baseline, measuring the checks it has not
// measured yet. A passing validation, or a run with no baseline, is returned untouched.
func (b *runBaseline) judge(ctx context.Context, v anchor.Result) anchor.Result {
	if b == nil || v.Pass {
		return v
	}
	var unknown []string
	for _, name := range v.Failing() {
		if _, ok := b.known[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		b.measure(ctx, unknown)
	}
	return anchor.WithBaseline(v, b.known)
}

// measure runs the named checks on a throwaway checkout of the snapshot and records the result.
// A checkout that cannot be made measures nothing, and nothing measured changes no verdict.
func (b *runBaseline) measure(ctx context.Context, names []string) {
	b.a.report("checking whether %s already failed before this change...", strings.Join(names, ", "))
	tmp, err := baselineTempDir("", "motita-baseline-")
	if err != nil {
		b.a.log.Warn("the anchor baseline has no place to check out", "error", err)
		return
	}
	defer os.RemoveAll(tmp)
	tree := filepath.Join(tmp, "tree")
	if err := baselineCheckout(ctx, b.dir, tree, b.snap); err != nil {
		b.a.log.Warn("the anchor baseline could not be checked out", "error", err)
		return
	}
	defer func() { _ = gitx.RemoveWorktree(ctx, b.dir, tree, true) }()
	// `git worktree add` copies nothing git ignores, so without the project's node_modules or
	// .venv a JS or Python check would fail on the baseline for want of a toolchain - and that
	// would read as "already failing" about a check the run may well have broken.
	gitx.LinkDependencyDirs(b.dir, tree)
	for name, passed := range anchor.New(b.a.cfg.Anchor, tree, b.a.sandbox).
		WithTools(b.a.cfg.Sandbox.ToolsDir, b.a.cfg.Sandbox.CheckTimeout).Rerun(ctx, names) {
		b.known[name] = passed
	}
	b.a.log.Info("anchor baseline measured", "checks", strings.Join(names, ","), "passed_before", fmt.Sprint(b.known))
}
