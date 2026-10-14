package policy

import (
	"fmt"
	"strings"
)

// What changes when the line runs under PowerShell, which is the shell on Windows.
//
// The policy reads a line with POSIX rules, and for most of what a line holds PowerShell agrees:
// `;`, `|`, `&`, `>` and newlines divide it the same way, and quotes close in the same places.
// Where the two disagree, the program PowerShell runs is not the program the policy judged, so
// those lines are not classified at all (powerShellSyntax), and the rest are judged with one
// more rule (powerShellVerdict).

// powerShellSyntax reports the first character of line that PowerShell reads as code, or as a
// different word boundary, where the POSIX reading sees a plain word:
//
//   - outside quotes: a script block (`{}`), a subexpression or call (`()`), a variable or
//     splat (`$`, `@`), a type literal (`[`), a backtick escape, a comment (`#`, which also
//     opens `<#` block comments);
//   - in double quotes: `$`, which expands `$(…)` there, and the backtick escape;
//   - a backslash before anything but a path character: POSIX takes `\;` as a literal
//     semicolon and PowerShell as a backslash followed by a separator, and `\"` ends a
//     PowerShell string where POSIX keeps going;
//   - the typographic quotes, which PowerShell honours as quotes and POSIX does not;
//   - a network path (uncPath), quoted or not: reading `\\host\share\f` is a reader on
//     the POSIX reading and a connection to another machine on Windows.
//
// Single-quoted text is literal in both, so nothing in it is reported.
func powerShellSyntax(line string) (rune, bool) {
	runes := []rune(line)
	quote := rune(0)
	for i, r := range runes {
		if strings.ContainsRune("\u2018\u2019\u201a\u201b\u201c\u201d\u201e", r) {
			return r, true
		}
		if uncPath(runes, i) {
			return r, true
		}
		switch quote {
		case '\'':
			if r == '\'' {
				quote = 0
			}
			continue
		case '"':
			switch {
			case r == '"':
				quote = 0
			case r == '$' || r == '`':
				return r, true
			case r == '\\' && i+1 < len(runes) && runes[i+1] == '"':
				return r, true
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
		case strings.ContainsRune("{}()$@[`#", r):
			return r, true
		case r == '\\' && i+1 < len(runes) && !pathRune(runes[i+1]):
			return r, true
		}
	}
	return 0, false
}

// uncPath reports whether a network path starts at runes[i]: `\\` anywhere, or `//` that is not
// the `//` of a URL (`https://`), whose request is judged by the program that makes it.
func uncPath(runes []rune, i int) bool {
	if i+1 >= len(runes) || runes[i] != runes[i+1] {
		return false
	}
	switch runes[i] {
	case '\\':
		return true
	case '/':
		return !urlScheme(runes[:i])
	}
	return false
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
	if k == n-1 || !(('a' <= first && first <= 'z') || ('A' <= first && first <= 'Z')) {
		return false
	}
	return k == 0 || strings.ContainsRune(" \t'\"=,", before[k-1])
}

// schemeRune reports whether r may appear in a URL scheme.
func schemeRune(r rune) bool {
	return r == '+' || r == '.' || r == '-' ||
		('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

// pathRune reports whether r may follow a backslash in a Windows path without POSIX and
// PowerShell disagreeing about the word it belongs to.
func pathRune(r rune) bool {
	return r == '.' || r == '_' || r == '-' || r == '\\' || r == '/' ||
		('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

// powerShellVerdict confirms a silent verdict that rests on a path the POSIX rules misread.
//
// Every Allow but a reader's rests on a judgement about a PATH — a write inside the workspace, a
// script the workspace holds. With its backslashes turned into slashes a relative Windows path is
// judged right, but three forms are not: a path rooted without a drive (`/Windows`, which
// Windows takes from the root of the current drive and the POSIX join places in the workspace),
// a drive or provider prefix (`C:/Users`, the drive-relative `C:x`, `HKLM:/Software`), and `~`.
// A line that names one of them is confirmed; a reader needs no such judgement, since reading is
// allowed wherever the file is.
func powerShellVerdict(d Decision, words []string) Decision {
	if d.Verdict != Allow || d.Rule == "reader" {
		return d
	}
	for _, w := range words {
		if windowsPath(w) {
			return Decision{Ask, fmt.Sprintf("%s; %q is a Windows path whose place the guardrails "+
				"cannot check, so it is confirmed", d.Reason, unquote(w)), d.Rule, false}
		}
	}
	return d
}

// windowsPath reports whether word names a path rooted outside the working directory in a way
// only Windows reads: rooted without a drive, behind a drive or provider prefix, or in a home
// directory. A `-Parameter:value` word is judged by its value.
func windowsPath(word string) bool {
	w := unquote(word)
	if strings.HasPrefix(w, "-") {
		_, value, ok := strings.Cut(w, ":")
		if !ok {
			return false
		}
		w = value
	}
	if strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~") {
		return true
	}
	end := strings.IndexByte(w, ':')
	if end < 1 || !isLetter(rune(w[0])) {
		return false
	}
	for _, r := range w[1:end] {
		if !isLetter(r) && !('0' <= r && r <= '9') && !strings.ContainsRune("_.-", r) {
			return false
		}
	}
	// `fix: x`, a word with a colon and then a space, is prose (a commit message), not a path.
	return end == len(w)-1 || w[end+1] != ' '
}

// isLetter reports whether r is an ASCII letter.
func isLetter(r rune) bool { return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') }
