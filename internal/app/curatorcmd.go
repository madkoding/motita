package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/curator"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/tui"
)

// curatorActions is the list every rejection prints. It is ONE constant so the parser's
// message and the help below cannot drift into disagreeing about what exists.
const curatorActions = "status, run, pin, unpin, restore, list-archived, list-proposed, show-proposed, accept or reject"

const curatorCommandHelp = `Usage: motita curator <action>

Actions:
  status                 the thresholds, the last run and the lifecycle counts
  run                    run one maintenance pass now
  run --consolidate      force the LLM consolidation pass too
  run --dry-run          report what a pass would do, and change nothing
  pin <skill>            exempt a skill from every automatic transition
  unpin <skill>          remove that exemption
  restore <skill>        bring an archived skill back
  list-archived          everything in the skills archive
  list-proposed          skills the background review proposed, waiting for you
  show-proposed <skill>  read a proposed skill before deciding
  accept <skill>         put a proposed skill in the library, where sessions use it
  reject <skill>         delete a proposed skill
`

// runCuratorCommand dispatches `motita curator <action>`.
//
// It pays for neither the reasoning engine nor a sandbox, which is the point of a
// subcommand: tidying the library must not require a configured model. The engine is
// built ONLY for a consolidation run, the one path that talks to a model.
func (op Options) runCuratorCommand(ctx context.Context, fl flags) int {
	cfg, code := op.curatorConfig(fl)
	if code != Success {
		return code
	}
	log, err := op.newLogger(cfg.Agent)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}
	defer func() { _ = log.Close() }()

	procs := procedures.Open(cfg, log)

	engine, code := op.consolidationEngine(cfg, fl, log)
	if code != Success {
		return code
	}
	c := curator.New(cfg.Curator, procs, engine, log, buildSkillRunner)

	// The action is read the same way parse validated it and the same way the gateway's
	// dispatch reads its own: lower-cased and trimmed, because a user who types
	// `curator STATUS ` means the same thing.
	switch strings.ToLower(strings.TrimSpace(fl.curatorAction)) {
	case "status":
		fmt.Fprint(op.Out, c.Status())
		return Success
	case "run":
		return op.curatorRun(ctx, c, fl)
	case "pin":
		return op.reportSkillAction(c.Pin(fl.curatorSkill), fl.curatorSkill, "pinned")
	case "unpin":
		return op.reportSkillAction(c.Unpin(fl.curatorSkill), fl.curatorSkill, "unpinned")
	case "restore":
		return op.reportSkillAction(c.Restore(fl.curatorSkill), fl.curatorSkill, "restored")
	case "list-archived":
		names, err := c.ListArchived()
		return op.printNames(names, err, "the archive is empty.")
	case "list-proposed":
		names, err := procs.Library.Proposed()
		return op.printNames(names, err, "no skill is waiting for review.")
	case "show-proposed":
		return op.showProposal(procs, fl.curatorSkill)
	case "accept":
		return op.reportSkillAction(procs.Library.AcceptProposal(fl.curatorSkill), fl.curatorSkill, "accepted")
	case "reject":
		return op.reportSkillAction(procs.Library.RejectProposal(fl.curatorSkill), fl.curatorSkill, "rejected")
	default:
		// Unreachable through the command line, and kept because this function must not
		// DEPEND on parse having validated it: a second caller that forgot would fall off
		// the end of this switch into doing nothing, the quietest possible failure. The
		// repository keeps branches for exactly this reason already (see runGatewayCommand).
		fmt.Fprint(op.Out, curatorCommandHelp)
		return Success
	}
}

// consolidationEngine builds the reasoning engine for a consolidation run; any other action
// gets none, because nothing else calls a model.
func (op Options) consolidationEngine(cfg config.Config, fl flags, log *logx.Logger) (*llm.Client, int) {
	var engine *llm.Client
	var err error
	if fl.curatorConsolidate {
		engine, err = op.newEngine(cfg.LLM, log)
	}
	if err != nil {
		fmt.Fprintf(op.Err, "❌ the consolidation pass needs a working engine: %v\n", err)
		return nil, ConfigError
	}
	return engine, Success
}

// curatorRun runs one curator pass, or its preview, and reports what it did.
func (op Options) curatorRun(ctx context.Context, c *curator.Curator, fl flags) int {
	report, err := c.RunWith(ctx, curator.RunOptions{
		Consolidate: fl.curatorConsolidate,
		DryRun:      fl.curatorDryRun,
	})
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return RunError
	}
	what := "pass"
	if fl.curatorDryRun {
		what = "preview (nothing was changed)"
	}
	fmt.Fprintf(op.Out, "%s: %d stale, %d archived\n", what, report.Stale, report.Archived)
	return Success
}

// printNames prints a list of skill names one per line, or empty when there is none.
func (op Options) printNames(names []string, err error, empty string) int {
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return RunError
	}
	if len(names) == 0 {
		fmt.Fprintln(op.Out, empty)
		return Success
	}
	for _, n := range names {
		fmt.Fprintln(op.Out, n)
	}
	return Success
}

// showProposal prints a proposed skill.
func (op Options) showProposal(procs *procedures.Store, name string) int {
	sk, err := procs.Library.Proposal(name)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return RunError
	}
	// The body was written by a model that read untrusted text: shown, not obeyed.
	fmt.Fprint(op.Out, tui.EscapeControls(sk.Body))
	return Success
}

// curatorConfig loads the configuration the way `run` does: an invalid file is an error
// even here, and a MISSING KEY is not, because nothing but a consolidation run calls a
// model.
func (op Options) curatorConfig(fl flags) (config.Config, int) {
	if path := resolvedConfigPath(fl); path != "" {
		cfg, err := loadPreferringKey(path, fl)
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return config.Config{}, ConfigError
		}
		return cfg, Success
	}
	cfg, err := config.LoadOrDefault("")
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return config.Config{}, ConfigError
	}
	return cfg, Success
}

// reportSkillAction turns one action's error into an exit code and one line of output.
//
// The library's message already names the skill and the reason ("could not restore the skill
// \"old\": ..."), so the prefix here is the VERB alone: repeating the name produced
// `could not restored "old": could not restore the skill "old": ...`.
func (op Options) reportSkillAction(err error, name, done string) int {
	if err != nil {
		fmt.Fprintf(op.Err, "❌ could not %s: %v\n", done, err)
		return RunError
	}
	fmt.Fprintf(op.Out, "%s: %s\n", done, name)
	return Success
}
