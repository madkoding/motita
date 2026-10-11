package gateway

// Coverage tests for the gateway package: the Podman offer on project creation, and Podman after
// creation (the toggle, compose, and the sessions told about it).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Podman offer on project creation ---

func withPodman(t *testing.T, installed bool) {
	t.Helper()
	old, oldWorks := podmanInstalled, podmanWorks
	podmanInstalled = func() bool { return installed }
	podmanWorks = func() bool { return installed }
	t.Cleanup(func() { podmanInstalled, podmanWorks = old, oldWorks })
}

func createPodmanProject(t *testing.T, body string) map[string]any {
	t.Helper()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = t.TempDir()
		o.WorkspaceDir = t.TempDir()
	})
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRuntimesReportsPodman(t *testing.T) {
	for _, installed := range []bool{true, false} {
		withPodman(t, installed)
		srv := newTestServer(t, &fakeService{})
		req, _ := http.NewRequest(http.MethodGet, srv.BaseURL()+"/v1/runtimes", nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		var out map[string]bool
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out["podman"] != installed {
			t.Fatalf("installed=%v: got %s", installed, w.Body)
		}
	}
}

func TestCreateProjectKeepsThePodmanAnswer(t *testing.T) {
	withPodman(t, true)
	out := createPodmanProject(t, `{"title":"x","dir":"p","use_podman":true}`)
	if out["podman"] != true {
		t.Fatalf("podman = %v, want true", out["podman"])
	}
}

func TestCreateProjectIgnoresPodmanWhenNotInstalled(t *testing.T) {
	withPodman(t, false)
	out := createPodmanProject(t, `{"title":"x","dir":"p","use_podman":true}`)
	if out["podman"] != false {
		t.Fatalf("podman = %v, want false", out["podman"])
	}
}

func TestPodmanInstalledReadsThePath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if podmanInstalled() {
		t.Fatal("podman found on an empty PATH")
	}
}

// --- Podman after creation: toggle, compose, and the sessions told about it ---

type runtimeService struct {
	*fakeService
	runtimes []string
}

func (s *runtimeService) SetContainerRuntime(name string) { s.runtimes = append(s.runtimes, name) }

func TestRuntimeOfAndApplyRuntimeTo(t *testing.T) {
	if runtimeOf(nil) != "" || runtimeOf(&Project{}) != "" || runtimeOf(&Project{Podman: true}) != "podman" {
		t.Fatal("runtimeOf must say podman only for a project that asked for it")
	}
	svc := &runtimeService{fakeService: &fakeService{}}
	applyRuntimeTo(svc, &Project{Podman: true})
	if len(svc.runtimes) != 1 || svc.runtimes[0] != "podman" {
		t.Fatalf("runtime not passed: %v", svc.runtimes)
	}
	applyRuntimeTo(&fakeService{}, &Project{Podman: true}) // cannot take it: left alone
}

func patchProject(t *testing.T, srv *Server, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, srv.BaseURL()+"/v1/projects/"+id, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestUpdateProjectPodman(t *testing.T) {
	withPodman(t, true)
	svc := &runtimeService{fakeService: &fakeService{}}
	srv := newTestServer(t, svc, func(o *Options) {
		o.ProjectDir = t.TempDir()
		o.WorkspaceDir = t.TempDir()
		o.NewService = func() (Service, error) { return svc, nil }
	})
	if w := patchProject(t, srv, "nope", `{"podman":true}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown project: %d", w.Code)
	}
	if err := srv.projects.save(Project{ID: "p1", Title: "t", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	c, err := srv.createSession()
	if err != nil {
		t.Fatal(err)
	}
	c.svc = svc
	c.setProjectID("p1", "", "")
	srv.sessionsMu.Lock()
	srv.sessions[c.id] = c
	srv.sessionsMu.Unlock()

	if w := patchProject(t, srv, "p1", `{`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", w.Code)
	}
	if w := patchProject(t, srv, "p1", `{"podman":true}`); w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	if p, _ := srv.projects.load("p1"); !p.Podman {
		t.Fatal("the answer was not saved")
	}
	if len(svc.runtimes) == 0 || svc.runtimes[len(svc.runtimes)-1] != "podman" {
		t.Fatalf("the open session was not told: %v", svc.runtimes)
	}
	withPodman(t, false)
	if w := patchProject(t, srv, "p1", `{"podman":true}`); w.Code != http.StatusConflict {
		t.Fatalf("podman missing: %d", w.Code)
	}
	if w := patchProject(t, srv, "p1", `{"podman":false}`); w.Code != http.StatusOK {
		t.Fatalf("disable: %d", w.Code)
	}
}

func TestUpdateProjectSaveFailure(t *testing.T) {
	withPodman(t, true)
	dir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = dir
		o.WorkspaceDir = t.TempDir()
	})
	if err := srv.projects.save(Project{ID: "p1", Title: "t", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if w := patchProject(t, srv, "p1", `{"podman":true}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("save failure: %d", w.Code)
	}
}

func TestRuntimesReportsCompose(t *testing.T) {
	withPodman(t, true)
	old := podmanComposeInstalled
	podmanComposeInstalled = func() bool { return true }
	defer func() { podmanComposeInstalled = old }()
	srv := newTestServer(t, &fakeService{})
	req, _ := http.NewRequest(http.MethodGet, srv.BaseURL()+"/v1/runtimes", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var out map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out["podman_compose"] {
		t.Fatalf("got %s", w.Body)
	}
}

func TestPodmanComposeInstalledReadsThePath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if podmanComposeInstalled() {
		t.Fatal("compose found on an empty PATH")
	}
}

// TestWriteSyncedReportsAFailedWrite: /dev/full opens fine and refuses every write, which is
// the failure of the three steps a test can cause for any user, root included.
func TestWriteSyncedReportsAFailedWrite(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("this platform has no /dev/full")
	}
	if err := writeSynced("/dev/full", []byte("x")); err == nil {
		t.Fatal("a write to a full device must fail")
	}
}

// fakePodman puts a podman on the PATH that answers `info` and `compose version` with the exit
// codes it is given: the real detection functions run, against a binary a test controls.
func fakePodman(t *testing.T, infoExit, composeExit int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(
		"#!/bin/sh\ncase \"$1\" in\n  info) exit %d ;;\n  compose) exit %d ;;\nesac\nexit 0\n",
		infoExit,
		composeExit,
	)
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPodmanDetectionAgainstAFakeBinary(t *testing.T) {
	fakePodman(t, 0, 0)
	if !podmanInstalled() || !podmanWorks() || !podmanComposeInstalled() {
		t.Fatal("a podman that answers must be installed, working and have compose")
	}
	fakePodman(t, 1, 1)
	if !podmanInstalled() {
		t.Fatal("the binary is there")
	}
	if podmanWorks() || podmanComposeInstalled() {
		t.Fatal("a podman whose info and compose fail is neither working nor has compose")
	}
}

func TestPodmanWorksIsFalseWithoutABinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if podmanWorks() {
		t.Fatal("no binary, nothing works")
	}
}

func TestRuntimesSeparatesInstalledFromReady(t *testing.T) {
	oldI, oldW, oldC := podmanInstalled, podmanWorks, podmanComposeInstalled
	defer func() { podmanInstalled, podmanWorks, podmanComposeInstalled = oldI, oldW, oldC }()
	podmanInstalled = func() bool { return true }
	podmanWorks = func() bool { return false }
	podmanComposeInstalled = func() bool {
		t.Fatal("compose must not be asked of a podman that does not work")
		return false
	}
	srv := newTestServer(t, &fakeService{})
	req, _ := http.NewRequest(http.MethodGet, srv.BaseURL()+"/v1/runtimes", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var out map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out["podman"] || out["podman_ready"] ||
		out["podman_compose"] {
		t.Fatalf("got %s", w.Body)
	}
}
