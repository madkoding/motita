package policy

// Commands reads a line the way the policy does and returns the words of each command in it, in
// order: the pieces a pipe, a chain or a `;` separates, with heredoc bodies, redirections and the
// control words of a loop or a branch left out. `git add . && git commit -m "a && b"` is two
// commands, and the quoted `&&` is not a third. It never fails: a line it cannot split further
// comes back as one command.
func Commands(line string) [][]string {
	main, _ := splitHeredocs(line)
	var out [][]string
	for _, seg := range lineSegments(main) {
		if seg.redirect {
			continue
		}
		words, syntax := stripShellKeyword(seg.words)
		if syntax || len(words) == 0 {
			continue
		}
		out = append(out, words)
	}
	return out
}

// IsBuiltin says a word is run by the shell itself (cd, echo, export...) rather than being a
// program of its own, which is why saying yes to a line should not hang on it.
func IsBuiltin(word string) bool {
	return shellBuiltins[word]
}
