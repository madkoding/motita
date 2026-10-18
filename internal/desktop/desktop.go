// Package desktop lets the agent see and drive the desktop of the computer motita runs on: a
// picture of the screen, the mouse, the keyboard and the open windows.
//
// It is off unless the person turns it on (agent.desktop_control), and the agent asks before every
// step (see internal/agent/desktopaction.go); this package only carries a step out. Each one is a
// short program the system already has, run by motita itself and not in the sandbox, which hides
// the display from the agent's commands on purpose:
//
//   - Linux with X11:     xdotool, maim / scrot / ImageMagick's import, wmctrl
//   - Linux with Wayland: grim and ydotool (its service, ydotoold, must be running)
//   - macOS:              screencapture, osascript and cliclick
//   - Windows:            Windows PowerShell, with user32 and System.Windows.Forms
//
// The model reads the screen as a picture no larger than maxSide pixels on its longer side, and
// the coordinates it answers with are that picture's: they are mapped back to the screen's with
// the scale of the last picture, so no click lands before one was taken.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The steps a Request names.
const (
	Shot    = "shot"
	Click   = "click"
	Move    = "move"
	Type    = "type"
	Key     = "key"
	Scroll  = "scroll"
	Windows = "windows"
	Focus   = "focus"
)

// stepTimeout bounds one program of a step.
const stepTimeout = 20 * time.Second

// Request is one step on the desktop. X and Y are in the pixels of the last picture.
type Request struct {
	Kind   string
	X, Y   int
	Button string // left (default), right or middle
	Double bool
	Text   string // what Type writes
	Keys   string // what Key presses, like "ctrl+c" or "enter"
	Amount int    // Scroll: notches, positive down
	Window string // what Focus brings forward: a title or an application
}

// Result is what a step gives back: a line for the model and, for Shot, the picture.
type Result struct {
	Text    string
	Picture *Picture
}

// cmd is one program a step runs.
type cmd struct {
	name string
	args []string
}

// backend is how one kind of desktop does each step. A nil list is a step this desktop cannot do.
type backend interface {
	label() string
	shot(file string) []cmd
	// size prints the screen's size in the units the pointer moves in ("W H" or "x, y, W, H"),
	// or is nil when those are the picture's own pixels.
	size() []cmd
	click(x, y int, button string, double bool) []cmd
	move(x, y int) []cmd
	write(text string) []cmd
	press(k keys) ([]cmd, error)
	scroll(x, y, amount int) []cmd
	windows() []cmd
	focus(window string) []cmd
}

// Desktop carries out steps on this computer's desktop.
type Desktop struct {
	be   backend
	run  func(ctx context.Context, c cmd) (string, error)
	look func(name string) (string, error)

	mu    sync.Mutex
	scale float64 // screen units per picture pixel, from the last picture; 0 before one
}

// ErrNoDisplay is a computer with no desktop motita can reach.
var ErrNoDisplay = errors.New("there is no desktop to control here: motita is not running inside a graphical " +
	"session (DISPLAY and WAYLAND_DISPLAY are not set). Start motita from the desktop session, not as a service")

// New is the desktop of this computer, or ErrNoDisplay.
func New() (*Desktop, error) {
	be, err := detect(runtime.GOOS, os.Getenv, exec.LookPath)
	if err != nil {
		return nil, err
	}
	return &Desktop{be: be, run: runCmd, look: exec.LookPath}, nil
}

// detect is the backend for goos and the session the environment describes.
func detect(goos string, getenv func(string) string, look func(string) (string, error)) (backend, error) {
	switch goos {
	case "windows":
		return winBackend{}, nil
	case "darwin":
		return macBackend{}, nil
	default:
		return unixBackend(getenv, look)
	}
}

// unixBackend is the backend of the graphical session the environment names: Wayland, then X11.
func unixBackend(getenv func(string) string, look func(string) (string, error)) (backend, error) {
	if getenv("WAYLAND_DISPLAY") != "" {
		return waylandBackend{}, nil
	}
	if getenv("DISPLAY") != "" {
		return x11Backend{shooter: firstFound(look, "maim", "scrot", "import")}, nil
	}
	return nil, ErrNoDisplay
}

// firstFound is the first of names installed, or the first name when none is.
func firstFound(look func(string) (string, error), names ...string) string {
	for _, n := range names {
		if _, err := look(n); err == nil {
			return n
		}
	}
	return names[0]
}

// runCmd runs c and answers what it printed.
func runCmd(ctx context.Context, c cmd) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.name, c.args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s failed: %w: %s", c.name, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Label names the desktop, for the model and the person.
func (d *Desktop) Label() string { return d.be.label() }

// Do carries out r.
func (d *Desktop) Do(ctx context.Context, r Request) (Result, error) {
	switch r.Kind {
	case Shot:
		return d.shot(ctx)
	case Windows:
		out, err := d.steps(ctx, "listing the windows", d.be.windows())
		return Result{Text: listOrNone(out)}, err
	case Focus:
		return d.focus(ctx, r.Window)
	case Type:
		return d.write(ctx, r.Text)
	case Key:
		return d.press(ctx, r.Keys)
	default:
		return d.pointer(ctx, r)
	}
}

// pointer is a step at a point of the screen: click, move or scroll.
func (d *Desktop) pointer(ctx context.Context, r Request) (Result, error) {
	x, y, err := d.toScreen(r.X, r.Y)
	if err != nil {
		return Result{}, err
	}
	switch r.Kind {
	case Click:
		return d.click(ctx, r, x, y)
	case Move:
		_, err = d.steps(ctx, "moving the pointer", d.be.move(x, y))
		return Result{Text: fmt.Sprintf("pointer at %d,%d", r.X, r.Y)}, err
	case Scroll:
		return d.scroll(ctx, r, x, y)
	default:
		return Result{}, fmt.Errorf("%q is not a desktop step", r.Kind)
	}
}

// click clicks r's button at x,y of the screen.
func (d *Desktop) click(ctx context.Context, r Request, x, y int) (Result, error) {
	button := strings.ToLower(strings.TrimSpace(r.Button))
	if button == "" {
		button = "left"
	}
	if button != "left" && button != "right" && button != "middle" {
		return Result{}, fmt.Errorf("button is left, right or middle, not %q", r.Button)
	}
	_, err := d.steps(ctx, "clicking", d.be.click(x, y, button, r.Double))
	return Result{Text: fmt.Sprintf("clicked %s at %d,%d", button, r.X, r.Y)}, err
}

// scroll turns the wheel r.Amount notches at x,y of the screen.
func (d *Desktop) scroll(ctx context.Context, r Request, x, y int) (Result, error) {
	if r.Amount == 0 {
		return Result{}, errors.New("amount is the number of notches to scroll: positive down, negative up")
	}
	_, err := d.steps(ctx, "scrolling", d.be.scroll(x, y, r.Amount))
	return Result{Text: fmt.Sprintf("scrolled %d at %d,%d", r.Amount, r.X, r.Y)}, err
}

// toScreen maps a point of the last picture to the screen.
func (d *Desktop) toScreen(x, y int) (int, int, error) {
	d.mu.Lock()
	scale := d.scale
	d.mu.Unlock()
	if scale == 0 {
		return 0, 0, errors.New("take a picture of the screen first (desktop_shot): " +
			"the coordinates are that picture's pixels")
	}
	if x < 0 || y < 0 {
		return 0, 0, fmt.Errorf("%d,%d is off the screen", x, y)
	}
	return int(float64(x)*scale + 0.5), int(float64(y)*scale + 0.5), nil
}

// write types text where the keyboard focus is.
func (d *Desktop) write(ctx context.Context, text string) (Result, error) {
	if text == "" {
		return Result{}, errors.New("text is what to type, and it is empty")
	}
	_, err := d.steps(ctx, "typing", d.be.write(text))
	return Result{Text: fmt.Sprintf("typed %d characters", len([]rune(text)))}, err
}

// press presses a key or a combination.
func (d *Desktop) press(ctx context.Context, combo string) (Result, error) {
	k, err := parseKeys(combo)
	if err != nil {
		return Result{}, err
	}
	cmds, err := d.be.press(k)
	if err != nil {
		return Result{}, err
	}
	_, err = d.steps(ctx, "pressing keys", cmds)
	return Result{Text: "pressed " + k.String()}, err
}

// focus brings a window forward.
func (d *Desktop) focus(ctx context.Context, window string) (Result, error) {
	window = strings.TrimSpace(window)
	if window == "" {
		return Result{}, errors.New("window is the title (or, on macOS, the application) to bring forward")
	}
	_, err := d.steps(ctx, "bringing a window forward", d.be.focus(window))
	return Result{Text: "brought forward: " + window}, err
}

// steps runs cmds in order and answers what the last printed. An empty list is a step this
// desktop cannot do; a program that is not installed is named with how to install it.
func (d *Desktop) steps(ctx context.Context, what string, cmds []cmd) (string, error) {
	if len(cmds) == 0 {
		return "", fmt.Errorf("%s is not possible on this desktop (%s)", what, d.be.label())
	}
	var out string
	for _, c := range cmds {
		if _, err := d.look(c.name); err != nil {
			return "", fmt.Errorf("%s needs %s, which is not installed: %s", what, c.name, installHint(c.name))
		}
		var err error
		if out, err = d.run(ctx, c); err != nil {
			return "", err
		}
	}
	return out, nil
}

// listOrNone is a listing, or a line that says it is empty.
func listOrNone(out string) string {
	if strings.TrimSpace(out) == "" {
		return "(no windows)"
	}
	return strings.TrimSpace(out)
}

// installHints say how to get each program a backend runs.
var installHints = map[string]string{
	"xdotool":  "install xdotool (apt install xdotool)",
	"maim":     "install maim, scrot or ImageMagick (apt install maim)",
	"wmctrl":   "install wmctrl (apt install wmctrl)",
	"grim":     "install grim (apt install grim)",
	"ydotool":  "install ydotool and keep its service running (apt install ydotool; systemctl --user start ydotoold)",
	"cliclick": "install cliclick (brew install cliclick)",
}

// installHint says how to get the program name.
func installHint(name string) string {
	if h, ok := installHints[name]; ok {
		return h
	}
	return "install " + name
}

// tempShot is a fresh path for a picture of the screen, and the folder to remove after.
func tempShot() (string, string, error) {
	dir, err := os.MkdirTemp("", "motita-desktop-")
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, "screen.png"), dir, nil
}
