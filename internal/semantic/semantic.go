// Package semantic checks commit subjects and pull request titles against
// Conventional Commits, the form the repository's release tooling reads.
package semantic

import (
	"fmt"
	"regexp"
	"strings"
)

// CommitTypes are the types a semantic commit may carry: the Conventional
// Commits set the repository's release tooling reads.
var CommitTypes = []string{"feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert"}

// maxSubject is the longest subject line accepted: long enough for a real
// description, short enough to read in `git log --oneline`.
const maxSubject = 100

var semanticRe = regexp.MustCompile(`^(` + strings.Join(CommitTypes, "|") + `)(\([a-z0-9][a-z0-9._/-]*\))?(!)?: \S.*$`)

// LintSubject checks a commit subject or a pull request title against
// Conventional Commits: `type(scope)!: description`. The error says what to do,
// because the reader is an agent or a user who has to fix the line.
func LintSubject(subject string) error {
	subject = strings.TrimSpace(subject)
	switch {
	case subject == "":
		return fmt.Errorf("the title is empty: write it as \"type(scope): description\", with type one of %s", strings.Join(CommitTypes, ", "))
	case strings.ContainsAny(subject, "\r\n"):
		return fmt.Errorf("the title must be one line")
	case !semanticRe.MatchString(subject):
		return fmt.Errorf("%q is not a semantic title: write it as \"type(scope): description\", with type one of %s (for example \"fix(auth): refresh the token before it expires\")",
			subject, strings.Join(CommitTypes, ", "))
	case len(subject) > maxSubject:
		return fmt.Errorf("the title is %d characters long: keep it under %d", len(subject), maxSubject)
	}
	return nil
}

// CommitSubject reads the subject a `git commit` is being given from its arguments
// (everything after the word commit), so a policy can check it BEFORE the commit is made.
//
// ok is false when the subject cannot be known from the command line: the message comes from
// a file (-F), from an earlier commit (-C, -c, --fixup, --squash), or from an editor (no -m
// at all). Those are not checked here and git's own hook, if the repository has one, still
// sees them. A cluster of short flags ending in m (`-am "msg"`) takes the next argument, as
// git does. Only the first -m counts: it is the subject, the later ones are paragraphs.
func CommitSubject(args []string) (subject string, ok bool) {
	var message string
	found := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-F" || a == "--file" || strings.HasPrefix(a, "--file=") || strings.HasPrefix(a, "-F") && len(a) > 2 && !strings.HasPrefix(a, "--"),
			a == "-C" || a == "-c" || a == "--reuse-message" || a == "--reedit-message" || strings.HasPrefix(a, "--fixup") || strings.HasPrefix(a, "--squash"):
			return "", false
		case a == "--message":
			if i+1 < len(args) && !found {
				message, found = args[i+1], true
			}
			i++
		case strings.HasPrefix(a, "--message="):
			if !found {
				message, found = strings.TrimPrefix(a, "--message="), true
			}
		case strings.HasPrefix(a, "--") || !strings.HasPrefix(a, "-") || a == "-":
			// A long flag, or a pathspec: neither carries the message.
		case clusterTakesMessage(a):
			if i+1 < len(args) && !found {
				message, found = args[i+1], true
			}
			i++
		case strings.HasPrefix(a, "-m"):
			if !found {
				message, found = a[2:], true
			}
		}
	}
	if !found {
		return "", false
	}
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		message = message[:i]
	}
	return strings.TrimSpace(message), true
}

// clusterTakesMessage reports whether a short-flag cluster ends in m: `-m`, `-am`, `-sam`. It
// is only asked about arguments that start with a single dash and have something after it.
func clusterTakesMessage(a string) bool {
	if a[len(a)-1] != 'm' {
		return false
	}
	for _, r := range a[1:] {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
