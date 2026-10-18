package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/madkoding/motita/internal/devbox"
	"github.com/madkoding/motita/internal/policy"
	"github.com/madkoding/motita/internal/redact"
)

// SERVICES IN A BOX, THAT ONLY MOTITA REACHES.
//
// Four actions let the run start the project in a container, test it and photograph it without
// the service being exposed: box_up, box_get, box_shot and box_down (see internal/devbox for how
// the container is kept private). They are actions rather than shell commands on purpose: the
// agent's commands run without a network and cannot reach a published port, and the model must
// not be the one writing the runtime's flags.
//
// Starting a box is the one step that is asked about, like any action that leaves the workspace:
// it runs a container and may pull an image. Reading a box that was started that way, taking its
// picture and taking it down touch nothing the user did not approve. Every box goes when the run
// ends.
//
//	box_up:    lines "image: <ref>" or "dir: <folder with a Dockerfile>", and "port: <n>".
//	box_get:   lines "box: <name>" (optional with one box up) and "path: /x".
//	box_shot:  as box_get, plus "name: <file>" and optionally "size: 1280x800".
//	box_down:  "box: <name>".

// PreviewDir is where a screenshot is saved so the person sees it with the result; it is the
// folder the gateway reads when the run ends (gateway.PreviewDir, which a test keeps equal).
const PreviewDir = ".motita/previews"

// boxes is what the actions need from internal/devbox; a test replaces the real one.
type boxes interface {
	Up(ctx context.Context, s devbox.Spec) (*devbox.Box, error)
	Get(ctx context.Context, name, path string) (string, error)
	Shot(ctx context.Context, name, path, out string, width, height int) error
	Down(ctx context.Context, name string) error
	DownAll()
	Runtime() string
}

// newBoxes starts the manager of a run: it finds the runtime (podman first) and names the run.
var newBoxes = func(ctx context.Context) (boxes, error) {
	rt, err := devbox.Detect(ctx)
	if err != nil {
		return nil, err
	}
	var id [4]byte
	_, _ = rand.Read(id[:])
	return devbox.New(rt, hex.EncodeToString(id[:])), nil
}

// boxState is the run's manager, created by the first box_up.
type boxState struct {
	m boxes
}

func isBoxKind(kind string) bool {
	switch kind {
	case "box_up", "box_get", "box_shot", "box_down":
		return true
	}
	return false
}

// runBoxAction carries out a box_* action. It reports whether the kind was one of them, what to
// show the model, and an error only for a REFUSAL; a service that fails to start is a fact about
// the project, shown to the model like a failing command.
func (a *Agent) runBoxAction(ctx context.Context, kind string, action Command) (bool, string, error) {
	if !isBoxKind(kind) {
		return false, "", nil
	}
	refuse := func(msg string) (bool, string, error) {
		return true, "[refused: " + msg + "]", errors.New(msg)
	}
	switch {
	case a.isChild:
		return refuse("only the main agent starts boxes")
	case a.readOnly():
		return refuse("this session is read-only: no service can be started")
	}
	kv := parseLines(action.Command)
	if kind == "box_up" {
		return a.boxUp(ctx, action, kv)
	}
	if a.box == nil {
		return refuse("no box is up: start one with box_up first")
	}
	var out string
	var err error
	switch kind {
	case "box_get":
		out, err = a.box.m.Get(ctx, kv["box"], kv["path"])
	case "box_shot":
		out, err = a.boxShot(ctx, kv)
	default:
		if err = a.box.m.Down(ctx, kv["box"]); err == nil {
			out = "[box taken down]"
		}
	}
	if err != nil {
		return true, "[failed: " + err.Error() + "]", nil
	}
	return true, redact.String(out), nil
}

// boxUp asks the user, then starts the service.
func (a *Agent) boxUp(ctx context.Context, action Command, kv map[string]string) (bool, string, error) {
	fail := func(msg string) (bool, string, error) { return true, "[failed: " + msg + "]", nil }
	port, err := strconv.Atoi(kv["port"])
	if err != nil {
		return fail(`"port:" must be the number the service listens on inside the container`)
	}
	spec := devbox.Spec{Image: kv["image"], Port: port}
	if rel := kv["dir"]; rel != "" {
		dir, err := a.workspacePath(rel)
		if err != nil {
			return true, "[refused: " + err.Error() + "]", err
		}
		if !hasContainerfile(dir) {
			return fail(rel + " has no Dockerfile or Containerfile")
		}
		spec.Dir = dir
	}
	if err := spec.Validate(); err != nil {
		return fail(err.Error())
	}
	if a.cfg.Agent.Policy.Enforce {
		what := "image " + spec.Image
		if spec.Dir != "" {
			what = "the image built from " + kv["dir"]
		}
		plan := RequestPlan{Verdict: policy.Ask, Rule: "box-up", Reason: "it starts a container from " + what +
			", published on 127.0.0.1 only and removed when this run ends; the service inside can use the network"}
		approved, err := a.approve(ctx, plan, "box_up "+strings.Join(strings.Fields(action.Command), " "))
		if err != nil {
			return true, "[not approved: " + err.Error() + "]", err
		}
		if !approved {
			err := errors.New("the user declined to start the box")
			return true, "[not approved: " + err.Error() + "]", err
		}
	}
	if a.box == nil {
		m, err := newBoxes(ctx)
		if err != nil {
			return fail(err.Error())
		}
		a.box = &boxState{m: m}
	}
	b, err := a.box.m.Up(ctx, spec)
	if err != nil {
		return fail(err.Error())
	}
	return true, fmt.Sprintf("[box %s is up with %s, reachable only by motita at %s: use box_get and box_shot, "+
		"not curl or a browser in a command]", b.Name, a.box.m.Runtime(), b.URL), nil
}

// boxShot saves a picture of a page of the box under the workspace's preview folder.
func (a *Agent) boxShot(ctx context.Context, kv map[string]string) (string, error) {
	file, err := devbox.ShotName(kv["name"])
	if err != nil {
		return "", err
	}
	w, h := 1280, 800
	if s := kv["size"]; s != "" {
		ws, hs, ok := strings.Cut(strings.ToLower(s), "x")
		w, _ = strconv.Atoi(ws)
		h, _ = strconv.Atoi(hs)
		if !ok || w == 0 || h == 0 {
			return "", fmt.Errorf(`"size:" is written like 1280x800, not %q`, s)
		}
	}
	out, err := a.workspacePath(PreviewDir + "/" + file)
	if err != nil {
		return "", err
	}
	if err := a.box.m.Shot(ctx, kv["box"], kv["path"], out, w, h); err != nil {
		return "", err
	}
	return fmt.Sprintf("[saved %s/%s: it is shown to the person with the result]", PreviewDir, file), nil
}

// endBoxes removes the boxes of the run that is ending.
func (a *Agent) endBoxes() {
	if a.box != nil {
		a.box.m.DownAll()
		a.box = nil
	}
}

func hasContainerfile(dir string) bool {
	for _, n := range []string{"Dockerfile", "Containerfile"} {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// parseLines reads "key: value" lines; keys are lowercase, unknown ones are kept and ignored.
func parseLines(s string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			kv[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return kv
}
