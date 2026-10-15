package readonly

import (
	"fmt"
	"runtime"
	"strings"
)

// windowsNetworkPaths is true where a path that names another machine (`\\host\share\f`,
// `//host/share/f`) is opened over the network by whatever program is handed it. On Windows that
// open is an SMB connection that authenticates as the user, so a reader pointed at a host the
// model chose sends the user's NTLM hash to it. Elsewhere `//host` is a plain local path and
// `\\` is not a separator, so a pattern such as `//go:build` is left alone. A variable so the
// tests on every platform can take either side.
var windowsNetworkPaths = runtime.GOOS == "windows"

// NetworkPathAt reports whether a network path starts at runes[i]: `\\` anywhere, or `//` that is
// not the `//` of a URL (`https://`), whose request is judged by the program that makes it.
func NetworkPathAt(runes []rune, i int) bool {
	if i+1 >= len(runes) || runes[i] != runes[i+1] {
		return false
	}
	switch runes[i] {
	case '\\':
		return true
	case '/':
		return !urlScheme(runes[:i])
	default:
		return false
	}
}

// networkPathArg returns the first argument that holds a network path, if any.
func networkPathArg(args []string) (string, bool) {
	for _, a := range args {
		runes := []rune(a)
		for i := range runes {
			if NetworkPathAt(runes, i) {
				return a, true
			}
		}
	}
	return "", false
}

// networkPathRefusal refuses, where a network path reaches another machine, a command that names
// one: reading it is a connection that hands the user's credentials to that machine.
func networkPathRefusal(args []string) (Decision, bool) {
	if !windowsNetworkPaths {
		return Decision{}, false
	}
	if a, hit := networkPathArg(args); hit {
		return Decision{false, fmt.Sprintf("%q names a network path: opening it connects to another "+
			"machine and signs in as the user, so read-only mode refuses it", a)}, true
	}
	return Decision{}, false
}

// urlScheme reports whether before ends in `scheme:`, the scheme starting a word with a letter.
func urlScheme(before []rune) bool {
	n := len(before)
	if n < 2 || before[n-1] != ':' {
		return false
	}
	k := n - 1
	for k > 0 && schemeRune(before[k-1]) {
		k--
	}
	first := before[k]
	if k == n-1 || !asciiLetter(first) {
		return false
	}
	return k == 0 || strings.ContainsRune(" \t'\"=,", before[k-1])
}

// schemeRune reports whether r may appear in a URL scheme.
func schemeRune(r rune) bool {
	return strings.ContainsRune("+.-", r) || asciiLetter(r) || ('0' <= r && r <= '9')
}

// asciiLetter reports whether r is an ASCII letter.
func asciiLetter(r rune) bool { return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') }
