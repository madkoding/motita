package policy

import (
	"fmt"
	"strings"
)

// What changes when the line runs under cmd.exe, the Windows command processor and the shell
// motita falls back to where PowerShell cannot be found.
//
// cmd.exe shares almost nothing with the POSIX reading the policy uses. It expands `%VAR%`
// anywhere, quotes included, and `!VAR!` under delayed expansion; `^` is its escape; a backslash
// is a path separator and never an escape; `,`, `;` and `=` divide words like a space; `"` does
// not protect `&`, `|` or `>` the way POSIX quotes do. A line read with POSIX rules is therefore
// not the line cmd.exe runs: `echo x > %USERPROFILE%\evil.bat` is a write inside the workspace
// to the POSIX eye and a write into the user's home to cmd.exe.
//
// Rather than a second parser that would have to be right about all of it, a cmd.exe line is
// classified only when it is made of characters both readings agree on (cmdPlainRune): words of
// letters, digits, `.`, `_` and `-`, divided by blanks. Anything else is a shell line, approved
// as a whole.

// cmdSyntax reports the first character of line that cmd.exe may read differently from the POSIX
// reading.
func cmdSyntax(line string) (rune, bool) {
	for _, r := range line {
		if !cmdPlainRune(r) {
			return r, true
		}
	}
	return 0, false
}

// cmdPlainRune reports whether r means the same to cmd.exe and to the POSIX reading.
func cmdPlainRune(r rune) bool {
	return isLetter(r) || isDigit(r) || strings.ContainsRune(" \t._-", r)
}

// cmdLine decides, under cmd.exe, a line that holds anything but plain words: it is a shell line.
// Any other shell's line, and a plain cmd.exe line, are left to the segment reading (hit false).
func (m Mode) cmdLine(line string) (Decision, bool) {
	if !m.Cmd {
		return Decision{}, false
	}
	if r, hit := cmdSyntax(line); hit {
		return m.Relaxing(m.ShellLine(fmt.Sprintf("cmd.exe reads %q by rules of its own", string(r)))), true
	}
	return Decision{}, false
}
