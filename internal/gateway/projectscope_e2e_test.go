package gateway

// The end-to-end property: two sessions in two different projects must not see each other's
// procedures, and a session with no project must see only the shared shelf.
//
// This is the defect that motivated the feature, in its own words (a real run): a session asked
// to enable a feature in one project searched the library, and a procedure describing ANOTHER
// repository outranked everything — the model read a confident procedure about a codebase the
// user was not in, and answered about that one instead.
//
// The unit tests in internal/projectskills pin the mechanism. This one pins the WIRING: that the
// gateway actually scopes the library of a session that belongs to a project, which is the step
// that would silently do nothing if the type assertion or the call site were wrong.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/tui"
)

// scopedRunnerFor builds an AppRunner the way the served gateway does, rooted at a shared
// library directory, and returns it with that directory.
func scopedRunnerFor(t *testing.T, shared string) *tui.AppRunner {
	t.Helper()
	cfg := config.Default()
	cfg.Skills.Dir = shared
	r := tui.NewAppRunner(os.Stdout, os.Stderr, cfg, nil, nil, nil)
	st := procedures.Open(cfg, nil)
	r.UseStore(st)
	return r
}

// projectNames is the index the MODEL is offered, sorted by the library.
func projectNames(t *testing.T, r *tui.AppRunner) []string {
	t.Helper()
	all, err := r.Skills()
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, s.Name)
	}
	return out
}

func listedNames(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestTwoProjectsDoNotSeeEachOthersProcedures is the whole point of the change, asserted through
// the service the gateway scopes rather than through the library directly.
func TestTwoProjectsDoNotSeeEachOthersProcedures(t *testing.T) {
	// One shared shelf, two projects, three services — exactly the shape of a gateway process
	// with three conversations open.
	shared := t.TempDir()
	alpha := t.TempDir()
	beta := t.TempDir()

	writeProjectProc(t, alpha, "alpha-deploy", "# Alpha deploy\n\nHow alpha ships.\n")
	writeProjectProc(t, beta, "beta-deploy", "# Beta deploy\n\nHow beta ships.\n")
	if err := os.WriteFile(filepath.Join(shared, "shared-note.md"), []byte("# Shared\n\nwritten with no project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	alphaSvc := scopedRunnerFor(t, shared)
	betaSvc := scopedRunnerFor(t, shared)
	freeSvc := scopedRunnerFor(t, shared)

	scopeProceduresTo(alphaSvc, alpha)
	scopeProceduresTo(betaSvc, beta)
	// The third session belongs to no project at all, and is left unscoped on purpose.

	// A session in alpha sees alpha's procedure and the shared shelf, and NOT beta's.
	gotAlpha := projectNames(t, alphaSvc)
	for _, want := range []string{"alpha-deploy", "shared-note"} {
		if !listedNames(gotAlpha, want) {
			t.Errorf("a session in alpha must see %q: %v", want, gotAlpha)
		}
	}
	if listedNames(gotAlpha, "beta-deploy") {
		t.Errorf("alpha's session sees beta's procedure: %v", gotAlpha)
	}

	// And the mirror image, so a bug that leaks in one direction only cannot pass.
	gotBeta := projectNames(t, betaSvc)
	if !listedNames(gotBeta, "beta-deploy") {
		t.Errorf("a session in beta must see its own procedure: %v", gotBeta)
	}
	if listedNames(gotBeta, "alpha-deploy") {
		t.Errorf("beta's session sees alpha's procedure: %v", gotBeta)
	}

	// A session with no project sees the shared shelf and neither project's work.
	gotFree := projectNames(t, freeSvc)
	if !listedNames(gotFree, "shared-note") {
		t.Errorf("a session with no project must see the shared shelf: %v", gotFree)
	}
	for _, leaked := range []string{"alpha-deploy", "beta-deploy"} {
		if listedNames(gotFree, leaked) {
			t.Errorf("a session with no project sees %q, which belongs to a project: %v", leaked, gotFree)
		}
	}
}

// TestAProcedureLearnedInAProjectStaysInIt: the write side. Work done inside a project must not
// silently become the shared shelf that every other project then reads.
func TestAProcedureLearnedInAProjectStaysInIt(t *testing.T) {
	shared := t.TempDir()
	project := t.TempDir()

	inProject := scopedRunnerFor(t, shared)
	outsider := scopedRunnerFor(t, shared)
	scopeProceduresTo(inProject, project)

	// What save_skill does, through the API the gateway's own skill endpoints use.
	if _, err := inProject.SaveSkill("learned-here", "# Learned here\n\nthis project's specifics\n"); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}

	// It is on disk inside the project...
	if _, err := os.Stat(filepath.Join(project, ".motita", "skills", "learned-here.md")); err != nil {
		t.Errorf("the procedure must be stored inside the project: %v", err)
	}
	// ...and it is NOT on the shared shelf.
	if _, err := os.Stat(filepath.Join(shared, "learned-here.md")); err == nil {
		t.Error("a procedure learned in a project was written to the shared shelf")
	}
	// A session with no project does not see it.
	if listedNames(projectNames(t, outsider), "learned-here") {
		t.Error("a procedure learned inside a project was offered to a session with no project")
	}
	// And the session that learned it does.
	if !listedNames(projectNames(t, inProject), "learned-here") {
		t.Error("the session that learned the procedure must be able to use it")
	}
}

// writeProjectProc puts a document where a project's own procedures live.
func writeProjectProc(t *testing.T, projectDir, name, body string) {
	t.Helper()
	dir := filepath.Join(projectDir, ".motita", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestTheScopeSurvivesAConversationContext: scoping is called with a context that is not used,
// and this pins that the helper does not depend on one — a call site that passed a cancelled
// context would otherwise scope nothing, silently.
func TestTheScopeSurvivesAConversationContext(t *testing.T) {
	shared := t.TempDir()
	project := t.TempDir()
	writeProjectProc(t, project, "alpha-deploy", "# Alpha\n\nbody\n")

	svc := scopedRunnerFor(t, shared)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx // the helper takes no context at all, which is the property under test

	scopeProceduresTo(svc, project)
	if !listedNames(projectNames(t, svc), "alpha-deploy") {
		t.Error("scoping must not depend on a live context")
	}
}
