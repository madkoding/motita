package desktop

import (
	"fmt"
	"strconv"
	"strings"
)

// repeatFlag is xdotool's and ydotool's option to click more than once.
const repeatFlag = "--repeat"

// x11Backend drives an X11 session with xdotool, a screenshot program and wmctrl.
type x11Backend struct {
	shooter string // maim, scrot or import, whichever is installed
}

func (x11Backend) label() string { return "Linux, X11" }

func (b x11Backend) shot(file string) []cmd {
	switch b.shooter {
	case "scrot":
		return []cmd{{"scrot", []string{"--overwrite", file}}}
	case "import":
		return []cmd{{"import", []string{"-window", "root", file}}}
	default:
		return []cmd{{"maim", []string{file}}}
	}
}

func (x11Backend) size() []cmd { return nil }

// xButtons are the X buttons of each mouse button.
var xButtons = map[string]string{"left": "1", "middle": "2", "right": "3"}

func (x11Backend) click(x, y int, button string, double bool) []cmd {
	args := []string{"mousemove", itoa(x), itoa(y), "click"}
	if double {
		args = append(args, repeatFlag, "2")
	}
	return []cmd{{"xdotool", append(args, xButtons[button])}}
}

func (x11Backend) move(x, y int) []cmd {
	return []cmd{{"xdotool", []string{"mousemove", itoa(x), itoa(y)}}}
}

func (x11Backend) write(text string) []cmd {
	return []cmd{{"xdotool", []string{"type", "--delay", "12", "--", text}}}
}

// xKeys are the X keysyms of the named keys and of the symbols that have no keysym of their own
// spelling.
var xKeys = map[string]string{
	"enter": "Return", "tab": "Tab", "esc": "Escape", "backspace": "BackSpace", "delete": "Delete",
	"space": "space", "up": "Up", "down": "Down", "left": "Left", "right": "Right", "home": "Home",
	"end": "End", "pageup": "Prior", "pagedown": "Next",
	"+": "plus", "-": "minus", "=": "equal", "/": "slash", "\\": "backslash", ".": "period",
	",": "comma", ";": "semicolon", "'": "apostrophe", "`": "grave", "[": "bracketleft", "]": "bracketright",
}

func (x11Backend) press(k keys) ([]cmd, error) {
	parts := append([]string{}, k.mods...)
	if k.key != "" {
		parts = append(parts, xKeyName(k.key))
	}
	return []cmd{{"xdotool", []string{"key", "--clearmodifiers", "--", strings.Join(parts, "+")}}}, nil
}

// xKeyName is key as xdotool names it.
func xKeyName(key string) string {
	if n, ok := xKeys[key]; ok {
		return n
	}
	if strings.HasPrefix(key, "f") && len(key) > 1 {
		return strings.ToUpper(key) // f5 -> F5
	}
	return key
}

func (x11Backend) scroll(x, y, amount int) []cmd {
	button := "5" // down
	if amount < 0 {
		button, amount = "4", -amount
	}
	return []cmd{{"xdotool", []string{"mousemove", itoa(x), itoa(y), "click", repeatFlag, itoa(amount), button}}}
}

func (x11Backend) windows() []cmd { return []cmd{{"wmctrl", []string{"-l"}}} }

func (x11Backend) focus(window string) []cmd { return []cmd{{"wmctrl", []string{"-a", window}}} }

// waylandBackend drives a Wayland session with grim and ydotool. Wayland gives no program the list
// of windows, so those steps are done with the keyboard instead (alt+tab, super).
type waylandBackend struct{}

func (waylandBackend) label() string { return "Linux, Wayland" }

func (waylandBackend) shot(file string) []cmd { return []cmd{{"grim", []string{file}}} }

func (waylandBackend) size() []cmd { return nil }

// ydoButtons are ydotool's codes for a press and release of each button.
var ydoButtons = map[string]string{"left": "0xC0", "right": "0xC1", "middle": "0xC2"}

func (b waylandBackend) click(x, y int, button string, double bool) []cmd {
	args := []string{"click"}
	if double {
		args = append(args, repeatFlag, "2", "--next-delay", "80")
	}
	return append(b.move(x, y), cmd{"ydotool", append(args, ydoButtons[button])})
}

func (waylandBackend) move(x, y int) []cmd {
	return []cmd{{"ydotool", []string{"mousemove", "--absolute", "-x", itoa(x), "-y", itoa(y)}}}
}

func (waylandBackend) write(text string) []cmd {
	return []cmd{{"ydotool", []string{"type", "--", text}}}
}

// evdevKeys are the Linux input codes ydotool presses.
var evdevKeys = map[string]int{
	"ctrl": 29, "shift": 42, "alt": 56, "super": 125,
	"enter": 28, "tab": 15, "esc": 1, "backspace": 14, "delete": 111, "space": 57,
	"up": 103, "down": 108, "left": 105, "right": 106, "home": 102, "end": 107, "pageup": 104, "pagedown": 109,
	"f1": 59, "f2": 60, "f3": 61, "f4": 62, "f5": 63, "f6": 64, "f7": 65, "f8": 66, "f9": 67, "f10": 68,
	"f11": 87, "f12": 88,
	"q": 16, "w": 17, "e": 18, "r": 19, "t": 20, "y": 21, "u": 22, "i": 23, "o": 24, "p": 25,
	"a": 30, "s": 31, "d": 32, "f": 33, "g": 34, "h": 35, "j": 36, "k": 37, "l": 38,
	"z": 44, "x": 45, "c": 46, "v": 47, "b": 48, "n": 49, "m": 50,
	"1": 2, "2": 3, "3": 4, "4": 5, "5": 6, "6": 7, "7": 8, "8": 9, "9": 10, "0": 11,
	"-": 12, "=": 13, "[": 26, "]": 27, ";": 39, "'": 40, "`": 41, "\\": 43, ",": 51, ".": 52, "/": 53,
}

func (waylandBackend) press(k keys) ([]cmd, error) {
	names := append([]string{}, k.mods...)
	if k.key != "" {
		names = append(names, k.key)
	}
	codes := make([]int, 0, len(names))
	for _, n := range names {
		code, ok := evdevKeys[n]
		if !ok {
			return nil, fmt.Errorf("%q cannot be pressed on this desktop: type it with desktop_type", n)
		}
		codes = append(codes, code)
	}
	// Every key goes down in order and comes up in reverse, as a hand presses a combination.
	args := []string{"key"}
	for _, c := range codes {
		args = append(args, itoa(c)+":1")
	}
	for i := len(codes) - 1; i >= 0; i-- {
		args = append(args, itoa(codes[i])+":0")
	}
	return []cmd{{"ydotool", args}}, nil
}

func (b waylandBackend) scroll(x, y, amount int) []cmd {
	// The wheel turns up for a positive value, so a scroll down is negative.
	return append(b.move(x, y), cmd{"ydotool", []string{"mousemove", "--wheel", "-x", "0", "-y", itoa(-amount)}})
}

func (waylandBackend) windows() []cmd { return nil }

func (waylandBackend) focus(string) []cmd { return nil }

// itoa is strconv.Itoa, shorter where commands are built.
func itoa(n int) string { return strconv.Itoa(n) }
