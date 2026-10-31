package tui

import (
	"errors"

	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// The runner's library of skills and its usage telemetry.

// library is the procedure library the skill tools read and write.
//
// It is resolved once and kept: the directory does not change during a session, and creating
// it per turn would be a filesystem call for nothing.
func (r *AppRunner) library() *skills.Library { return r.procedures().Library }

// procedures returns the library and its ledger, building them on first use.
func (r *AppRunner) procedures() *procedures.Store {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.store == nil {
		// Build them through internal/procedures, the same constructor the command line's task
		// and plan runs use. Both fields below used to be built here directly, which is how
		// this interface ended up with a library while two other paths promised the model one
		// and then answered that none was configured.
		r.shared = procedures.Open(r.Config(), r.Log)
		// A scope remembered before the store existed is applied HERE, on the way in: the
		// gateway may name a session's project before any turn has run, and a library built
		// afterwards must not come up unscoped just because of the order the two happened in.
		r.store = procedures.Scoped(r.shared, r.projectScope)
	}
	return r.store
}

// UseStore installs the procedure library and its ledger.
//
// It exists so that several conversations in ONE process share them, which is what sessions need.
// The ledger is a file read once into memory, so two runners with two ledgers over one file would
// each save their own view over the other's - and a verdict would be lost with nothing to show for
// it. One store means one in-memory truth, and the ledger's own lock makes it safe to touch from
// two runs at once.
//
// A runner given none still builds its own on first use, which is what every other caller wants:
// the command line's task and plan runs have one conversation and no reason to share.
func (r *AppRunner) UseStore(st *procedures.Store) {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	r.shared = st
	r.store = st
	if st != nil && r.projectScope != "" {
		r.store = procedures.Scoped(st, r.projectScope)
	}
}

// Skills is the index the INTERFACE draws, so it includes the documents turned off: they belong
// in the list with a badge, which is what makes turning one back on a click rather than a hunt
// for a file that is no longer visible. The model's own list is Library.List, and it drops them.
func (r *AppRunner) Skills() ([]skills.Skill, error) { return r.library().Catalog() }

// Skill returns one document with its body.
func (r *AppRunner) Skill(name string) (skills.Skill, error) { return r.library().Get(name) }

// SaveSkill writes a document, creating or replacing it.
//
// The provenance marker is ByForeground and that is a SECURITY decision, not bookkeeping:
// only created_by="agent" skills may be archived or merged by the curator, so a skill a
// person wrote from the interface must never be marked as the background fork's.
func (r *AppRunner) SaveSkill(name, body string) (skills.Skill, error) {
	s, err := r.library().Save(name, body)
	if err != nil {
		return skills.Skill{}, err
	}
	if u := r.procedures().Usage; u != nil {
		u.BumpPatch(s.Name, usage.ByForeground)
		_ = u.Save()
	}
	return s, nil
}

// SkillTelemetry returns the lifecycle sidecar. Empty rather than nil when the ledger
// could not be opened: a front end that ranges over nil and one that ranges over an
// empty map draw the same thing.
func (r *AppRunner) SkillTelemetry() map[string]usage.Entry {
	if u := r.procedures().Usage; u != nil {
		return u.All()
	}
	return map[string]usage.Entry{}
}

// SetSkillPinned pins or unpins a skill.
//
// It is REFUSED without a ledger rather than silently ignored: the pin is the user's veto
// over every automatic transition, and a front end that reported success for a veto it
// could not remember would be lying about the one thing that protects their work.
func (r *AppRunner) SetSkillPinned(name string, pinned bool) error {
	u := r.procedures().Usage
	if u == nil {
		return errors.New("no usage ledger is configured, so a skill cannot be pinned")
	}
	u.SetPinned(skills.Name(name), pinned)
	return u.Save()
}

// ArchiveSkill moves a document aside; RestoreSkill brings it back. Neither is a
// delete, and that is the promise these two exist to keep.
//
// Both work without a ledger: the document moves either way, and only the telemetry that
// would have recorded it is absent.
func (r *AppRunner) ArchiveSkill(name string) error {
	if err := r.library().Archive(name); err != nil {
		return err
	}
	if u := r.procedures().Usage; u != nil {
		u.SetState(skills.Name(name), usage.StateArchived)
		return u.Save()
	}
	return nil
}

// RestoreSkill brings an archived document back.
func (r *AppRunner) RestoreSkill(name string) error {
	if err := r.library().Restore(name); err != nil {
		return err
	}
	if u := r.procedures().Usage; u != nil {
		u.SetState(skills.Name(name), usage.StateActive)
		return u.Save()
	}
	return nil
}

// SetSkillDisabled turns a skill off or back on.
//
// It is REFUSED without a ledger for the same reason the pin is: the flag is the promise that
// the agent stops seeing the document, and a front end told "done" for a promise nobody could
// remember would be lying about the one thing it was asked to change.
func (r *AppRunner) SetSkillDisabled(name string, disabled bool) error {
	u := r.procedures().Usage
	if u == nil {
		return errors.New("no usage ledger is configured, so a skill cannot be disabled")
	}
	u.SetDisabled(skills.Name(name), disabled)
	return u.Save()
}

// DeleteSkill removes a document for good, and the telemetry that describes it.
//
// The ledger entry goes too: a name left behind would be handed to the curator as a skill that
// no longer exists, and a document re-created under the same name later would arrive carrying
// the counters and complaints of the one that was deleted.
//
// A missing ledger does not block it. The document is what the user asked to be rid of, and the
// telemetry that would have described it is exactly the thing that is not there.
func (r *AppRunner) DeleteSkill(name string) error {
	if err := r.library().Delete(name); err != nil {
		return err
	}
	if u := r.procedures().Usage; u != nil {
		u.Forget(skills.Name(name))
		return u.Save()
	}
	return nil
}

// ArchivedSkills lists what the archive holds.
func (r *AppRunner) ArchivedSkills() ([]string, error) { return r.library().Archived() }

// ProposedSkills lists the skills the background review proposed, waiting for the user.
func (r *AppRunner) ProposedSkills() ([]string, error) { return r.library().Proposed() }

// ProposedSkill reads one proposal.
func (r *AppRunner) ProposedSkill(name string) (skills.Skill, error) {
	return r.library().Proposal(name)
}

// AcceptProposedSkill puts a proposal in the shared library.
func (r *AppRunner) AcceptProposedSkill(name string) error {
	return r.library().AcceptProposal(name)
}

// RejectProposedSkill deletes a proposal.
func (r *AppRunner) RejectProposedSkill(name string) error {
	return r.library().RejectProposal(name)
}
