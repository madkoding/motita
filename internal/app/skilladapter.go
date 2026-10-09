package app

import (
	"context"
	"fmt"

	"github.com/madkoding/motita/internal/curator"
	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/tui"
	"github.com/madkoding/motita/internal/usage"
)

// skillAdapter forwards the gateway's library questions to the ONE runner that owns the
// shared store. Every conversation this process holds shares that store (see
// startGateway), so this is not "a" library but "the" library.
//
// The translation lives HERE and nowhere else, which is the job this package already
// does everywhere: internal/gateway cannot import internal/tui without a cycle, so the
// gateway declares what it needs and this is where the two meet.
type skillAdapter struct{ runner *tui.AppRunner }

func (a skillAdapter) Skills() ([]skills.Skill, error)         { return a.runner.Skills() }
func (a skillAdapter) Skill(name string) (skills.Skill, error) { return a.runner.Skill(name) }
func (a skillAdapter) SaveSkill(n, b string) (skills.Skill, error) {
	return a.runner.SaveSkill(n, b)
}
func (a skillAdapter) SkillTelemetry() map[string]usage.Entry { return a.runner.SkillTelemetry() }
func (a skillAdapter) SetSkillPinned(n string, p bool) error  { return a.runner.SetSkillPinned(n, p) }
func (a skillAdapter) SetSkillDisabled(n string, d bool) error {
	return a.runner.SetSkillDisabled(n, d)
}
func (a skillAdapter) DeleteSkill(n string) error { return a.runner.DeleteSkill(n) }
func (a skillAdapter) ArchiveSkill(n string) error {
	return a.runner.ArchiveSkill(n)
}
func (a skillAdapter) RestoreSkill(n string) error       { return a.runner.RestoreSkill(n) }
func (a skillAdapter) ArchivedSkills() ([]string, error) { return a.runner.ArchivedSkills() }
func (a skillAdapter) ProposedSkills() ([]string, error) { return a.runner.ProposedSkills() }
func (a skillAdapter) ProposedSkill(n string) (skills.Skill, error) {
	return a.runner.ProposedSkill(n)
}
func (a skillAdapter) AcceptProposedSkill(n string) error { return a.runner.AcceptProposedSkill(n) }
func (a skillAdapter) RejectProposedSkill(n string) error { return a.runner.RejectProposedSkill(n) }

// curatorAdapter exposes the maintenance pass. The pass needs the runner builder, which
// lives in THIS package, so the curator is built here and the gateway only sees the two
// questions a front end can ask.
type curatorAdapter struct{ c *curator.Curator }

func (a curatorAdapter) CuratorStatus() string { return a.c.Status() }

func (a curatorAdapter) CuratorRun(ctx context.Context, consolidate, dryRun bool) (string, error) {
	report, err := a.c.RunWith(ctx, curator.RunOptions{Consolidate: consolidate, DryRun: dryRun})
	if err != nil {
		return "", err
	}
	what := "pass"
	if dryRun {
		// Said in the ANSWER and not only in the request, because the reply to a dry run is
		// the only thing the person who asked it will read.
		what = "preview (nothing was changed)"
	}
	return fmt.Sprintf("%s: %d stale, %d archived (%s)", what, report.Stale, report.Archived,
		report.RunAt.Format("2006-01-02 15:04")), nil
}

// Compile-time proof that the gateway can accept both: a struct that stops satisfying an
// interface must break the build, not the first request against a live gateway.
var (
	_ gateway.SkillService    = skillAdapter{}
	_ gateway.ProposalService = skillAdapter{}
	_ gateway.CuratorService  = curatorAdapter{}
)
