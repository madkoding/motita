package policy

import (
	"fmt"
	"strings"
)

// What changes when nothing confines a command (Mode.Unconfined).
//
// On Linux with Landlock a command the policy lets through still cannot read the user's keys, the
// logins motita keeps or another project's files: the sandbox hides them, whatever the command
// does. That is what lets a reader run in silence wherever its file is, and the project's own
// tools run in silence whatever code the project holds. Where there is no such confinement
// (Windows, macOS, a kernel without Landlock) both promises rest on the policy alone, so two kinds
// of Allow become questions:
//
//   - a command that runs code the policy cannot see (unconfinedRuns): `python t.py`, `./x.ps1`,
//     `make`, `npm test`. The script, the Makefile or the test was written by the model, or by
//     whoever wrote the repository, and it can read `~/.motita/auth` as easily as `cat` can.
//     The operator may keep these silent (Mode.TrustUnconfinedRuns);
//   - a command that names a path outside the workspace or among the user's keys
//     (privateDirs): `Get-Content ~/.motita/auth/x`, `Select-String -Path ~/.ssh/*`,
//     `cp ../../.ssh/id_rsa .`, `git log --output=../../x`.
//
// What stays inside the workspace is still silent: reading and writing the project is the work.

// unconfinedRuns are the rules whose Allow runs code the policy did not read.
var unconfinedRuns = map[string]bool{"local-tool": true, "project-local": true, "project-tooling": true}

// privateDirs are the directories, wherever they are, that hold a person's keys and logins. They
// are named so a workspace that contains one (a home directory opened as the workspace) does not
// make its keys part of the work.
var privateDirs = map[string]bool{
	".ssh": true, ".gnupg": true, ".aws": true, ".azure": true, ".kube": true, ".docker": true,
	".git-credentials": true, ".netrc": true, "_netrc": true,
}

// motitaPrivate are the entries of motita's own home (`~/.motita`) that hold its logins and keys.
// A project's `.motita` directory (its anchor, its skills) is the project's and stays readable.
var motitaPrivate = map[string]bool{"auth": true, "motita.env": true, "motita.yaml": true}

// unconfinedDecision turns an Allow that only the sandbox kept small into a question, when
// nothing confines the command. Any other verdict, and every verdict under confinement, is
// returned as it is.
func (m Mode) unconfinedDecision(d Decision, args []string, dir string) Decision {
	if !m.Unconfined || d.Verdict != Allow {
		return d
	}
	if unconfinedRuns[d.Rule] && !m.TrustUnconfinedRuns {
		return Decision{Ask, d.Reason + "; nothing on this platform confines what it runs, so code it " +
			"starts could read the user's keys and logins, and it is confirmed", "unconfined-run", false}
	}
	for _, a := range args {
		for _, p := range pathOperands(a) {
			if m.unconfinedOutside(p, dir) {
				return m.unconfinedRead(d.Reason, p, dir)
			}
		}
	}
	return d
}

// unconfinedRead is the question about a command that reaches p while nothing confines it.
func (m Mode) unconfinedRead(reason, p, dir string) Decision {
	return Decision{Ask, fmt.Sprintf("%s; it reaches %q, which is outside the directory this task works "+
		"in (%s) or among the user's keys, and nothing on this platform confines what it reads, so it "+
		"is confirmed", reason, p, dir), "unconfined-read", false}
}

// pathOperand is the path a word may name: the word itself, or the value of an option written
// with it (`--output=x`, `-Path:x`). An option without a value names no path.
func pathOperand(word string) (string, bool) {
	w := unquote(word)
	if strings.HasPrefix(w, "-") {
		i := strings.IndexAny(w, "=:")
		if i < 0 {
			return "", false
		}
		w = w[i+1:]
	}
	return w, w != ""
}

// pathOperands are the paths a word may name: pathOperand's, and for a short option written
// with its value glued on (`-f../../.ssh/id_rsa`, `-f~/.ssh/id_rsa`) the value after the letter,
// which pathOperand cannot see because the word has no `=` or `:`.
func pathOperands(word string) []string {
	var paths []string
	if p, ok := pathOperand(word); ok {
		paths = append(paths, p)
	}
	if w := unquote(word); len(w) > 2 && w[0] == '-' && w[1] != '-' {
		paths = append(paths, w[2:])
	}
	return paths
}

// unconfinedOutside reports whether p, a path a command was handed, is outside dir or among the
// user's keys. A URL is not a path: what it reaches is judged by the program that requests it.
func (m Mode) unconfinedOutside(p, dir string) bool {
	if strings.Contains(p, "://") {
		return false
	}
	if m.FullDisk {
		// The whole disk is the work: only the user's keys stay a question.
		return privatePath(p)
	}
	if m.PowerShell && windowsPath(p) {
		return true
	}
	return firstOutside([]string{p}, dir) != "" || privatePath(p)
}

// privatePath reports whether p passes through a directory that holds keys or logins.
func privatePath(p string) bool {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	for i, part := range parts {
		part = strings.ToLower(part)
		if privateDirs[part] {
			return true
		}
		if part == ".motita" && i+1 < len(parts) && motitaPrivate[strings.ToLower(parts[i+1])] {
			return true
		}
	}
	return false
}
