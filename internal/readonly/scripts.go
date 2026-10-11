package readonly

// The PROGRAM TEXT of sed and awk, read for the commands that write a file or run one.
//
// A rule about flags is not enough for these two: `sed 'w out' f`, `sed -n 'e id' f` and
// `awk 'BEGIN{"id" | getline}'` write or run something with no flag at all, and a substring
// match on the text is both too narrow (`system ("id")` has a space) and too wide (`print
// "system"` is a string). So the text is read the way the program reads it — far enough to
// find the commands and statements, and no further. What these readers cannot follow is
// refused, which is the safe direction for a mode whose promise is that nothing changes.

import (
	"fmt"
	"strings"
)

// unquote removes ONE layer of matching quotes around a program. The line splitter that feeds
// plan mode has already removed them, but other callers split on spaces and hand the quotes
// through (`'1,300p'`), and a reader that took the quote for a command would refuse every one.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// resolveLong resolves a long option the way getopt_long does: an exact name, or a prefix of
// exactly one known name. It returns "" for an unknown or ambiguous one.
func resolveLong(name string, known []string) string {
	match := ""
	for _, k := range known {
		if k == name {
			return k
		}
		if len(name) > 2 && strings.HasPrefix(k, name) {
			if match != "" {
				return ""
			}
			match = k
		}
	}
	return match
}

// --- sed ---------------------------------------------------------------------

var sedLongOptions = []string{
	"--quiet", "--silent", "--regexp-extended", "--separate", "--unbuffered", "--null-data",
	"--zero-terminated", "--posix", "--debug", "--sandbox", "--binary", "--follow-symlinks",
	"--help", "--version", "--line-length", "--expression", "--file", "--in-place",
}

// sedRule refuses `-i`, a script this policy cannot see (`-f`), an option it does not know,
// and a script with a command that writes, reads another file or runs one. The options are an
// ALLOWLIST: GNU sed accepts abbreviations and clusters, and a list of the bad ones misses
// `--in-pl` and `-ni`.
func sedRule(args []string) (string, bool) {
	var scripts, operands []string
	explicit := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, value, glued := strings.Cut(a, "=")
			switch resolveLong(name, sedLongOptions) {
			case "--in-place":
				return "sed --in-place rewrites the files", true
			case "--file":
				return fmt.Sprintf("sed %s reads its script from a file this policy cannot see", a), true
			case "--expression":
				if !glued && i+1 < len(args) {
					i++
					value = args[i]
				}
				scripts, explicit = append(scripts, value), true
			case "--line-length":
				if !glued {
					i++
				}
			case "":
				return fmt.Sprintf("sed %s is not an option read-only mode knows", a), true
			}
		case strings.HasPrefix(a, "-") && a != "-":
			for j := 1; j < len(a); j++ {
				switch a[j] {
				case 'n', 'r', 'E', 's', 'u', 'z', 'b':
				case 'i':
					// `-i`, `-i.bak`, and `-ni` (a cluster, which GNU sed really accepts).
					return fmt.Sprintf("sed %s rewrites the files", a), true
				case 'f':
					return fmt.Sprintf("sed %s reads its script from a file this policy cannot see", a), true
				case 'e', 'l':
					value := a[j+1:]
					if value == "" && i+1 < len(args) {
						i++
						value = args[i]
					}
					if a[j] == 'e' {
						scripts, explicit = append(scripts, value), true
					}
					j = len(a)
				default:
					return fmt.Sprintf("sed %s is not an option read-only mode knows", a), true
				}
			}
		default:
			operands = append(operands, a)
		}
	}
	if !explicit && len(operands) > 0 {
		scripts = append(scripts, operands[0])
	}
	for _, s := range scripts {
		if reason, bad := sedScript(unquote(s)); bad {
			return reason, true
		}
	}
	return "", false
}

// sedScript reads a sed script command by command and refuses the ones that touch anything
// but the output: `w`/`W` write a file, `r`/`R` read one into the output, `e` runs a command,
// and the `s` command's `w` and `e` flags do the same. A script it cannot read is refused.
func sedScript(s string) (string, bool) {
	unreadable := func(at int) (string, bool) {
		return fmt.Sprintf("the sed script %q cannot be read past position %d, so what it does "+
			"cannot be checked", s, at), true
	}
	i := 0
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', ';', '}':
			i++
			continue
		case '#':
			i = skipLine(s, i)
			continue
		}
		// One or two addresses, then any number of `!`.
		var ok bool
		if i, ok = sedAddress(s, i); !ok {
			return unreadable(i)
		}
		i = skipBlanks(s, i)
		if i < len(s) && s[i] == ',' {
			if i, ok = sedAddress(s, skipBlanks(s, i+1)); !ok {
				return unreadable(i)
			}
		}
		for i = skipBlanks(s, i); i < len(s) && s[i] == '!'; i = skipBlanks(s, i+1) {
		}
		if i >= len(s) {
			return unreadable(i)
		}
		cmd := s[i]
		i++
		switch cmd {
		case '{', '=', 'd', 'D', 'g', 'G', 'h', 'H', 'n', 'N', 'p', 'P', 'x', 'z', 'F':
		case 'q', 'Q', 'l', 'L':
			for i = skipBlanks(s, i); i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			}
		case ':', 'b', 't', 'T', 'v':
			// A label, up to the end of the command.
			for i < len(s) && s[i] != ';' && s[i] != '\n' {
				i++
			}
		case 'a', 'i', 'c':
			// The text runs to the end of the line; a backslash continues it on the next.
			for i < len(s) && s[i] != '\n' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		case 'y':
			if i >= len(s) {
				return unreadable(i)
			}
			// y's two parts are character lists, not regular expressions: no brackets.
			if i, ok = sedDelimited(s, i+1, s[i], 2, false); !ok {
				return unreadable(i)
			}
		case 's':
			if i >= len(s) {
				return unreadable(i)
			}
			if i, ok = sedDelimited(s, i+1, s[i], 2, true); !ok {
				return unreadable(i)
			}
			// The flags: g, p, a number, i/I, m/M. `w FILE` writes and `e` runs the pattern
			// space as a command, so neither is in the list.
			for ; i < len(s) && strings.IndexByte(" \t\n;}", s[i]) < 0; i++ {
				switch c := s[i]; {
				case strings.IndexByte("gpiImM0123456789", c) >= 0:
				case c == 'w' || c == 'e':
					return fmt.Sprintf("the sed s command's %c flag %s", c, sedEffect(c)), true
				default:
					return unreadable(i)
				}
			}
		case 'w', 'W', 'r', 'R', 'e':
			return fmt.Sprintf("the sed %c command %s", cmd, sedEffect(cmd)), true
		default:
			return unreadable(i - 1)
		}
	}
	return "", false
}

// sedEffect says what a refused sed command does, for the refusal.
func sedEffect(c byte) string {
	switch c {
	case 'w', 'W':
		return "writes a file"
	case 'e':
		return "runs a command"
	}
	return "reads another file into the output"
}

// sedAddress reads one address, if there is one: a line number (with GNU's `~step` and `+N`),
// `$`, or a regular expression in `/.../` or `\c...c`, with its I and M flags.
func sedAddress(s string, i int) (int, bool) {
	if i >= len(s) {
		return i, true
	}
	switch c := s[i]; {
	case c == '$':
		return i + 1, true
	case c >= '0' && c <= '9', c == '+', c == '~':
		for i++; i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '~'); i++ {
		}
		return i, true
	case c == '/' || c == '\\':
		if c == '\\' {
			i++
			if i >= len(s) {
				return i, false
			}
		}
		j, ok := sedDelimited(s, i+1, s[i], 1, true)
		for ok && j < len(s) && (s[j] == 'I' || s[j] == 'M') {
			j++
		}
		return j, ok
	}
	return i, true
}

// sedDelimited skips n delimited parts that start at i (the delimiter itself already read),
// honouring backslash escapes and, when the first part is a regular expression, bracket
// expressions in which the delimiter is an ordinary character, as GNU sed reads them. An older
// sed that ends the expression inside the bracket sees an unclosed one and runs nothing. It
// returns the index after the last delimiter.
func sedDelimited(s string, i int, delim byte, n int, regex bool) (int, bool) {
	for part := 0; part < n; part++ {
		for {
			if i >= len(s) || s[i] == '\n' {
				return i, false
			}
			switch {
			case s[i] == '\\':
				i += 2
				continue
			case s[i] == delim:
			case s[i] == '[' && regex && part == 0 && delim != '[':
				end, ok := bracketEnd(s, i)
				if !ok {
					return i, false
				}
				i = end
				continue
			default:
				i++
				continue
			}
			break
		}
		i++
	}
	return i, true
}

// bracketEnd returns the index after the bracket expression that opens at i, with the POSIX
// rules: a `]` first (after an optional `^`) is literal, and `[:class:]`, `[=e=]` and `[.c.]`
// are inner parts whose `]` does not close it.
func bracketEnd(s string, i int) (int, bool) {
	j := i + 1
	if j < len(s) && s[j] == '^' {
		j++
	}
	if j < len(s) && s[j] == ']' {
		j++
	}
	for j < len(s) {
		switch {
		case s[j] == ']':
			return j + 1, true
		case s[j] == '[' && j+1 < len(s) && strings.IndexByte(":=.", s[j+1]) >= 0:
			end := strings.Index(s[j+2:], string(s[j+1])+"]")
			if end < 0 {
				return j, false
			}
			j += 2 + end + 2
		default:
			j++
		}
	}
	return j, false
}

func skipBlanks(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

func skipLine(s string, i int) int {
	for i < len(s) && s[i] != '\n' {
		i++
	}
	return i
}

// --- awk ---------------------------------------------------------------------

var awkLongOptions = []string{
	"--field-separator", "--assign", "--source", "--posix", "--traditional", "--re-interval",
	"--characters-as-bytes", "--sandbox", "--use-lc-numeric", "--help", "--version",
	"--file", "--include", "--load", "--exec",
}

// awkRule is the rule for `awk`, `gawk` and `mawk` — one program under three names.
//
// Its options are an allowlist (`-f`, `-i`, `-l`, `-E` and mawk's `-W` load or run something
// this policy cannot see), and its program is read by awkProgramWrites.
func awkRule(args []string) (string, bool) {
	var programs, operands []string
	explicit := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, value, glued := strings.Cut(a, "=")
			switch resolveLong(name, awkLongOptions) {
			case "--field-separator", "--assign":
				if !glued {
					i++
				}
			case "--source":
				if !glued && i+1 < len(args) {
					i++
					value = args[i]
				}
				programs, explicit = append(programs, value), true
			case "--posix", "--traditional", "--re-interval", "--characters-as-bytes",
				"--sandbox", "--use-lc-numeric", "--help", "--version":
			default:
				return fmt.Sprintf("awk %s loads a program or an option read-only mode cannot check", a), true
			}
		case strings.HasPrefix(a, "-") && a != "-":
			rest := a[2:]
			switch a[1] {
			case 'F', 'v':
				if rest == "" {
					i++
				}
			case 'e':
				if rest == "" && i+1 < len(args) {
					i++
					rest = args[i]
				}
				programs, explicit = append(programs, rest), true
			case 'i':
				if rest == "inplace" || (rest == "" && i+1 < len(args) && args[i+1] == "inplace") {
					return "awk -i inplace rewrites the files", true
				}
				return fmt.Sprintf("awk %s loads a program read-only mode cannot see", a), true
			default:
				if strings.Trim(a[1:], "Pbcr") != "" {
					return fmt.Sprintf("awk %s loads a program or an option read-only mode cannot check", a), true
				}
			}
		default:
			operands = append(operands, a)
		}
	}
	if !explicit && len(operands) > 0 {
		programs = append(programs, operands[0])
	}
	for _, p := range programs {
		if reason, bad := awkProgramWrites(unquote(p)); bad {
			return reason, true
		}
	}
	return "", false
}

// awkProgramWrites reads an awk program for what reaches outside its output: `system()`,
// `getline` (which reads a file or a command), a pipe (`print | "cmd"`, `"cmd" | getline`),
// an output redirection, and gawk's `@` (`@load`, `@include`, and the indirect call `@f()`).
//
// Strings, regular expressions and comments are skipped, so a `>` or a `|` inside one is not
// a redirection. A `>` is a redirection only inside a print statement and outside its
// parentheses — which is the grammar, and is what separates `print > out` from `$1 > 5`.
func awkProgramWrites(p string) (string, bool) {
	var (
		inPrint   bool // inside a print or printf statement
		depth     int  // parentheses opened inside that statement
		operand   bool // the last token ends an operand, so a `/` divides rather than opening a regex
		continues bool
	)
	for i := 0; i < len(p); {
		c := p[i]
		switch {
		case c == ' ' || c == '\t':
			i++
			continue
		case c == '\\':
			i += 2
			continue
		case c == '\n' && continues:
			i++
			continue
		case c == '"':
			end, ok := awkStringEnd(p, i)
			if !ok {
				return fmt.Sprintf("the awk program has an unterminated string at %d, so it cannot be checked", i), true
			}
			i, operand = end, true
		case c == '/' && !operand:
			end, ok := awkRegexEnd(p, i)
			if !ok {
				return fmt.Sprintf("the awk program has an unterminated regular expression at %d, so it cannot be checked", i), true
			}
			i, operand = end, true
		case c == '#':
			i = skipLine(p, i)
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(p) && (p[j] == '_' || p[j] >= 'a' && p[j] <= 'z' || p[j] >= 'A' && p[j] <= 'Z' || p[j] >= '0' && p[j] <= '9') {
				j++
			}
			switch word := p[i:j]; word {
			case "system":
				return "awk with system() runs an arbitrary command", true
			case "getline":
				return "awk getline reads a file or the output of a command", true
			case "print", "printf":
				inPrint, depth = true, 0
			}
			i, operand = j, true
		case c >= '0' && c <= '9' || c == '.':
			for i++; i < len(p) && (p[i] >= '0' && p[i] <= '9' || p[i] == '.' || p[i] == 'e' || p[i] == 'E'); i++ {
			}
			operand = true
		case c == '@':
			return "awk @ directives and indirect calls load or run code read-only mode cannot see", true
		case c == '|':
			if i+1 < len(p) && p[i+1] == '|' {
				i, operand = i+2, false
				break
			}
			return "awk writes a file (a pipe in its program)", true
		case c == '>':
			if inPrint && depth == 0 {
				return "awk writes a file (a redirection in its program)", true
			}
			i, operand = i+1, false
		case c == '(' || c == '[':
			if inPrint {
				depth++
			}
			i, operand = i+1, false
		case c == ')' || c == ']':
			if inPrint && depth > 0 {
				depth--
			}
			i, operand = i+1, true
		case c == '+' || c == '-':
			// `x++ / 2` divides: an increment ends an operand.
			if i+1 < len(p) && p[i+1] == c {
				i, operand = i+2, true
				break
			}
			i, operand = i+1, false
		case c == ';' || c == '\n' || c == '{' || c == '}':
			inPrint = false
			i, operand = i+1, false
		default:
			i, operand = i+1, false
		}
		// A newline after a comma, `&&` or `||` continues the statement, and so a print.
		continues = c == ',' || c == '&' || c == '|'
	}
	return "", false
}

// awkStringEnd returns the index after the string literal that opens at i.
func awkStringEnd(p string, i int) (int, bool) {
	for j := i + 1; j < len(p); j++ {
		switch p[j] {
		case '\\':
			j++
		case '"':
			return j + 1, true
		}
	}
	return len(p), false
}

// awkRegexEnd returns the index after the regular expression literal that opens at i. A `/`
// inside a bracket expression does not close it, as in gawk; an awk that disagrees sees an
// unclosed bracket and refuses to compile the program, so nothing runs either way.
func awkRegexEnd(p string, i int) (int, bool) {
	for j := i + 1; j < len(p); {
		switch p[j] {
		case '\\':
			j += 2
		case '/':
			return j + 1, true
		case '\n':
			return j, false
		case '[':
			end, ok := bracketEnd(p, j)
			if !ok {
				return j, false
			}
			j = end
		default:
			j++
		}
	}
	return len(p), false
}
