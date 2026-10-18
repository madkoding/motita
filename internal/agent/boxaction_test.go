package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/devbox"
	"github.com/madkoding/motita/internal/task"
)

// fakeBoxes records what the actions ask of the container layer.
type fakeBoxes struct {
	specs   []devbox.Spec
	up      error
	get     string
	getErr  error
	shotErr error
	shots   []string
	downs   int
	all     int
}

func (f *fakeBoxes) Up(_ context.Context, s devbox.Spec) (*devbox.Box, error) {
	f.specs = append(f.specs, s)
	if f.up != nil {
		return nil, f.up
	}
	return &devbox.Box{Name: "motita-box-x-1", URL: "http://127.0.0.1:49000"}, nil
}
func (f *fakeBoxes) Get(_ context.Context, name, path string) (string, error) {
	return f.get + path, f.getErr
}
func (f *fakeBoxes) Shot(_ context.Context, _, _, out string, w, h int) error {
	f.shots = append(f.shots, out)
	if f.shotErr == nil {
		_ = os.MkdirAll(filepath.Dir(out), 0o755)
		_ = os.WriteFile(out, []byte("png"), 0o644)
	}
	return f.shotErr
}
func (f *fakeBoxes) Down(context.Context, string) error { f.downs++; return nil }
func (f *fakeBoxes) DownAll()                           { f.all++ }
func (f *fakeBoxes) Runtime() string                    { return "podman" }

func boxAgent(t *testing.T, fb *fakeBoxes, answer bool) (*fixture, *[]ApprovalRequest) {
	t.Helper()
	e := fileAgent(t)
	old := newBoxes
	t.Cleanup(func() { newBoxes = old })
	newBoxes = func(context.Context) (boxes, error) { return fb, nil }
	asked := &[]ApprovalRequest{}
	e.agent.cfg.Agent.Policy.Enforce = true
	e.agent.SetApprover(func(_ context.Context, r ApprovalRequest) (bool, error) {
		*asked = append(*asked, r)
		return answer, nil
	})
	return e, asked
}

func box(e *fixture, kind, cmd string) (string, error) {
	return e.agent.runActions(context.Background(), []Command{{Kind: kind, Description: "d", Command: cmd}}, "")
}

func TestBoxUpAsksThenStartsAndTheRestNeedNoQuestion(t *testing.T) {
	fb := &fakeBoxes{get: "GET "}
	e, asked := boxAgent(t, fb, true)
	out, err := box(e, "box_up", "image: nginx:alpine\nport: 80")
	if err != nil || !strings.Contains(out, "reachable only by motita at http://127.0.0.1:49000") {
		t.Fatalf("%v %q", err, out)
	}
	if len(*asked) != 1 || (*asked)[0].Rule != "box-up" || !strings.Contains((*asked)[0].Command, "nginx:alpine") ||
		!strings.Contains((*asked)[0].Reason, "127.0.0.1 only") {
		t.Fatalf("the person is asked what will run: %+v", *asked)
	}
	if got := fb.specs[0]; got.Image != "nginx:alpine" || got.Port != 80 {
		t.Fatalf("%+v", got)
	}

	if out, _ = box(e, "box_get", "path: /health"); !strings.Contains(out, "GET /health") {
		t.Fatalf("%q", out)
	}
	out, err = box(e, "box_shot", "path: /\nname: Home-After\nsize: 800x600")
	if err != nil || !strings.Contains(out, ".motita/previews/home-after.png") {
		t.Fatalf("%v %q", err, out)
	}
	if want := filepath.Join(e.dir, ".motita", "previews", "home-after.png"); fb.shots[0] != want {
		t.Fatalf("saved at %s, want %s", fb.shots[0], want)
	}
	if out, _ = box(e, "box_down", ""); !strings.Contains(out, "taken down") || fb.downs != 1 {
		t.Fatalf("%q", out)
	}
	if len(*asked) != 1 {
		t.Fatalf("only starting asks: %d", len(*asked))
	}
	e.agent.endBoxes()
	e.agent.endBoxes()
	if fb.all != 1 || e.agent.box != nil {
		t.Fatalf("the run's end removes the boxes once: %d", fb.all)
	}
}

func TestBoxUpDeclinedOrWithoutApprover(t *testing.T) {
	fb := &fakeBoxes{}
	e, _ := boxAgent(t, fb, false)
	out, err := box(e, "box_up", "image: nginx\nport: 80")
	if err == nil || !strings.Contains(out, "declined") || len(fb.specs) != 0 {
		t.Fatalf("declined: %v %q", err, out)
	}
	e.agent.approver = nil
	if out, err = box(e, "box_up", "image: nginx\nport: 80"); err == nil || !strings.Contains(out, "nobody to ask") {
		t.Fatalf("no approver: %v %q", err, out)
	}
	// With the policy off nothing asks, like every other action.
	e.agent.cfg.Agent.Policy.Enforce = false
	if out, err = box(e, "box_up", "image: nginx\nport: 80"); err != nil || !strings.Contains(out, "is up") {
		t.Fatalf("enforce off: %v %q", err, out)
	}
}

func TestBoxUpBuildsAFolderOfTheWorkspaceOnly(t *testing.T) {
	fb := &fakeBoxes{}
	e, asked := boxAgent(t, fb, true)
	if err := os.MkdirAll(filepath.Join(e.dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, _ := box(e, "box_up", "dir: app\nport: 3000"); !strings.Contains(out, "no Dockerfile or Containerfile") {
		t.Fatalf("%q", out)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "app", "Containerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := box(e, "box_up", "dir: app\nport: 3000"); err != nil || !strings.Contains(out, "is up") {
		t.Fatalf("%v %q", err, out)
	}
	if fb.specs[0].Dir != filepath.Join(e.dir, "app") || !strings.Contains((*asked)[0].Reason, "built in app") {
		t.Fatalf("%+v %+v", fb.specs, *asked)
	}
	out, err := box(e, "box_up", "dir: ../../etc\nport: 80")
	if err == nil || !strings.Contains(out, "outside the working directory") {
		t.Fatalf("outside the workspace is refused: %v %q", err, out)
	}
}

func TestBoxUpInternalAndCompose(t *testing.T) {
	fb := &fakeBoxes{}
	e, asked := boxAgent(t, fb, true)
	out, err := box(e, "box_up", "image: nginx\nport: 80\nnetwork: internal")
	if err != nil || !strings.Contains(out, "no port on the host") || !fb.specs[0].Internal ||
		!strings.Contains((*asked)[0].Reason, "no port on this machine") {
		t.Fatalf("%v %q %+v", err, out, *asked)
	}
	out, _ = box(e, "box_up", "image: nginx\nport: 80\nnetwork: loopback")
	if !strings.Contains(out, "published on 127.0.0.1 only") || fb.specs[1].Internal {
		t.Fatalf("%q", out)
	}
	if out, _ = box(e, "box_up", "image: nginx\nport: 80\nnetwork: host"); !strings.Contains(out, "internal or loopback") {
		t.Fatalf("%q", out)
	}
	out, err = box(e, "box_up", "compose: stack\nservice: web\nport: 8080")
	if err != nil || fb.specs[2].Compose != filepath.Join(e.dir, "stack") || fb.specs[2].Service != "web" ||
		!strings.Contains((*asked)[2].Reason, "compose project in stack (service web)") {
		t.Fatalf("%v %q %+v", err, out, fb.specs)
	}
	if out, err = box(e, "box_up", "compose: ../../etc\nservice: web\nport: 80"); err == nil || !strings.Contains(out, "outside the working directory") {
		t.Fatalf("%v %q", err, out)
	}
}

func TestBoxUpReportsWhatDoesNotStart(t *testing.T) {
	fb := &fakeBoxes{}
	e, _ := boxAgent(t, fb, true)
	for cmd, want := range map[string]string{
		"image: nginx":                  `"port:" must be`,
		"port: 80":                      "exactly one of image, dir and compose",
		"image: --privileged\nport: 80": "not an image reference",
	} {
		if out, err := box(e, "box_up", cmd); err != nil || !strings.Contains(out, want) {
			t.Errorf("%q: %v %q", cmd, err, out)
		}
	}
	fb.up = errors.New("the service did not answer\nlast log lines:\nboom")
	if out, err := box(e, "box_up", "image: app\nport: 80"); err != nil || !strings.Contains(out, "boom") {
		t.Fatalf("a failed start is information for the model, not a refusal: %v %q", err, out)
	}
	newBoxes = func(context.Context) (boxes, error) { return nil, errors.New("no container runtime answers") }
	e.agent.box = nil
	if out, err := box(e, "box_up", "image: app\nport: 80"); err != nil || !strings.Contains(out, "no container runtime") {
		t.Fatalf("%v %q", err, out)
	}
}

func TestBoxActionsFailInWordsTheModelCanUse(t *testing.T) {
	fb := &fakeBoxes{}
	e, _ := boxAgent(t, fb, true)
	for _, k := range []string{"box_get", "box_shot", "box_down"} {
		if out, err := box(e, k, "path: /"); err == nil || !strings.Contains(out, "start one with box_up first") {
			t.Errorf("%s with nothing up: %v %q", k, err, out)
		}
	}
	if _, err := box(e, "box_up", "image: app\nport: 80"); err != nil {
		t.Fatal(err)
	}
	fb.getErr = errors.New("GET / failed")
	if out, err := box(e, "box_get", "path: /"); err != nil || !strings.Contains(out, "[failed: GET / failed]") {
		t.Fatalf("%v %q", err, out)
	}
	for cmd, want := range map[string]string{
		"path: /\nname: ../x":                  "not a file name",
		"path: /\nname: a\nsize: wide":         "written like 1280x800",
		"path: /\nname: a\nsize: 0x0":          "written like 1280x800",
		"path: /\nname: a\nsize: 1280x800junk": "written like 1280x800",
	} {
		if out, err := box(e, "box_shot", cmd); err != nil || !strings.Contains(out, want) {
			t.Errorf("%q: %v %q", cmd, err, out)
		}
	}
	// A preview folder that is a link out of the workspace is not written through.
	if err := os.MkdirAll(filepath.Join(e.dir, ".motita"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(e.dir, ".motita", "previews")); err == nil {
		if out, _ := box(e, "box_shot", "path: /\nname: a"); !strings.Contains(out, "failed") || len(fb.shots) != 0 {
			t.Fatalf("%q", out)
		}
		_ = os.Remove(filepath.Join(e.dir, ".motita", "previews"))
	}
	fb.shotErr = errors.New("the browser failed")
	if out, _ := box(e, "box_shot", "path: /\nname: a"); !strings.Contains(out, "the browser failed") {
		t.Fatalf("%q", out)
	}
}

func TestBoxActionsAreRefusedWhereTheyMustNotRun(t *testing.T) {
	fb := &fakeBoxes{}
	e, _ := boxAgent(t, fb, true)
	e.agent.isChild = true
	if out, err := box(e, "box_up", "image: app\nport: 80"); err == nil || !strings.Contains(out, "only the main agent") {
		t.Fatalf("%v %q", err, out)
	}
	e.agent.isChild = false
	e.agent.cfg.Agent.ReadOnly = true
	if out, err := box(e, "box_up", "image: app\nport: 80"); err == nil || !strings.Contains(out, "read-only") || len(fb.specs) != 0 {
		t.Fatalf("%v %q", err, out)
	}
	if handled, _, _ := e.agent.runBoxAction(context.Background(), "write_file", Command{}); handled {
		t.Fatal("not a box kind")
	}
}

func TestTheRealManagerNeedsARuntime(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if _, err := newBoxes(context.Background()); err == nil {
		t.Fatal("no podman and no docker on PATH")
	}
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Skip("cannot write a fake runtime here")
	}
	m, err := newBoxes(context.Background())
	if err != nil || m.Runtime() != "podman" {
		t.Fatalf("a runtime that answers info is used: %v", err)
	}
}

func TestTheEndOfTheRunRemovesTheBoxes(t *testing.T) {
	fb := &fakeBoxes{}
	e, _ := boxAgent(t, fb, true)
	if _, err := box(e, "box_up", "image: app\nport: 80"); err != nil {
		t.Fatal(err)
	}
	e.agent.startFleet(task.Task{Description: "x"})
	e.agent.endFleet(TaskResult{})
	if fb.all != 1 {
		t.Fatalf("endFleet must take the boxes down: %d", fb.all)
	}
}
