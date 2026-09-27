package tui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// Front ends reach the library through the RUNNER, never by opening a second one: two
// libraries over one directory would each save their own view over the other's, which is
// the mistake the shared store exists to prevent.
func TestTheRunnerExposesTheLibraryItWasGiven(t *testing.T) {
	dir := t.TempDir()
	led, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatalf("usage.Open: %v", err)
	}
	store := &procedures.Store{Library: skills.New(dir), Usage: led}
	r := NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, nil)
	r.UseStore(store)

	if _, err := r.SaveSkill("One", "# One\n\nbody\n"); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	index, err := r.Skills()
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(index) != 1 || index[0].Name != "one" {
		t.Fatalf("Skills() = %+v, want one entry named one", index)
	}
	got, err := r.Skill("one")
	if err != nil || got.Body == "" {
		t.Fatalf("Skill(one) = %+v, %v; the body is what this is for", got, err)
	}
	// The provenance marker is a SECURITY decision: only created_by="agent" skills may be
	// archived or merged by the curator, so a save from the interface must never be
	// marked as the background fork's.
	if e := r.SkillTelemetry()["one"]; e.CreatedBy != usage.ByForeground {
		t.Errorf("a save from the interface must be provenance %q, not %q", usage.ByForeground, e.CreatedBy)
	}
	if err := r.SetSkillPinned("one", true); err != nil {
		t.Fatalf("SetSkillPinned: %v", err)
	}
	if !r.SkillTelemetry()["one"].Pinned {
		t.Error("the pin was not recorded in the sidecar")
	}
	if err := r.ArchiveSkill("one"); err != nil {
		t.Fatalf("ArchiveSkill: %v", err)
	}
	archived, err := r.ArchivedSkills()
	if err != nil || len(archived) != 1 || archived[0] != "one" {
		t.Fatalf("ArchivedSkills = %v, %v", archived, err)
	}
	if err := r.RestoreSkill("one"); err != nil {
		t.Fatalf("RestoreSkill: %v", err)
	}
	if _, err := r.Skill("one"); err != nil {
		t.Errorf("Skill after Restore: %v", err)
	}
}

// A runner whose ledger could not be opened still answers the list: the library is the
// feature, the telemetry is beside it.
func TestTheRunnerSurvivesWithoutALedger(t *testing.T) {
	dir := t.TempDir()
	r := NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, nil)
	r.UseStore(&procedures.Store{Library: skills.New(dir), Usage: nil})

	if _, err := r.Skills(); err != nil {
		t.Errorf("Skills with no ledger: %v", err)
	}
	if err := r.SetSkillPinned("one", true); err == nil {
		t.Error("pinning with no ledger must be refused with a reason, not silently ignored")
	}
	if got := r.SkillTelemetry(); len(got) != 0 {
		t.Errorf("SkillTelemetry with no ledger = %v, want empty", got)
	}
	// Archive and restore still WORK without a ledger: the document moves, the
	// telemetry that would have recorded it simply is not there.
	if _, err := r.SaveSkill("two", "# Two\n\nbody\n"); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	if err := r.ArchiveSkill("two"); err != nil {
		t.Errorf("ArchiveSkill with no ledger: %v", err)
	}
	if err := r.RestoreSkill("two"); err != nil {
		t.Errorf("RestoreSkill with no ledger: %v", err)
	}
}

// TestTheRunnerReportsWhatTheLibraryRefuses: the three writes pass the library's answer
// through untouched. A front end shows the reason the LIBRARY gave ("the name is empty",
// "past the ...-byte limit"), not one this layer invented, because the library is the only
// thing that knows the rule.
func TestTheRunnerReportsWhatTheLibraryRefuses(t *testing.T) {
	dir := t.TempDir()
	led, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, nil)
	r.UseStore(&procedures.Store{Library: skills.New(dir), Usage: led})

	// A name that sanitises to nothing.
	if _, err := r.SaveSkill("!!!", "# X\n\nbody\n"); err == nil {
		t.Error("SaveSkill with an unusable name must fail")
	}
	// An empty body.
	if _, err := r.SaveSkill("fine", ""); err == nil {
		t.Error("SaveSkill with an empty body must fail")
	}
	// Archiving something that is not there, and restoring something that is not archived.
	if err := r.ArchiveSkill("nope"); err == nil {
		t.Error("ArchiveSkill on a missing document must fail")
	}
	if err := r.RestoreSkill("nope"); err == nil {
		t.Error("RestoreSkill on a document that is not archived must fail")
	}
}

// TestTheRunnerTurnsASkillOffAndBackOn: the interface does not talk to the ledger, it talks to the
// runner, and the runner is what holds the shared store. Turning off has to be visible in the
// ledger, has to leave the model's list, and has to STAY in the interface's list with a flag -
// those three are the whole meaning of "off", and dropping the third would make the switch
// one-way.
func TestTheRunnerTurnsASkillOffAndBackOn(t *testing.T) {
	dir := t.TempDir()
	led, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatalf("usage.Open: %v", err)
	}
	store := &procedures.Store{Library: skills.New(dir), Usage: led}
	store.Library.Hidden = led.Disabled
	r := NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, nil)
	r.UseStore(store)

	if _, err := r.SaveSkill("one", "# One\n\nbody\n"); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	if err := r.SetSkillDisabled("one", true); err != nil {
		t.Fatalf("SetSkillDisabled: %v", err)
	}
	if !r.SkillTelemetry()["one"].Disabled {
		t.Error("the flag did not reach the ledger")
	}
	// The MODEL's list drops it: that is the promise the switch makes.
	if index, _ := r.library().List(); len(index) != 0 {
		t.Errorf("the model's index = %+v: a disabled skill must not be offered", index)
	}
	// The INTERFACE's list keeps it, so the badge can be drawn and the switch reversed.
	if index, _ := r.Skills(); len(index) != 1 || index[0].Name != "one" {
		t.Errorf("the interface's index = %+v, want the document still listed with its flag", index)
	}
	// And it is still openable: turned off is not deleted.
	if _, err := r.Skill("one"); err != nil {
		t.Errorf("a disabled skill must still be readable by name: %v", err)
	}
	if err := r.SetSkillDisabled("one", false); err != nil {
		t.Fatalf("re-enabling: %v", err)
	}
	if index, _ := r.library().List(); len(index) != 1 {
		t.Errorf("the model's index = %+v: enabling did not restore it", index)
	}
}

// TestDisablingWithoutALedgerIsRefused: the flag is the promise that the agent stops seeing the
// document, and without a ledger there is nowhere to remember it. A reported success for a veto
// that cannot be stored is the one answer that will not do, the same rule the pin follows.
func TestDisablingWithoutALedgerIsRefused(t *testing.T) {
	r := NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, nil)
	r.UseStore(&procedures.Store{Library: skills.New(t.TempDir())})

	err := r.SetSkillDisabled("one", true)
	if err == nil {
		t.Fatal("disabling with no ledger must fail")
	}
	if !strings.Contains(err.Error(), "ledger") {
		t.Errorf("the refusal must name what is missing: %v", err)
	}
}

// TestTheRunnerDeletesTheDocumentAndItsTelemetry: deleting a skill without deleting its telemetry
// leaves rubbish the curator keeps counting, and the name can be created again later carrying the
// old counters. The two go together.
func TestTheRunnerDeletesTheDocumentAndItsTelemetry(t *testing.T) {
	dir := t.TempDir()
	led, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatalf("usage.Open: %v", err)
	}
	store := &procedures.Store{Library: skills.New(dir), Usage: led}
	r := NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, nil)
	r.UseStore(store)

	if _, err := r.SaveSkill("one", "# One\n\nbody\n"); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	if err := r.DeleteSkill("one"); err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "one.md")); !os.IsNotExist(err) {
		t.Errorf("the document survived: %v", err)
	}
	if _, ok := r.SkillTelemetry()["one"]; ok {
		t.Error("the telemetry survived the document it describes")
	}

	// A name that is not there is a failure to report, not a quiet success.
	if err := r.DeleteSkill("nope"); err == nil {
		t.Error("deleting a name that is not there must fail")
	}
}

// TestDeletingWithoutALedgerStillRemovesTheDocument: the document is the thing the user asked to
// be rid of, and a missing ledger describes nothing. Refusing the deletion over telemetry that
// does not exist would block the operation for the wrong reason.
func TestDeletingWithoutALedgerStillRemovesTheDocument(t *testing.T) {
	dir := t.TempDir()
	r := NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, nil)
	r.UseStore(&procedures.Store{Library: skills.New(dir)})

	if _, err := r.SaveSkill("one", "# One\n\nbody\n"); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	if err := r.DeleteSkill("one"); err != nil {
		t.Fatalf("DeleteSkill without a ledger: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "one.md")); !os.IsNotExist(err) {
		t.Errorf("the document survived: %v", err)
	}
}
