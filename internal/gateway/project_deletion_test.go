package gateway

// Deleting a project must do what the dialog says it does.
//
// The dialog has always said "This project and all its sessions will be
// permanently deleted", and the handler did not delete the sessions. Measured:
// it stopped their runs and removed the project's file, and every session was
// left alive - pointing at a project id that no longer exists, invisible in the
// sidebar (which groups sessions UNDER their project), and still holding its
// worktree and branch. Nothing could reach them again.
//
// The user's words for the requirement: "en ese caso si hay cambios, entonces al
// poner eliminar (y debiera ser en todos) debiera comprobar si el usuario esta
// realmente seguro, y si hay cambios mostrar cuales son".
//
// So the rules are the session deletion's rules, applied to every session of the
// project:
//
//  1. A project whose sessions hold uncommitted work is REFUSED, and the refusal
//     names the sessions and their files, so the decision stays the user's.
//  2. `?force=1` carries out the discard the user confirmed, for every session.
//  3. With the sessions gone, their worktrees go with them - they are the only
//     pointer to those checkouts.

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

// projectWithSessions creates a project, then the given sessions inside it, and
// returns the server, the project's id and the session ids in order.
func projectWithSessions(t *testing.T, n int) (*Server, string, []string) {
	t.Helper()
	srv, _, _ := newSessionInProject(t)
	// newSessionInProject made one project and one session already; reuse both.
	projects := listProjects(t, srv)
	if len(projects) == 0 {
		t.Fatal("the project must exist")
	}
	projectID := projects[0]
	ids := []string{}
	first := firstSessionOf(t, srv, projectID)
	ids = append(ids, first)
	for i := 1; i < n; i++ {
		ids = append(ids, createSessionIn(t, srv, projectID).ID)
	}
	return srv, projectID, ids
}

type projectInfo struct {
	ID string `json:"id"`
}

func listProjects(t *testing.T, srv *Server) []string {
	t.Helper()
	rec := send(t, srv, http.MethodGet, "/v1/projects", testToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list projects: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Projects []projectInfo `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(out.Projects))
	for _, p := range out.Projects {
		ids = append(ids, p.ID)
	}
	return ids
}

func firstSessionOf(t *testing.T, srv *Server, projectID string) string {
	t.Helper()
	rec := send(t, srv, http.MethodGet, "/v1/sessions", testToken, "")
	var out struct {
		Sessions []SessionStatus `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range out.Sessions {
		if s.ProjectID == projectID {
			return s.ID
		}
	}
	t.Fatalf("no session found for project %s", projectID)
	return ""
}

// TestDeletingAProjectDeletesItsSessions is the fix. The dialog promises it, so
// it has to happen: the sessions are gone, and so are the worktrees that were
// their only pointer.
func TestDeletingAProjectDeletesItsSessions(t *testing.T) {
	srv, projectID, ids := projectWithSessions(t, 2)

	rec := send(t, srv, http.MethodDelete, "/v1/projects/"+projectID, testToken, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deleting a clean project must answer 204, got %d: %s", rec.Code, rec.Body.String())
	}

	for _, id := range ids {
		if _, ok := srv.lookup(id); ok {
			t.Errorf("session %s survived its project; nothing can reach it again", id)
		}
		wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
		if _, err := os.Stat(wt); !os.IsNotExist(err) {
			t.Errorf("session %s left its worktree behind: %v", id, err)
		}
	}
}

// TestDeletingAProjectWithUncommittedWorkIsRefused: the sessions' work is the
// project's work, and it must not vanish silently. The refusal names what is in
// the way.
func TestDeletingAProjectWithUncommittedWorkIsRefused(t *testing.T) {
	srv, projectID, ids := projectWithSessions(t, 2)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", ids[1])
	if err := os.WriteFile(filepath.Join(wt, "in-progress.md"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := send(t, srv, http.MethodDelete, "/v1/projects/"+projectID, testToken, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("a project whose sessions hold work must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "in-progress.md") && !strings.Contains(body, ids[1]) {
		t.Errorf("the refusal must name what is in the way, got: %s", body)
	}

	// Everything survives: the project, both sessions, and the work.
	if _, err := os.Stat(filepath.Join(wt, "in-progress.md")); err != nil {
		t.Errorf("the work must survive: %v", err)
	}
	if _, ok := srv.lookup(ids[0]); !ok {
		t.Error("the first session must survive the refusal")
	}
	if _, ok := srv.lookup(ids[1]); !ok {
		t.Error("the second session must survive the refusal")
	}
	if len(listProjects(t, srv)) == 0 {
		t.Error("the project must survive the refusal")
	}
}

// TestAConfirmedProjectDiscardGoesThrough: the user saw the list and said yes,
// so every session's work is discarded and the project goes.
func TestAConfirmedProjectDiscardGoesThrough(t *testing.T) {
	srv, projectID, ids := projectWithSessions(t, 2)
	for _, id := range ids {
		wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
		if err := os.WriteFile(filepath.Join(wt, "scratch.tmp"), []byte("junk\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rec := send(t, srv, http.MethodDelete, "/v1/projects/"+projectID+"?force=1", testToken, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a confirmed discard must delete, got %d: %s", rec.Code, rec.Body.String())
	}

	for _, id := range ids {
		if _, ok := srv.lookup(id); ok {
			t.Errorf("session %s survived a confirmed discard", id)
		}
	}
	if len(listProjects(t, srv)) != 0 {
		t.Error("the project must be gone")
	}
}

// TestTheProjectPreviewNamesTheSessionsAndTheirChanges: the dialog has to be
// able to show what would be discarded BEFORE the user confirms, which is the
// whole point. A session holding a file is named with the file.
func TestTheProjectPreviewNamesTheSessionsAndTheirChanges(t *testing.T) {
	srv, projectID, ids := projectWithSessions(t, 2)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", ids[1])
	if err := os.WriteFile(filepath.Join(wt, "half-done.md"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := send(t, srv, http.MethodGet, "/v1/projects/"+projectID+"/deletion-preview", testToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("preview must answer 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Sessions []struct {
			ID      string        `json:"id"`
			Title   string        `json:"title"`
			Changes []gitx.Change `json:"changes"`
		} `json:"sessions"`
		Changes int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding the preview: %v (%s)", err, rec.Body.String())
	}
	if len(out.Sessions) != len(ids) {
		t.Fatalf("the preview must cover every session, got %d of %d", len(out.Sessions), len(ids))
	}
	found := false
	for _, s := range out.Sessions {
		for _, c := range s.Changes {
			if strings.Contains(c.Path, "half-done.md") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the preview must name the file that is in the way, got %+v", out)
	}
	if out.Changes != 1 {
		t.Errorf("count = %d, want 1", out.Changes)
	}

	// And asking must not delete anything.
	if _, ok := srv.lookup(ids[1]); !ok {
		t.Error("the preview must not delete the session")
	}
}

// TestTheProjectPreviewIgnoresTheProjectOwnCheckout: the project's own
// uncommitted changes are the USER's, and deleting the project's FILE does not
// touch its directory. Only the sessions' checkouts are at stake, and saying
// otherwise would refuse a deletion that destroys nothing.
func TestTheProjectPreviewIgnoresTheProjectOwnCheckout(t *testing.T) {
	srv, projectID, _ := projectWithSessions(t, 1)
	projectDir := projectDirOfProject(t, srv, projectID)
	if err := os.WriteFile(filepath.Join(projectDir, "user-edit.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := send(t, srv, http.MethodGet, "/v1/projects/"+projectID+"/deletion-preview", testToken, "")
	var out struct {
		Changes int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Changes != 0 {
		t.Errorf("count = %d, want 0: the project's own checkout is not deleted", out.Changes)
	}

	// And the deletion goes through, leaving the user's own edit alone.
	del := send(t, srv, http.MethodDelete, "/v1/projects/"+projectID, testToken, "")
	if del.Code != http.StatusNoContent {
		t.Fatalf("a project's own uncommitted edits must not block its deletion, got %d: %s", del.Code, del.Body.String())
	}
	if _, err := os.Stat(filepath.Join(projectDir, "user-edit.md")); err != nil {
		t.Errorf("the project's directory and its edits must survive: %v", err)
	}
}

// projectDirOfProject resolves a project's directory from the gateway's own view.
func projectDirOfProject(t *testing.T, srv *Server, id string) string {
	t.Helper()
	for _, c := range srv.snapshot() {
		if c.projectID == id && c.projectDir != "" {
			return c.projectDir
		}
	}
	t.Fatalf("no session carries a project directory for %s", id)
	return ""
}

// TestTheProjectPreviewReportsAnUnreadableCheckout: a checkout that cannot be
// read is not a clean one, and the preview must not say "nothing to lose".
func TestTheProjectPreviewReportsAnUnreadableCheckout(t *testing.T) {
	srv, projectID, _ := projectWithSessions(t, 1)

	restore := worktreeInspectList
	worktreeInspectList = func(context.Context, string) ([]gitx.Change, error) {
		return nil, errors.New("the checkout could not be read")
	}
	t.Cleanup(func() { worktreeInspectList = restore })

	rec := send(t, srv, http.MethodGet, "/v1/projects/"+projectID+"/deletion-preview", testToken, "")
	var out struct {
		InspectionFailed bool `json:"inspection_failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.InspectionFailed {
		t.Error("an unreadable checkout must be reported, not counted as zero")
	}

	// And the deletion is refused rather than destroying what could not be read.
	del := send(t, srv, http.MethodDelete, "/v1/projects/"+projectID, testToken, "")
	if del.Code != http.StatusConflict {
		t.Fatalf("a checkout that cannot be inspected must refuse the deletion, got %d: %s", del.Code, del.Body.String())
	}
}

// TestDeletingAProjectWithNoSessionsIsUnaffected: the common case still works.
func TestDeletingAProjectWithNoSessionsIsUnaffected(t *testing.T) {
	srv, projectID, ids := projectWithSessions(t, 1)
	for _, id := range ids {
		if rec := doDelete(t, srv, "/v1/sessions/"+id); rec.Code != http.StatusNoContent {
			t.Fatalf("clearing the session first: %d %s", rec.Code, rec.Body.String())
		}
	}

	rec := send(t, srv, http.MethodDelete, "/v1/projects/"+projectID, testToken, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a project with no sessions must delete, got %d: %s", rec.Code, rec.Body.String())
	}
}
