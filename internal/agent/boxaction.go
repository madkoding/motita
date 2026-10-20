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
//	box_up:    lines "image: <ref>", "dir: <folder with a Dockerfile>" or "compose: <folder>" with
//	           "service: <name>", and "port: <n>"; "network: internal" publishes nothing on the host.
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

// boxKinds are the actions of this file.
var boxKinds = map[string]bool{"box_up": true, "box_get": true, "box_shot": true, "box_down": true}

func isBoxKind(kind string) bool { return boxKinds[kind] }

// What the model reads when an action is refused (the run's rules), fails (a fact about the
// project) or is not approved.
func boxRefused(msg string) (string, error) { return "[refused: " + msg + "]", errors.New(msg) }
func boxFailed(msg string) (string, error)  { return "[failed: " + msg + "]", nil }

// runBoxAction carries out a box_* action. It reports whether the kind was one of them, what to
// show the model, and an error only for a REFUSAL; a service that fails to start is a fact about
// the project, shown to the model like a failing command.
func (a *Agent) runBoxAction(ctx context.Context, kind string, action Command) (bool, string, error) {
	if !isBoxKind(kind) {
		return false, "", nil
	}
	out, err := a.boxStep(ctx, kind, action)
	return true, out, err
}

// boxStep is runBoxAction for a kind that is one of ours.
func (a *Agent) boxStep(ctx context.Context, kind string, action Command) (string, error) {
	if a.isChild {
		return boxRefused("only the main agent starts boxes")
	}
	if a.readOnly() {
		return boxRefused("this session is read-only: no service can be started")
	}
	kv := parseLines(action.Command)
	if kind == "box_up" {
		return a.boxUp(ctx, action, kv)
	}
	if a.box == nil {
		return boxRefused("no box is up: start one with box_up first")
	}
	out, err := a.boxUse(ctx, kind, kv)
	if err != nil {
		return boxFailed(err.Error())
	}
	return redact.String(out), nil
}

// boxUse is get, shot or down on a box that is up.
func (a *Agent) boxUse(ctx context.Context, kind string, kv map[string]string) (string, error) {
	switch kind {
	case "box_get":
		return a.box.m.Get(ctx, kv["box"], kv["path"])
	case "box_shot":
		return a.boxShot(ctx, kv)
	default:
		return "[box taken down]", a.box.m.Down(ctx, kv["box"])
	}
}

// boxSpec reads what box_up asks for. The folders are resolved inside the workspace; a message for
// the model is returned when the request cannot be a spec, and a refusal when it leaves the workspace.
func (a *Agent) boxSpec(kv map[string]string) (devbox.Spec, string, error) {
	port, err := strconv.Atoi(kv["port"])
	if err != nil {
		return devbox.Spec{}, `"port:" must be the number the service listens on inside the container`, nil
	}
	spec := devbox.Spec{Image: kv["image"], Port: port, Service: kv["service"],
		Internal: !strings.EqualFold(kv["network"], "loopback")}
	if n := kv["network"]; n != "" && !strings.EqualFold(n, "internal") && !strings.EqualFold(n, "loopback") {
		return spec, `"network:" is internal or loopback (internal is the default)`, nil
	}
	if rel := kv["dir"]; rel != "" {
		dir, err := a.workspacePath(rel)
		if err != nil {
			return spec, "", err
		}
		if !hasContainerfile(dir) {
			return spec, rel + " has no Dockerfile or Containerfile", nil
		}
		spec.Dir = dir
	}
	if rel := kv["compose"]; rel != "" {
		dir, err := a.workspacePath(rel)
		if err != nil {
			return spec, "", err
		}
		spec.Compose = dir
	}
	if err := spec.Validate(); err != nil {
		return spec, err.Error(), nil
	}
	return spec, "", nil
}

// boxQuestion is what the person is asked before a box starts: what runs and how it is kept apart.
func boxQuestion(spec devbox.Spec, kv map[string]string) string {
	what := "a container from image " + spec.Image
	if spec.Dir != "" {
		what = "a container from the image built in " + kv["dir"]
	}
	if spec.Compose != "" {
		what = "the compose project in " + kv["compose"] + " (service " + spec.Service + ")"
	}
	where := "published on 127.0.0.1 only"
	if spec.Internal || spec.Compose != "" {
		where = "with no port on this machine, on a private network"
	}
	return "it starts " + what + ", " + where + ", and removes it when this run ends"
}

// boxUp asks the user, then starts the service.
func (a *Agent) boxUp(ctx context.Context, action Command, kv map[string]string) (string, error) {
	spec, problem, err := a.boxSpec(kv)
	if err != nil {
		return boxRefused(err.Error())
	}
	if problem != "" {
		return boxFailed(problem)
	}
	if a.cfg.Agent.Policy.Enforce {
		plan := RequestPlan{Verdict: policy.Ask, Rule: "box-up", Reason: boxQuestion(spec, kv)}
		approved, err := a.approve(ctx, plan, "box_up "+strings.Join(strings.Fields(action.Command), " "))
		if err == nil && !approved {
			err = errors.New("the user declined to start the box")
		}
		if err != nil {
			return "[not approved: " + err.Error() + "]", err
		}
	}
	if a.box == nil {
		m, err := newBoxes(ctx)
		if err != nil {
			return boxFailed(err.Error())
		}
		a.box = &boxState{m: m}
	}
	b, err := a.box.m.Up(ctx, spec)
	if err != nil {
		return boxFailed(err.Error())
	}
	how := reach(spec.Internal || spec.Compose != "")
	return fmt.Sprintf("[box %s is up with %s, reachable only by motita at %s (%s): use box_get and "+
		"box_shot, not curl or a browser in a command]", b.Name, a.box.m.Runtime(), b.URL, how), nil
}

// reach says how a box is reached, for the model.
func reach(internal bool) string {
	if internal {
		return "no port on the host; helper containers on its private network make the requests"
	}
	return "published on 127.0.0.1 only"
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
