package desktop

import (
	"fmt"
	"strings"
)

// KEYS, AS THE MODEL WRITES THEM AND AS EACH DESKTOP NAMES THEM.
//
// A combination is written with "+": modifiers first, then one key, like ctrl+shift+t, alt+tab,
// enter or super. The key is a single letter, digit or symbol, or one of namedKeys; the
// modifiers are ctrl, alt, shift and super (cmd, win and meta are the same key).

// keys is a parsed combination.
type keys struct {
	mods []string // ctrl, alt, shift, super, in the order written
	key  string   // one of namedKeys, a single character, or "" for a modifier alone
}

// String is the combination as it was meant.
func (k keys) String() string {
	parts := append([]string{}, k.mods...)
	if k.key != "" {
		parts = append(parts, k.key)
	}
	return strings.Join(parts, "+")
}

// modifierNames maps every spelling of a modifier to its name.
var modifierNames = map[string]string{
	"ctrl": "ctrl", "control": "ctrl", "alt": "alt", "option": "alt", "shift": "shift",
	"super": "super", "cmd": "super", "command": "super", "win": "super", "meta": "super",
}

// namedKeys are the keys with a name, by every spelling the model may use.
var namedKeys = map[string]string{
	"enter": "enter", "return": "enter", "tab": "tab", "esc": "esc", "escape": "esc",
	"backspace": "backspace", "delete": "delete", "del": "delete", "space": "space",
	"up": "up", "down": "down", "left": "left", "right": "right", "home": "home", "end": "end",
	"pageup": "pageup", "pagedown": "pagedown", "pgup": "pageup", "pgdn": "pagedown",
	"f1": "f1", "f2": "f2", "f3": "f3", "f4": "f4", "f5": "f5", "f6": "f6",
	"f7": "f7", "f8": "f8", "f9": "f9", "f10": "f10", "f11": "f11", "f12": "f12",
}

// parseKeys reads a combination.
func parseKeys(combo string) (keys, error) {
	var k keys
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(combo, " ", "")), "+")
	if combo == "+" || strings.HasSuffix(combo, "++") {
		parts = append(parts[:len(parts)-2], "+") // "+" itself, alone or as the key
	}
	for i, p := range parts {
		last := i == len(parts)-1
		mod, isMod := modifierNames[p]
		switch {
		case isMod:
			k.mods = append(k.mods, mod)
		case !last:
			return keys{}, fmt.Errorf("%q: only the last part of a combination is a key, the others are "+
				"ctrl, alt, shift or super", combo)
		case namedKeys[p] != "":
			k.key = namedKeys[p]
		case len([]rune(p)) == 1:
			k.key = p
		default:
			return keys{}, fmt.Errorf("%q is not a key (keys is what to press, like ctrl+c or enter): use a "+
				"single character or one of enter, tab, esc, backspace, delete, space, up, down, left, right, "+
				"home, end, pageup, pagedown, f1-f12", p)
		}
	}
	return k, nil
}
