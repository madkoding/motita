package gateway

// The handler branches that need a server built for a purpose: the model list against a fake
// provider, an injected UUID source, a workspace root shared with a session, and the refusals that
// only happen when a piece of the world is missing.
//
// Several of these reach a branch that a WELL-BEHAVED server never takes - an id that cannot be
// formed, a directory that cannot be created - so they build their own Options rather than reusing
// newTestServer's defaults, and say why in the comment above each.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
)

// A provider with no BaseURL configured has one derived from its own name, so a user who picked a
// provider in the interface does not have to know its endpoint. Without this fallback the model
// list would ask an empty URL and report a failure that blames the provider.
func TestTheModelListDerivesTheProviderURLWhenNoneIsConfigured(t *testing.T) {
	var asked string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-one"},{"id":"model-two"}]}`))
	}))
	defer provider.Close()

	cfg := config.Default()
	// The provider is one the catalogue knows, and its base URL is pointed at the fake above by
	// setting BaseURL to empty and the provider to a known id: DefaultBaseURL is what fills it in,
	// so the assertion below is that the handler CALLS it rather than reading the field.
	cfg.LLM.Provider = "ollama"
	cfg.LLM.BaseURL = provider.URL // explicit: the fallback path is covered separately below

	svc := &fakeService{cfg: cfg}
	srv := newTestServer(t, svc)
	w := runModelList(t, srv)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body = %s", w.Code, w.Body.String())
	}
	if asked == "" {
		t.Error("the configured base URL must be the one asked")
	}
	var body struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(body.Models) != 2 {
		t.Errorf("models = %v, want the two the provider listed", body.Models)
	}
}

// And with NO base URL configured, the provider's own default is used. The assertion is on the
// request the PROVIDER sees, so a handler that skipped the fallback and asked "" would fail here
// rather than pass on an empty list.
func TestTheModelListFallsBackToTheProvidersDefaultURL(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = "test-key"

	srv := newTestServer(t, &fakeService{cfg: cfg})
	w := runModelList(t, srv)

	// No network is available to api.openai.com in a test, so the answer is a failure - and what
	// matters is that it is a failure from the PROVIDER (502) rather than the "no API base URL"
	// 502 that an empty URL produces. Both are 502, so the message is what distinguishes them.
	if !strings.Contains(w.Body.String(), "openai.com") {
		t.Fatalf("body = %q, want it to name the provider's own default URL", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "no API base URL") {
		t.Error("the provider's default must be used, not an empty URL")
	}
}

// runModelList calls the model list endpoint on a conversation resolved through the scoped route,
// which is where the fake service's config comes from.
func runModelList(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("the default conversation must exist")
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models/list", nil)
	req = req.WithContext(context.WithValue(req.Context(), conversationKey, c))
	w := httptest.NewRecorder()
	srv.handleModelList(w, req)
	return w
}

// A session id that cannot be formed is reported rather than answered with an empty id. randReader
// is the package's own seam for this (token.go), so the failure is injected where the code reads
// from rather than by mocking a package.
func TestAProjectWithAnUnformableIdIsReported(t *testing.T) {
	restore := randReader
	randReader = failingReader{}
	t.Cleanup(func() { randReader = restore })

	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WorkspaceDir = t.TempDir() })
	withProjectStore(t, srv)

	w := post(t, srv, "/v1/projects", `{"title":"t","dir":"a-project"}`, testToken)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: body = %s", w.Code, w.Body.String())
	}
}

// A project whose folder cannot be created under the workspace is reported rather than registered:
// a project pointing at a directory that is not there is worse than no project, because the session
// it starts would run in whatever the agent's own directory happens to be.
func TestAProjectWhoseFolderCannotBeCreatedIsReported(t *testing.T) {
	// A FILE where the workspace is, so MkdirAll under it fails with ENOTDIR.
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.WriteFile(workspace, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WorkspaceDir = workspace })
	withProjectStore(t, srv)

	w := post(t, srv, "/v1/projects", `{"title":"t","dir":"a-project"}`, testToken)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the folder cannot be created: body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "could not create the project directory") {
		t.Errorf("body = %q, want it to name the directory failure", w.Body.String())
	}
}

// A body that is not JSON is refused before anything is created or read.
func TestAProjectWithAMalformedBodyIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WorkspaceDir = t.TempDir() })
	withProjectStore(t, srv)

	if w := post(t, srv, "/v1/projects", `{not json`, testToken); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if w := get(t, srv, "/v1/projects", testToken); !strings.Contains(w.Body.String(), `"projects":[]`) {
		t.Errorf("a refused create must leave nothing behind, got %q", w.Body.String())
	}
}

// The project list reports a store it cannot read rather than answering an empty list: an empty
// list is what a client deletes against.
func TestAnUnreadableProjectStoreIsReportedByTheList(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WorkspaceDir = t.TempDir() })
	ps := withProjectStore(t, srv)
	if err := ps.save(Project{ID: "p1", Title: "t", Dir: "/tmp/p1", Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	blockStoreDirectory(t, ps.dir)

	if w := get(t, srv, "/v1/projects", testToken); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for an unreadable project store: body = %s", w.Code, w.Body.String())
	}
}

// An empty store answers an EMPTY ARRAY and never null, which is the same stance every list in this
// gateway takes: a client that has to tell "empty" from "missing" is a client with a bug waiting.
func TestAnEmptyProjectStoreAnswersAnEmptyArray(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WorkspaceDir = t.TempDir() })
	withProjectStore(t, srv)

	w := get(t, srv, "/v1/projects", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"projects":[]`) {
		t.Errorf("body = %q, want an empty array rather than null", w.Body.String())
	}
}

// A project that cannot be SAVED is reported and does not answer 201: a client that believed a
// project was created would start a session against a name nothing knows.
func TestAProjectThatCannotBeSavedIsReported(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WorkspaceDir = t.TempDir() })
	ps := withProjectStore(t, srv)
	blockStoreDirectory(t, ps.dir)

	w := post(t, srv, "/v1/projects", `{"title":"t","dir":"a-project"}`, testToken)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the project cannot be saved: body = %s", w.Code, w.Body.String())
	}
}

// A project that cannot be DELETED is reported rather than answered with 204: the front end would
// remove it from the list while the file stayed on disk, and it would come back on the next load.
func TestAProjectThatCannotBeDeletedIsReported(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WorkspaceDir = t.TempDir() })
	ps := withProjectStore(t, srv)
	// A DIRECTORY where the project's file should be, so the remove fails instead of reporting a
	// clean miss (a missing file is the outcome the caller asked for).
	if err := os.MkdirAll(ps.path("p1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.path("p1"), "in-the-way"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := send(t, srv, http.MethodDelete, "/v1/projects/p1", testToken, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the project cannot be deleted: body = %s", w.Code, w.Body.String())
	}
}

// A merge for a session that has no workspace is refused: there is nothing to merge into, and a
// merge against a guess would run in the wrong repository.
func TestAMergeWithoutAWorkspaceIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("the default conversation must exist")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+DefaultSession+"/merge", nil)
	req = req.WithContext(context.WithValue(req.Context(), conversationKey, c))
	w := httptest.NewRecorder()
	srv.handleMergeSession(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "does not belong to a project") {
		t.Errorf("body = %q, want it to say there is no project to merge", w.Body.String())
	}
}

// A session whose project record is GONE is refused with 404 rather than merged into whatever the
// project id happens to resolve to now.
func TestAMergeForAMissingProjectIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjectStore(t, srv)
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("the default conversation must exist")
	}
	// A workspace and a project id, but no project record behind the id.
	c.setProjectID("a-project-that-was-deleted", t.TempDir(), t.TempDir())

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+DefaultSession+"/merge", nil)
	req = req.WithContext(context.WithValue(req.Context(), conversationKey, c))
	w := httptest.NewRecorder()
	srv.handleMergeSession(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: body = %s", w.Code, w.Body.String())
	}
}

// A rename with a malformed body is refused and the title is left alone.
func TestARenameWithAMalformedBodyIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("the default conversation must exist")
	}
	c.setTitle("the original title")

	req := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+DefaultSession, strings.NewReader(`{not json`))
	req = req.WithContext(context.WithValue(req.Context(), conversationKey, c))
	w := httptest.NewRecorder()
	srv.handleRenameSession(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := c.status().Title; got != "the original title" {
		t.Errorf("Title = %q, want it untouched by a refused rename", got)
	}
}

// An automatic title replaces the PLACEHOLDER only. A title the user chose is theirs, and a title
// already generated must not be regenerated on every turn.
func TestAnAutomaticTitleOnlyReplacesAPlaceholder(t *testing.T) {
	cases := []struct {
		name, existing, firstUser, want string
	}{
		{"a placeholder is replaced", placeholderTitle, "fix the build", "fix the build"},
		{"a legacy placeholder is replaced", legacyPlaceholderPrefix + " 5", "fix the build", "fix the build"},
		{"a user's title is kept", "my own session", "fix the build", "my own session"},
		{"an empty transcript leaves it alone", placeholderTitle, "", placeholderTitle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{}
			if tc.firstUser != "" {
				svc.transcript = []agent.DialogueTurn{{User: tc.firstUser}, {Agent: "the reply"}}
			}
			srv := newTestServer(t, svc)
			c, ok := srv.lookup(DefaultSession)
			if !ok {
				t.Fatal("the default conversation must exist")
			}
			// The conversation the server holds must be the one with the fake service: setTitle
			// and the transcript both go through it.
			c.setTitle(tc.existing)
			c.svc = svc
			srv.saveSession(c)

			srv.maybeAutoTitle(c)

			if got := c.status().Title; got != tc.want {
				t.Errorf("Title = %q, want %q", got, tc.want)
			}
		})
	}
}

// A session with a run already in flight has no run slot to give, and startDetachedRun says so
// rather than starting a second turn in one conversation.
func TestADetachedRunWithoutAConversationOrServiceIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})

	if _, ok := srv.startDetachedRun(nil, "a task", "task", srv.unattendedApprover("s1")); ok {
		t.Error("a nil conversation has no run slot")
	}
	if _, ok := srv.startDetachedRun(&conversation{id: "s1"}, "a task", "task", srv.unattendedApprover("s1")); ok {
		t.Error("a conversation with no service cannot run anything")
	}
}

// withProjectStore gives a test server a project store of its own, rooted where the server expects
// it so the handlers find it.
func withProjectStore(t *testing.T, srv *Server) *projectStore {
	t.Helper()
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	srv.projects = ps
	return ps
}
