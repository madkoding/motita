package desktop

import (
	"fmt"
	"strings"
)

// macBackend drives macOS with screencapture, osascript and cliclick. macOS asks the person once to
// let the program that runs motita record the screen and control the computer (Privacy & Security:
// Screen Recording and Accessibility).
type macBackend struct{}

func (macBackend) label() string { return "macOS" }

func (macBackend) shot(file string) []cmd {
	return []cmd{{"screencapture", []string{"-x", "-t", "png", file}}}
}

// size reads the desktop in points, which the pointer moves in: a Retina picture has twice as many
// pixels.
func (macBackend) size() []cmd {
	return []cmd{osascript(`tell application "Finder" to get bounds of window of desktop`)}
}

// cliclickVerbs are cliclick's click of each button; it has none for the middle one.
var cliclickVerbs = map[string]string{"left": "c", "right": "rc"}

func (macBackend) click(x, y int, button string, double bool) []cmd {
	verb, ok := cliclickVerbs[button]
	if !ok {
		return nil
	}
	if double && button == "left" {
		verb = "dc"
	}
	return []cmd{{"cliclick", []string{fmt.Sprintf("%s:%d,%d", verb, x, y)}}}
}

func (macBackend) move(x, y int) []cmd {
	return []cmd{{"cliclick", []string{fmt.Sprintf("m:%d,%d", x, y)}}}
}

func (macBackend) write(text string) []cmd {
	return []cmd{osascript(`tell application "System Events" to keystroke (item 1 of argv)`, text)}
}

// macKeyCodes are the key codes of the named keys.
var macKeyCodes = map[string]string{
	"enter": "36", "tab": "48", "space": "49", "backspace": "51", "esc": "53", "delete": "117",
	"left": "123", "right": "124", "down": "125", "up": "126", "home": "115", "end": "119",
	"pageup": "116", "pagedown": "121", "f1": "122", "f2": "120", "f3": "99", "f4": "118", "f5": "96",
	"f6": "97", "f7": "98", "f8": "100", "f9": "101", "f10": "109", "f11": "103", "f12": "111",
}

// macModifiers are AppleScript's names of the modifiers.
var macModifiers = map[string]string{
	"ctrl": "control down", "alt": "option down", "shift": "shift down", "super": "command down",
}

func (macBackend) press(k keys) ([]cmd, error) {
	if k.key == "" {
		return nil, fmt.Errorf("%s alone cannot be pressed on macOS: press it with a key", k)
	}
	mods := make([]string, 0, len(k.mods))
	for _, m := range k.mods {
		mods = append(mods, macModifiers[m])
	}
	using := ""
	if len(mods) > 0 {
		using = " using {" + strings.Join(mods, ", ") + "}"
	}
	if code, ok := macKeyCodes[k.key]; ok {
		return []cmd{osascript(`tell application "System Events" to key code ` + code + using)}, nil
	}
	return []cmd{osascript(`tell application "System Events" to keystroke (item 1 of argv)`+using, k.key)}, nil
}

// scroll has no program on macOS that ships with it or with cliclick: pageup and pagedown do it.
func (macBackend) scroll(int, int, int) []cmd { return nil }

func (macBackend) windows() []cmd {
	return []cmd{osascript(`tell application "System Events" to get name of every process whose visible is true`)}
}

func (macBackend) focus(window string) []cmd {
	return []cmd{osascript(`tell application (item 1 of argv) to activate`, window)}
}

// osascript runs one line of AppleScript; args reach it as argv, so no text is ever quoted into
// the script.
func osascript(line string, args ...string) cmd {
	return cmd{"osascript", append([]string{"-e", "on run argv", "-e", line, "-e", "end run", "--"}, args...)}
}
