package gateway

// The user must be asked before uncommitted work is destroyed, and the question
// must NAME what will be lost.
//
// The state that produced this: a session's checkout held two changes, deleting
// it was refused with "2 uncommitted change(s)", and the user had no way to
// proceed AND no way to see what those two were from the interface. The refusal
// was correct - the work must not vanish silently - but it was a dead end. The
// two changes turned out to be a build artefact and an npm lock file, which no
// number could have communicated.
//
// So deletion becomes a decision the user can actually make:
//
//  1. `GET .../deletion-preview` answers what WOULD be discarded, changing
//     nothing, and it lists the files rather than counting them.
//  2. `DELETE ...?force=1` carries out the discard the user confirmed.
//  3. Without that confirmation the refusal stands, and it is the SAME rules
//     both times - a preview that described a different decision than the
//     removal makes would be worse than having none.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
)

// previewOf asks the gateway what deleting this session would discard.
func previewOf(t *testing.T, srv *Server, id string) map[string]any {
	t.Helper()
	rec := send(t, srv, http.MethodGet, "/v1/sessions/"+id+"/deletion-preview", testToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("the preview must answer 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding the preview: %v (%s)", err, rec.Body.String())
	}
	return out
}

// changePaths returns the paths the preview lists.
func changePaths(t *testing.T, preview map[string]any) []string {
	t.Helper()
	raw, _ := preview["changes"].([]any)
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("a change must be an object, got %T", entry)
		}
		path, _ := m["path"].(string)
		out = append(out, path)
	}
	return out
}

// TestThePreviewNamesWhatDeletingWouldDiscard: the list, not a number. This is the
// contract the user asked for - "mostrar cuáles son esos cambios en el modal".
func TestThePreviewNamesWhatDeletingWouldDiscard(t *testing.T) {
	srv, _, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	if err := os.WriteFile(filepath.Join(wt, "tsconfig.tsbuildinfo"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "work-in-progress.md"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	preview := previewOf(t, srv, id)

	paths := changePaths(t, preview)
	if len(paths) != 2 {
		t.Fatalf("preview lists %v, want both changes named", paths)
	}
	joined := strings.Join(paths, " ")
	for _, want := range []string{"tsconfig.tsbuildinfo", "work-in-progress.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the preview must name %s, got %v", want, paths)
		}
	}
	// A count is still useful for the dialog's summary line, and it must agree with the list.
	if n, ok := preview["count"].(float64); !ok || int(n) != len(paths) {
		t.Errorf("count = %v, want %d, agreeing with the list", preview["count"], len(paths))
	}
	// The worktree and branch tell the user WHERE, which the dialog also shows.
	if preview["worktree"] == "" || preview["branch"] == "" {
		t.Errorf("the preview must say where the checkout is and on which branch: %v", preview)
	}
}

// TestThePreviewChangesNothing: asking must not answer by deleting. This is the
// difference between describing a decision and making it.
func TestThePreviewChangesNothing(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	work := filepath.Join(wt, "important.md")
	if err := os.WriteFile(work, []byte("do not lose me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	previewOf(t, srv, id)

	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the preview must not touch the work: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("the preview must not remove the checkout: %v", err)
	}
	if !worktreeRegistered(t, projectDir, wt) {
		t.Error("the preview must not release the registration")
	}
	if _, ok := srv.lookup(id); !ok {
		t.Error("the preview must not forget the session")
	}
}

// TestACleanCheckoutHasNothingToConfirm: the common case answers with an empty
// list, so the dialog does not ask a question that has no content.
func TestACleanCheckoutHasNothingToConfirm(t *testing.T) {
	srv, _, id := newSessionInProject(t)

	preview := previewOf(t, srv, id)

	if paths := changePaths(t, preview); len(paths) != 0 {
		t.Errorf("a clean checkout has nothing to discard, got %v", paths)
	}
	if n, _ := preview["count"].(float64); int(n) != 0 {
		t.Errorf("count = %v, want 0", preview["count"])
	}
}

// TestASessionWithNoCheckoutHasNothingToConfirm: a free-standing session has no
// worktree, and the preview says so rather than erroring.
func TestASessionWithNoCheckoutHasNothingToConfirm(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, withFactory())
	created := post(t, srv, "/v1/sessions", "{}", testToken)
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}

	preview := previewOf(t, srv, conv.ID)

	if preview["worktree"] != "" {
		t.Errorf("worktree = %v, want empty: this session belongs to no project", preview["worktree"])
	}
	if paths := changePaths(t, preview); len(paths) != 0 {
		t.Errorf("nothing to discard, got %v", paths)
	}
}

// TestThePreviewReportsAnUnreadableCheckoutRatherThanAnEmptyOne: the list could
// not be read, and that is NOT the same as "nothing to lose". A 0 here would be a
// statement the gateway cannot make.
func TestThePreviewReportsAnUnreadableCheckoutRatherThanAnEmptyOne(t *testing.T) {
	srv, _, id := newSessionInProject(t)

	restore := worktreeInspectList
	worktreeInspectList = func(context.Context, string) ([]gitx.Change, error) {
		return nil, errors.New("the checkout could not be read")
	}
	t.Cleanup(func() { worktreeInspectList = restore })

	preview := previewOf(t, srv, id)

	if preview["inspection_failed"] != true {
		t.Errorf("an unreadable checkout must be reported as such, got %v", preview)
	}
	if msg, _ := preview["error"].(string); !strings.Contains(msg, "could not be inspected") {
		t.Errorf("the preview must say why it could not be read, got %q", msg)
	}
}

// TestTheConfirmedDiscardGoesThrough is the point of the whole change: the user
// saw the list and said yes, so the deletion happens instead of dead-ending.
func TestTheConfirmedDiscardGoesThrough(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	if err := os.WriteFile(filepath.Join(wt, "scratch.tmp"), []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doDelete(t, srv, "/v1/sessions/"+id+"?force=1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a confirmed discard must delete, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the checkout must be gone after a confirmed discard: %v", err)
	}
	if worktreeRegistered(t, projectDir, wt) {
		t.Error("the registration must be released")
	}
	if _, ok := srv.lookup(id); ok {
		t.Error("the session must be gone")
	}
}

// TestWithoutTheConfirmationTheWorkSurvives: force is the ONLY thing that makes a
// discard happen. A client that forgets it gets the refusal, which is the safe
// direction for the mistake to go.
func TestWithoutTheConfirmationTheWorkSurvives(t *testing.T) {
	srv, _, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	work := filepath.Join(wt, "precious.md")
	if err := os.WriteFile(work, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doDelete(t, srv, "/v1/sessions/"+id)
	if rec.Code != http.StatusConflict {
		t.Fatalf("without a confirmation the deletion must be refused, got %d", rec.Code)
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the work must survive: %v", err)
	}
	if _, ok := srv.lookup(id); !ok {
		t.Error("the session must survive, so the user can look and decide")
	}
	// The message must name the way out, and the way out now exists.
	if !strings.Contains(rec.Body.String(), "confirm the discard") {
		t.Errorf("the refusal must say how to proceed, got: %s", rec.Body.String())
	}
}

// TestAForceThatIsNotAConfirmationIsNotOne: only the spellings a client sends
// count. A truncated or malformed request can then only ever be safe.
func TestAForceThatIsNotAConfirmationIsNotOne(t *testing.T) {
	for _, suffix := range []string{"?force=0", "?force=false", "?force=", "?force=maybe", "?forcex=1"} {
		t.Run(suffix, func(t *testing.T) {
			srv, _, id := newSessionInProject(t)
			wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
			if err := os.WriteFile(filepath.Join(wt, "keep.md"), []byte("keep\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			rec := doDelete(t, srv, "/v1/sessions/"+id+suffix)
			if rec.Code != http.StatusConflict {
				t.Fatalf("%s is not a confirmation, so the deletion must be refused: got %d", suffix, rec.Code)
			}
			if _, err := os.Stat(filepath.Join(wt, "keep.md")); err != nil {
				t.Fatalf("the work must survive %s: %v", suffix, err)
			}
		})
	}
}

// TestTheDiscardIsAcceptedByGitItself: the force flag has to reach `git worktree
// remove`, because git refuses a dirty checkout on its own. A confirmation that
// still failed at the git call would be a dead end again, one step later.
func TestTheDiscardIsAcceptedByGitItself(t *testing.T) {
	srv, _, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	// A TRACKED file, modified: git refuses to remove this without --force, unlike an
	// untracked one, which it is happy to leave behind.
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doDelete(t, srv, "/v1/sessions/"+id+"?force=1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a tracked modification must be discarded on confirmation, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the checkout must be gone: %v", err)
	}
}

// TestASessionWithNothingToGiveBackIgnoresTheConfirmation: force on a session
// with no worktree changes nothing about how it is deleted. It is not an error.
func TestASessionWithNothingToGiveBackIgnoresTheConfirmation(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, withFactory())
	created := post(t, srv, "/v1/sessions", "{}", testToken)
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}

	rec := doDelete(t, srv, "/v1/sessions/"+conv.ID+"?force=1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a session with no checkout must delete, got %d: %s", rec.Code, rec.Body.String())
	}
}
