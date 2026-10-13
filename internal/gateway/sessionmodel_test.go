package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// TestEachSessionKeepsItsOwnModel: the model belongs to the session. Switching it in one leaves
// every other session on the model it had, and the list says which model each one runs.
func TestEachSessionKeepsItsOwnModel(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.NewService = func() (Service, error) {
			s := &fakeService{}
			s.cfg.LLM.Provider, s.cfg.LLM.Model = "openai", "base"
			return s, nil
		}
	})
	a, b := createFree(t, srv), createFree(t, srv)
	if w := patchReq(t, srv, sessionPath(srv, a, "/config"), `{"model":"other"}`, testToken); w.Code != http.StatusOK {
		t.Fatalf("switching the model answered %d: %s", w.Code, w.Body.String())
	}
	if got := lookupService(t, srv, b).Config().LLM.Model; got != "base" {
		t.Errorf("switching the model of %s moved %s to %q", a, b, got)
	}

	var list struct {
		Sessions []SessionStatus `json:"sessions"`
	}
	if err := json.Unmarshal(get(t, srv, "/v1/sessions", testToken).Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	models := map[string]string{}
	for _, st := range list.Sessions {
		models[st.ID] = st.Provider + "/" + st.Model
	}
	if models[a] != "openai/other" || models[b] != "openai/base" {
		t.Errorf("the list reports %v, want %s on openai/other and %s on openai/base", models, a, b)
	}
}

// TestSwitchingTheModelOfASessionThatAlreadyTalkedWarns: nothing is refused, but the answer says
// what the switch means for this session, so the front end can warn about it.
func TestSwitchingTheModelOfASessionThatAlreadyTalkedWarns(t *testing.T) {
	svc := &fakeService{}
	svc.cfg.LLM.Provider, svc.cfg.LLM.Model = "openai", "first"
	srv := newTestServer(t, svc)
	path := sessionPath(srv, DefaultSession, "/config")
	view := func(body string) configView {
		t.Helper()
		w := patchReq(t, srv, path, body, testToken)
		if w.Code != http.StatusOK {
			t.Fatalf("PATCH %s answered %d: %s", body, w.Code, w.Body.String())
		}
		var v configView
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if v := view(`{"model":"second"}`); len(v.Warnings) != 0 {
		t.Errorf("a session with nothing said yet warned %v", v.Warnings)
	}
	svc.transcript = []agent.DialogueTurn{{User: "hi", Agent: "hello"}}
	if v := view(`{"model":"second"}`); len(v.Warnings) != 0 {
		t.Errorf("keeping the same model warned %v", v.Warnings)
	}
	if v := view(`{"reasoning":"high"}`); len(v.Warnings) != 0 {
		t.Errorf("changing only the level warned %v", v.Warnings)
	}
	v := view(`{"model":"third"}`)
	if !reflect.DeepEqual(v.Warnings, []string{"history"}) || v.PreviousModel != "second" {
		t.Errorf("switching after the conversation started answered %v / %q, want [history] / second",
			v.Warnings, v.PreviousModel)
	}

	conv, _ := srv.lookup(DefaultSession)
	conv.stateMu.Lock()
	conv.running = true
	conv.stateMu.Unlock()
	v = view(`{"provider":"anthropic","model":"claude"}`)
	if !reflect.DeepEqual(v.Warnings, []string{"history", "running"}) {
		t.Errorf("switching mid-turn answered %v, want [history running]", v.Warnings)
	}
	conv.stateMu.Lock()
	conv.running = false
	conv.stateMu.Unlock()
}

// TestTheDefaultSessionKeepsItsModelAcrossARestart: every other session got its model back after a
// restart, the first one came back on the gateway's configured model.
func TestTheDefaultSessionKeepsItsModelAcrossARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	opts := func() Options {
		return Options{
			Token: testToken, WorkspaceDir: t.TempDir(), SessionDir: dir,
			ProjectDir: filepath.Join(t.TempDir(), "projects"),
			Service:    &fakeService{},
			NewService: func() (Service, error) { return &fakeService{}, nil },
		}
	}
	first, _ := startServer(t, opts())
	if w := patchReq(t, first, sessionPath(first, DefaultSession, "/config"),
		`{"provider":"anthropic","model":"picked"}`, testToken); w.Code != http.StatusOK {
		t.Fatalf("switching answered %d", w.Code)
	}
	_ = first.Close(context.Background())

	second, _ := startServer(t, opts())
	cfg := lookupService(t, second, DefaultSession).Config()
	if cfg.LLM.Provider != "anthropic" || cfg.LLM.Model != "picked" {
		t.Errorf("the default session came back on %s/%s, want anthropic/picked", cfg.LLM.Provider, cfg.LLM.Model)
	}
}
