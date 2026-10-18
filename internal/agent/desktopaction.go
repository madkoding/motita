package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/desktop"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/policy"
)

// THE DESKTOP OF THIS COMPUTER, DRIVEN BY THE MODEL.
//
// With agent.desktop_control on, the run can look at the screen and use the mouse, the keyboard and
// the windows (see internal/desktop). Each step is an action and not a shell command: the commands
// run in a sandbox that hides the display on purpose, and the model must not be the one writing
// xdotool's or PowerShell's arguments.
//
// Every step is asked about, a picture of the screen included: it shows whatever the person has
// open. "Allow all for this session" answers them all at once. The picture goes to the model with
// its next round, and is saved where the person sees it with the result.
//
//	desktop_shot:    nothing
//	desktop_click:   "x: <n>", "y: <n>", optionally "button: right|middle" and "double: true"
//	desktop_move:    "x:", "y:"
//	desktop_scroll:  "x:", "y:", "amount: <notches, positive down>"
//	desktop_type:    the text to type, as the whole command
//	desktop_key:     the combination, like ctrl+c or enter, as the whole command
//	desktop_windows: nothing
//	desktop_focus:   the window's title (on macOS, the application), as the whole command

// DesktopRule is the rule a desktop step is asked under.
const DesktopRule = "desktop"

// desktopKinds maps each action to the step it is.
var desktopKinds = map[string]string{
	"desktop_shot": desktop.Shot, "desktop_click": desktop.Click, "desktop_move": desktop.Move,
	"desktop_scroll": desktop.Scroll, "desktop_type": desktop.Type, "desktop_key": desktop.Key,
	"desktop_windows": desktop.Windows, "desktop_focus": desktop.Focus,
}

// desktops is what the actions need from internal/desktop; a test replaces the real one.
type desktops interface {
	Do(ctx context.Context, r desktop.Request) (desktop.Result, error)
	Label() string
}

// newDesktop reaches this computer's desktop.
var newDesktop = func() (desktops, error) { return desktop.New() }

// desktopState is the run's desktop, reached by the first step, and the picture the next round
// shows the model.
type desktopState struct {
	mu    sync.Mutex
	d     desktops
	shot  *llm.Image
	shots int
}

// desktopStep carries out a desktop_* action, the step that kind names. It answers what to show the
// model, and an error only for a REFUSAL; a step that fails is a fact about the desktop, shown to
// the model like a failing command.
func (a *Agent) desktopStep(ctx context.Context, kind, step string, action Command) (string, error) {
	switch {
	case !a.cfg.Agent.DesktopControl:
		return boxRefused("controlling this computer's desktop is off: the person turns it on in " +
			"Settings → Security. Do the work another way")
	case a.isChild:
		return boxRefused("only the main agent uses the desktop")
	case a.readOnly():
		return boxRefused("this session is read-only: the desktop is not used")
	default:
		// Allowed here: the person is asked below.
	}
	req, err := desktopRequest(step, action.Command)
	if err != nil {
		return boxFailed(err.Error())
	}
	command := kind + " " + strings.Join(strings.Fields(action.Command), " ")
	plan := RequestPlan{Verdict: policy.Ask, Rule: DesktopRule, Reason: desktopQuestion(req)}
	approved, err := a.approve(ctx, plan, strings.TrimSpace(command))
	if err == nil && !approved {
		err = errors.New("the user declined this step on the desktop")
	}
	if err != nil {
		return "[not approved: " + err.Error() + "]", err
	}
	d, err := a.desktop()
	if err != nil {
		return boxFailed(err.Error())
	}
	res, err := d.Do(ctx, req)
	if err != nil {
		return boxFailed(err.Error())
	}
	if res.Picture != nil {
		return a.keepShot(res), nil
	}
	return "[" + res.Text + "]", nil
}

// desktop is the run's desktop, reached the first time a step needs it.
func (a *Agent) desktop() (desktops, error) {
	a.desk.mu.Lock()
	defer a.desk.mu.Unlock()
	if a.desk.d == nil {
		d, err := newDesktop()
		if err != nil {
			return nil, err
		}
		a.desk.d = d
	}
	return a.desk.d, nil
}

// keepShot holds the picture for the next round and saves it for the person.
func (a *Agent) keepShot(res desktop.Result) string {
	a.desk.mu.Lock()
	a.desk.shot = &llm.Image{MediaType: "image/jpeg", Data: res.Picture.JPEG}
	a.desk.shots++
	n := a.desk.shots
	a.desk.mu.Unlock()
	saved := ""
	name := fmt.Sprintf("desktop-%s-%d.jpg", time.Now().Format("150405"), n)
	path, err := a.workspacePath(PreviewDir + "/" + name)
	if err == nil && writePreview(path, res.Picture.JPEG) == nil {
		saved = " Saved as " + PreviewDir + "/" + name + ", which the person sees with the result."
	}
	return "[" + res.Text + ". The picture is attached to the next message." + saved + "]"
}

// writePreview writes a picture under the preview folder.
func writePreview(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// takeShot is the picture the next round shows the model, once.
func (a *Agent) takeShot() []llm.Image {
	a.desk.mu.Lock()
	defer a.desk.mu.Unlock()
	if a.desk.shot == nil {
		return nil
	}
	img := *a.desk.shot
	a.desk.shot = nil
	return []llm.Image{img}
}

// desktopRequest reads an action's command as the step it names.
func desktopRequest(step, command string) (desktop.Request, error) {
	req := desktop.Request{Kind: step}
	text := strings.TrimSpace(command)
	switch step {
	case desktop.Type:
		req.Text = command // typed exactly, spaces and new lines included
	case desktop.Key:
		req.Keys = text
	case desktop.Focus:
		req.Window = text
	case desktop.Click, desktop.Move, desktop.Scroll:
		return pointRequest(req, parseLines(command))
	default:
		// shot and windows take nothing.
	}
	return req, nil
}

// pointRequest reads the point, and the button or the amount, of a pointer step.
func pointRequest(req desktop.Request, kv map[string]string) (desktop.Request, error) {
	var errX, errY error
	req.X, errX = strconv.Atoi(kv["x"])
	req.Y, errY = strconv.Atoi(kv["y"])
	if errX != nil || errY != nil {
		return req, errors.New(`"x:" and "y:" are the point, in the pixels of the last desktop_shot`)
	}
	req.Button = kv["button"]
	req.Double = strings.EqualFold(kv["double"], "true")
	if req.Kind == desktop.Scroll {
		n, err := strconv.Atoi(kv["amount"])
		if err != nil {
			return req, errors.New(`"amount:" is the number of notches to scroll: positive down, negative up`)
		}
		req.Amount = n
	}
	return req, nil
}

// desktopQuestion is what the person is asked before a step.
func desktopQuestion(r desktop.Request) string {
	switch r.Kind {
	case desktop.Shot:
		return "it takes a picture of your screen and shows it to the model"
	case desktop.Windows:
		return "it reads the titles of your open windows"
	case desktop.Type:
		return fmt.Sprintf("it types %q where the keyboard is", truncate(r.Text, 80))
	case desktop.Key:
		return "it presses " + r.Keys + " on your keyboard"
	case desktop.Focus:
		return "it brings " + r.Window + " to the front"
	default:
		return fmt.Sprintf("it uses your mouse (%s) at %d,%d of the last picture of your screen", r.Kind, r.X, r.Y)
	}
}

// desktopDocs is the section of the execute prompt that teaches the desktop actions, or "" while
// they are off, so the model is not offered what it cannot use.
func (a *Agent) desktopDocs() string {
	if !a.cfg.Agent.DesktopControl || a.isChild {
		return ""
	}
	return desktopPrompt
}

// desktopPrompt teaches the desktop actions.
const desktopPrompt = `## THIS COMPUTER'S DESKTOP

The person let you see this computer's screen and use its mouse, keyboard and windows. Every step
asks them first, so take only the steps the task needs. Look before you act: desktop_shot attaches
a picture of the screen to your next message, and the x and y of every other step are that
picture's pixels. Take a new picture after anything that changes the screen.

- {"kind": "desktop_shot", "description": "why", "command": ""}
- {"kind": "desktop_click", "description": "why", "command": "x: 640\ny: 400"} (add "button: right"
  or "double: true" when needed)
- {"kind": "desktop_move", "description": "why", "command": "x: 640\ny: 400"}
- {"kind": "desktop_scroll", "description": "why", "command": "x: 640\ny: 400\namount: 3"} (positive scrolls down)
- {"kind": "desktop_type", "description": "why", "command": "text to type"}
- {"kind": "desktop_key", "description": "why", "command": "ctrl+s"} (enter, tab, esc, alt+tab, super...)
- {"kind": "desktop_windows", "description": "why", "command": ""} lists the open windows
- {"kind": "desktop_focus", "description": "why", "command": "window title"} brings it to the front

Never type passwords or other secrets you were not given for that purpose, and stop to ask when the
screen shows something the task did not expect (a login, a payment, a warning).
`
