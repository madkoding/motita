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

// Literals used more than once in this file.
const (
	flagFile    = "--file"
	flagPosix   = "--posix"
	flagSandbox = "--sandbox"
	flagHelp    = "--help"
	flagVersion = "--version"
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
	"--zero-terminated", flagPosix, "--debug", flagSandbox, "--binary", "--follow-symlinks",
	flagHelp, flagVersion, "--line-length", "--expression", flagFile, "--in-place",
}

// sedRule refuses `-i`, a script this policy cannot see (`-f`), an option it does not know,
// and a script with a command that writes, reads another file or runs one. The options are an
// ALLOWLIST: GNU sed accepts abbreviations and clusters, and a list of the bad ones misses
// `--in-pl` and `-ni`.
func sedRule(args []string) (string, bool) {
	o := &optionScan{args: args}
	for ; o.i < len(args); o.i++ {
		if reason, bad := o.sedArgument(); bad {
			return reason, true
		}
	}
	for _, s := range o.programs() {
		if reason, bad := sedScript(unquote(s)); bad {
			return reason, true
		}
	}
	return "", false
}

// optionScan walks the arguments of sed or awk, collecting the programs given with an option
// and the operands, the first of which is the program when no option gave one.
type optionScan struct {
	args     []string
	i        int // the argument being read
	scripts  []string
	operands []string
	explicit bool // a program came with an option
}

// endOfOptions takes every argument after `--` as an operand.
func (o *optionScan) endOfOptions() {
	o.operands = append(o.operands, o.args[o.i+1:]...)
	o.i = len(o.args)
}

// addProgram records a program given with an option.
func (o *optionScan) addProgram(p string) {
	o.scripts, o.explicit = append(o.scripts, p), true
}

// shortValue is an option's value: the rest of its argument, or the next argument (consumed)
// when the rest is empty.
func (o *optionScan) shortValue(rest string) string {
	if rest == "" && o.i+1 < len(o.args) {
		o.i++
		return o.args[o.i]
	}
	return rest
}

// longValue is a long option's value: the one glued with `=`, or the next argument (consumed).
func (o *optionScan) longValue(value string, glued bool) string {
	if glued {
		return value
	}
	return o.shortValue("")
}

// programs are the programs to read: the ones given with an option, or else the first operand.
func (o *optionScan) programs() []string {
	if !o.explicit && len(o.operands) > 0 {
		return append(o.scripts, o.operands[0])
	}
	return o.scripts
}

// isShortOption reports whether a is a short option or a cluster of them (`-` alone is stdin).
func isShortOption(a string) bool {
	return strings.HasPrefix(a, "-") && a != "-"
}

// sedArgument reads the argument at o.i.
func (o *optionScan) sedArgument() (string, bool) {
	a := o.args[o.i]
	switch {
	case a == "--":
		o.endOfOptions()
	case strings.HasPrefix(a, "--"):
		return o.sedLong(a)
	case isShortOption(a):
		return o.sedShort(a)
	default:
		o.operands = append(o.operands, a)
	}
	return "", false
}

// sedLong reads a long sed option.
func (o *optionScan) sedLong(a string) (string, bool) {
	name, value, glued := strings.Cut(a, "=")
	switch resolveLong(name, sedLongOptions) {
	case "--in-place":
		return "sed --in-place rewrites the files", true
	case flagFile:
		return fmt.Sprintf("sed %s reads its script from a file this policy cannot see", a), true
	case "--expression":
		o.addProgram(o.longValue(value, glued))
	case "--line-length":
		if !glued {
			o.i++
		}
	case "":
		return fmt.Sprintf("sed %s is not an option read-only mode knows", a), true
	default:
		// Every other known option changes only how the input is read or the output written.
	}
	return "", false
}

// sedShort reads a short sed option or a cluster of them.
func (o *optionScan) sedShort(a string) (string, bool) {
	for j := 1; j < len(a); j++ {
		switch a[j] {
		case 'n', 'r', 'E', 's', 'u', 'z', 'b':
			// Flags that change only how the input is read or the output written.
		case 'i':
			// `-i`, `-i.bak`, and `-ni` (a cluster, which GNU sed really accepts).
			return fmt.Sprintf("sed %s rewrites the files", a), true
		case 'f':
			return fmt.Sprintf("sed %s reads its script from a file this policy cannot see", a), true
		case 'e', 'l':
			// The rest of the cluster, or the next argument, is the option's value.
			if value := o.shortValue(a[j+1:]); a[j] == 'e' {
				o.addProgram(value)
			}
			return "", false
		default:
			return fmt.Sprintf("sed %s is not an option read-only mode knows", a), true
		}
	}
	return "", false
}

// sedScript reads a sed script command by command and refuses the ones that touch anything
// but the output: `w`/`W` write a file, `r`/`R` read one into the output, `e` runs a command,
// and the `s` command's `w` and `e` flags do the same. A script it cannot read is refused.
func sedScript(s string) (string, bool) {
	r := &sedReader{s: s}
	for r.i < len(s) {
		if reason, bad := r.command(); bad {
			return reason, true
		}
	}
	return "", false
}

// sedReader reads a sed script; i is the position being read.
type sedReader struct {
	s string
	i int
}

// unreadable is the refusal of a script that cannot be read past at.
func (r *sedReader) unreadable(at int) (string, bool) {
	return fmt.Sprintf("the sed script %q cannot be read past position %d, so what it does "+
		"cannot be checked", r.s, at), true
}

// command reads one command, with its addresses, or a separator or comment between commands.
func (r *sedReader) command() (string, bool) {
	switch r.s[r.i] {
	case ' ', '\t', '\n', ';', '}':
		r.i++
		return "", false
	case '#':
		r.i = skipLine(r.s, r.i)
		return "", false
	default:
		// The start of a command: its addresses come first.
	}
	if !r.addresses() || r.i >= len(r.s) {
		return r.unreadable(r.i)
	}
	cmd := r.s[r.i]
	r.i++
	return r.commandBody(cmd)
}

// addresses reads one or two addresses, then any number of `!`.
func (r *sedReader) addresses() bool {
	var ok bool
	if r.i, ok = sedAddress(r.s, r.i); !ok {
		return false
	}
	r.i = skipBlanks(r.s, r.i)
	if r.i < len(r.s) && r.s[r.i] == ',' {
		if r.i, ok = sedAddress(r.s, skipBlanks(r.s, r.i+1)); !ok {
			return false
		}
	}
	for r.i = skipBlanks(r.s, r.i); r.i < len(r.s) && r.s[r.i] == '!'; r.i = skipBlanks(r.s, r.i+1) {
		// Skip any '!' negations before the command.
	}
	return true
}

// commandBody reads what follows the command letter cmd.
func (r *sedReader) commandBody(cmd byte) (string, bool) {
	switch cmd {
	case '{', '=', 'd', 'D', 'g', 'G', 'h', 'H', 'n', 'N', 'p', 'P', 'x', 'z', 'F':
		// No argument.
	case 'q', 'Q', 'l', 'L':
		for r.i = skipBlanks(r.s, r.i); r.i < len(r.s) && isDigit(r.s[r.i]); r.i++ {
			// Skip the optional exit code.
		}
	case ':', 'b', 't', 'T', 'v':
		// A label, up to the end of the command.
		for r.i < len(r.s) && r.s[r.i] != ';' && r.s[r.i] != '\n' {
			r.i++
		}
	case 'a', 'i', 'c':
		r.skipText()
	case 'y':
		// y's two parts are character lists, not regular expressions: no brackets.
		return r.delimited(false)
	case 's':
		return r.substitute()
	case 'w', 'W', 'r', 'R', 'e':
		return fmt.Sprintf("the sed %c command %s", cmd, sedEffect(cmd)), true
	default:
		return r.unreadable(r.i - 1)
	}
	return "", false
}

// skipText skips the text of `a`, `i` and `c`, which runs to the end of the line; a backslash
// continues it on the next.
func (r *sedReader) skipText() {
	for r.i < len(r.s) && r.s[r.i] != '\n' {
		if r.s[r.i] == '\\' {
			r.i++
		}
		r.i++
	}
}

// delimited reads the two delimited parts of `y` or `s`, the delimiter being the character at
// r.i; regex says the first part is a regular expression.
func (r *sedReader) delimited(regex bool) (string, bool) {
	if r.i >= len(r.s) {
		return r.unreadable(r.i)
	}
	var ok bool
	if r.i, ok = sedDelimited(r.s, r.i+1, r.s[r.i], 2, regex); !ok {
		return r.unreadable(r.i)
	}
	return "", false
}

// substitute reads the `s` command: its two parts, then its flags. The flags: g, p, a number,
// i/I, m/M. `w FILE` writes and `e` runs the pattern space as a command, so neither is in the
// list.
func (r *sedReader) substitute() (string, bool) {
	if reason, bad := r.delimited(true); bad {
		return reason, true
	}
	for ; r.i < len(r.s) && strings.IndexByte(" \t\n;}", r.s[r.i]) < 0; r.i++ {
		if reason, bad := r.substituteFlag(r.s[r.i]); bad {
			return reason, true
		}
	}
	return "", false
}

// substituteFlag checks one flag of the `s` command.
func (r *sedReader) substituteFlag(c byte) (string, bool) {
	switch {
	case strings.IndexByte("gpiImM0123456789", c) >= 0:
		return "", false
	case c == 'w' || c == 'e':
		return fmt.Sprintf("the sed s command's %c flag %s", c, sedEffect(c)), true
	default:
		return r.unreadable(r.i)
	}
}

// sedEffect says what a refused sed command does, for the refusal.
func sedEffect(c byte) string {
	switch c {
	case 'w', 'W':
		return "writes a file"
	case 'e':
		return "runs a command"
	default:
		return "reads another file into the output"
	}
}

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
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
	case isDigit(c), c == '+', c == '~':
		for i++; i < len(s) && (isDigit(s[i]) || s[i] == '~'); i++ {
			// Skip the rest of the line number or step.
		}
		return i, true
	case c == '/' || c == '\\':
		return sedRegexAddress(s, i)
	default:
		return i, true
	}
}

// sedRegexAddress reads a regular expression address, `/.../` or `\c...c`, that opens at i,
// with its I and M flags.
func sedRegexAddress(s string, i int) (int, bool) {
	if s[i] == '\\' {
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

// sedDelimited skips n delimited parts that start at i (the delimiter itself already read),
// honouring backslash escapes and, when the first part is a regular expression, bracket
// expressions in which the delimiter is an ordinary character, as GNU sed reads them. An older
// sed that ends the expression inside the bracket sees an unclosed one and runs nothing. It
// returns the index after the last delimiter.
func sedDelimited(s string, i int, delim byte, n int, regex bool) (int, bool) {
	for part := 0; part < n; part++ {
		var ok bool
		if i, ok = sedPartEnd(s, i, delim, regex && part == 0 && delim != '['); !ok {
			return i, false
		}
	}
	return i, true
}

// sedPartEnd returns the index after the delimiter that ends the part starting at i; brackets
// says a bracket expression is read whole, the delimiter inside it an ordinary character.
func sedPartEnd(s string, i int, delim byte, brackets bool) (int, bool) {
	for i < len(s) && s[i] != '\n' {
		switch {
		case s[i] == '\\':
			i += 2
		case s[i] == delim:
			return i + 1, true
		case s[i] == '[' && brackets:
			end, ok := bracketEnd(s, i)
			if !ok {
				return i, false
			}
			i = end
		default:
			i++
		}
	}
	return i, false
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
	"--field-separator", "--assign", "--source", flagPosix, "--traditional", "--re-interval",
	"--characters-as-bytes", flagSandbox, "--use-lc-numeric", flagHelp, flagVersion,
	flagFile, "--include", "--load", "--exec",
}

// awkRule is the rule for `awk`, `gawk` and `mawk` — one program under three names.
//
// Its options are an allowlist (`-f`, `-i`, `-l`, `-E` and mawk's `-W` load or run something
// this policy cannot see), and its program is read by awkProgramWrites.
func awkRule(args []string) (string, bool) {
	o := &optionScan{args: args}
	for ; o.i < len(args); o.i++ {
		if reason, bad := o.awkArgument(); bad {
			return reason, true
		}
	}
	for _, p := range o.programs() {
		if reason, bad := awkProgramWrites(unquote(p)); bad {
			return reason, true
		}
	}
	return "", false
}

// awkArgument reads the argument at o.i.
func (o *optionScan) awkArgument() (string, bool) {
	a := o.args[o.i]
	switch {
	case a == "--":
		o.endOfOptions()
	case strings.HasPrefix(a, "--"):
		return o.awkLong(a)
	case isShortOption(a):
		return o.awkShort(a)
	default:
		o.operands = append(o.operands, a)
	}
	return "", false
}

// awkLong reads a long awk option.
func (o *optionScan) awkLong(a string) (string, bool) {
	name, value, glued := strings.Cut(a, "=")
	switch resolveLong(name, awkLongOptions) {
	case "--field-separator", "--assign":
		if !glued {
			o.i++
		}
	case "--source":
		o.addProgram(o.longValue(value, glued))
	case flagPosix, "--traditional", "--re-interval", "--characters-as-bytes",
		flagSandbox, "--use-lc-numeric", flagHelp, flagVersion:
		// Options that change only how the program is read or run.
	default:
		return fmt.Sprintf("awk %s loads a program or an option read-only mode cannot check", a), true
	}
	return "", false
}

// awkShort reads a short awk option.
func (o *optionScan) awkShort(a string) (string, bool) {
	rest := a[2:]
	switch a[1] {
	case 'F', 'v':
		if rest == "" {
			o.i++
		}
	case 'e':
		o.addProgram(o.shortValue(rest))
	case 'i':
		if o.valueIs(rest, "inplace") {
			return "awk -i inplace rewrites the files", true
		}
		return fmt.Sprintf("awk %s loads a program read-only mode cannot see", a), true
	default:
		if strings.Trim(a[1:], "Pbcr") != "" {
			return fmt.Sprintf("awk %s loads a program or an option read-only mode cannot check", a), true
		}
	}
	return "", false
}

// valueIs reports whether a short option's value is want: the rest of its argument or, when
// that is empty, the next argument (not consumed).
func (o *optionScan) valueIs(rest, want string) bool {
	if rest == "" {
		return o.i+1 < len(o.args) && o.args[o.i+1] == want
	}
	return rest == want
}

// awkProgramWrites reads an awk program for what reaches outside its output: `system()`,
// `getline` (which reads a file or a command), a pipe (`print | "cmd"`, `"cmd" | getline`),
// an output redirection, and gawk's `@` (`@load`, `@include`, and the indirect call `@f()`).
//
// Strings, regular expressions and comments are skipped, so a `>` or a `|` inside one is not
// a redirection. A `>` is a redirection only inside a print statement and outside its
// parentheses — which is the grammar, and is what separates `print > out` from `$1 > 5`.
func awkProgramWrites(p string) (string, bool) {
	r := &awkReader{p: p}
	for r.i < len(p) {
		c := p[r.i]
		if r.skip(c) {
			continue
		}
		if reason, bad := r.token(c); bad {
			return reason, true
		}
		// A newline after a comma, `&&` or `||` continues the statement, and so a print.
		r.continues = c == ',' || c == '&' || c == '|'
	}
	return "", false
}

// awkReader reads an awk program; i is the position being read.
type awkReader struct {
	p         string
	i         int
	inPrint   bool // inside a print or printf statement
	depth     int  // parentheses opened inside that statement
	operand   bool // the last token ends an operand, so a `/` divides rather than opening a regex
	continues bool // the last token continues the statement on the next line
}

// skip steps over a blank, an escaped character or a continued line, and reports whether it did.
func (r *awkReader) skip(c byte) bool {
	switch {
	case c == ' ' || c == '\t' || c == '\n' && r.continues:
		r.i++
	case c == '\\':
		r.i += 2
	default:
		return false
	}
	return true
}

// advance moves past n bytes; operand says whether they end an operand.
func (r *awkReader) advance(n int, operand bool) {
	r.i, r.operand = r.i+n, operand
}

// next reports whether the byte after the current one is c.
func (r *awkReader) next(c byte) bool {
	return r.i+1 < len(r.p) && r.p[r.i+1] == c
}

// token reads the token that starts with c.
func (r *awkReader) token(c byte) (string, bool) {
	switch {
	case c == '"':
		return r.literal(awkStringEnd, "string")
	case c == '/' && !r.operand:
		return r.literal(awkRegexEnd, "regular expression")
	case c == '#':
		r.i = skipLine(r.p, r.i)
	case isAwkNameStart(c):
		return r.name()
	case isDigit(c) || c == '.':
		for r.i++; r.i < len(r.p) && isAwkNumberByte(r.p[r.i]); r.i++ {
			// Skip the rest of the number.
		}
		r.operand = true
	default:
		return r.punctuation(c)
	}
	return "", false
}

// literal skips a string or a regular expression literal, which end finds the end of.
func (r *awkReader) literal(end func(string, int) (int, bool), what string) (string, bool) {
	after, ok := end(r.p, r.i)
	if !ok {
		return fmt.Sprintf("the awk program has an unterminated %s at %d, so it cannot be checked", what, r.i), true
	}
	r.i, r.operand = after, true
	return "", false
}

// name reads a name: system and getline are refused, and print and printf open a statement in
// which a `>` redirects.
func (r *awkReader) name() (string, bool) {
	j := r.i
	for j < len(r.p) && isAwkNameByte(r.p[j]) {
		j++
	}
	switch r.p[r.i:j] {
	case "system":
		return "awk with system() runs an arbitrary command", true
	case "getline":
		return "awk getline reads a file or the output of a command", true
	case "print", "printf":
		r.inPrint, r.depth = true, 0
	default:
		// Any other name is an operand.
	}
	r.i, r.operand = j, true
	return "", false
}

// punctuation reads an operator or a separator.
func (r *awkReader) punctuation(c byte) (string, bool) {
	switch c {
	case '@':
		return "awk @ directives and indirect calls load or run code read-only mode cannot see", true
	case '|':
		if !r.next('|') {
			return "awk writes a file (a pipe in its program)", true
		}
		r.advance(2, false)
	case '>':
		if r.inPrint && r.depth == 0 {
			return "awk writes a file (a redirection in its program)", true
		}
		r.advance(1, false)
	case '(', '[', ')', ']':
		r.bracket(c)
	case '+', '-':
		// `x++ / 2` divides: an increment ends an operand.
		if r.next(c) {
			r.advance(2, true)
		} else {
			r.advance(1, false)
		}
	case ';', '\n', '{', '}':
		r.inPrint = false
		r.advance(1, false)
	default:
		r.advance(1, false)
	}
	return "", false
}

// bracket counts the parentheses and brackets opened inside a print statement.
func (r *awkReader) bracket(c byte) {
	if c == '(' || c == '[' {
		if r.inPrint {
			r.depth++
		}
		r.advance(1, false)
		return
	}
	if r.inPrint && r.depth > 0 {
		r.depth--
	}
	r.advance(1, true)
}

// isAwkNameStart reports whether c can start a name.
func isAwkNameStart(c byte) bool {
	return c == '_' || isLetter(c)
}

// isLetter reports whether c is an ASCII letter.
func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// isAwkNameByte reports whether c can continue a name.
func isAwkNameByte(c byte) bool {
	return isAwkNameStart(c) || isDigit(c)
}

// isAwkNumberByte reports whether c can continue a number.
func isAwkNumberByte(c byte) bool {
	return isDigit(c) || c == '.' || c == 'e' || c == 'E'
}

// awkStringEnd returns the index after the string literal that opens at i.
func awkStringEnd(p string, i int) (int, bool) {
	for j := i + 1; j < len(p); j++ {
		switch p[j] {
		case '\\':
			j++
		case '"':
			return j + 1, true
		default:
			// Any other character is part of the string.
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
