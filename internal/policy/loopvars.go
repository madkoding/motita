package policy

import (
	"regexp"
	"strings"
)

// forBinding is `for NAME in WORD...` at the start of a line or after a separator, up to the `;`
// or newline that ends the list.
var forBinding = regexp.MustCompile(`(?:^|[;&|\n])\s*for\s+([A-Za-z_][A-Za-z0-9_]*)\s+in\s+([^;\n]+)`)

// plainWord is a word of a for-list that is a path and nothing else: no quote, no variable, no
// substitution, nothing the shell reads before it is a path.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./*?@+,=:%-]+$`)

// bindLoopVariables rewrites `$NAME` and `${NAME}` with a word of the list NAME loops over, when
// every word of that list stays inside the workspace. `for f in a b; do cat > "$f.txt"; done` then
// judges `cat > "a.txt"`: a path inside, which is what every pass of the loop writes.
//
// A variable is otherwise a path nobody here can see (see expandsVariable), and asking about
// every loop a model writes over the project's own files was the friction this trades away. Only
// a list of plain words inside the workspace binds its variable; a list with a variable, a
// substitution, a quote or a path outside leaves `$NAME` as it is, to be asked about.
func bindLoopVariables(line, dir string) string {
	if dir == "" || !strings.Contains(line, "$") {
		return line
	}
	for _, b := range forBinding.FindAllStringSubmatch(line, -1) {
		name, list := b[1], strings.Fields(b[2])
		if len(list) == 0 || !plainWords(list) || firstOutside(list, dir) != "" {
			continue
		}
		line = replaceVariable(line, name, list[0])
	}
	return line
}

// plainWords reports whether every word is a plain path word.
func plainWords(words []string) bool {
	for _, w := range words {
		if !plainWord.MatchString(w) {
			return false
		}
	}
	return true
}

// replaceVariable puts value where `$name` and `${name}` are, and leaves `$names` alone: a longer
// name is another variable.
func replaceVariable(line, name, value string) string {
	line = strings.ReplaceAll(line, "${"+name+"}", value)
	var b strings.Builder
	for {
		i := strings.Index(line, "$"+name)
		if i < 0 {
			b.WriteString(line)
			return b.String()
		}
		end := i + 1 + len(name)
		b.WriteString(line[:i])
		if end < len(line) && isNameRune(line[end]) {
			b.WriteString(line[i:end])
		} else {
			b.WriteString(value)
		}
		line = line[end:]
	}
}

// isNameRune says c can continue a variable's name.
func isNameRune(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '_'
	}
}
