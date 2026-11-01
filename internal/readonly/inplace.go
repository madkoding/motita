package readonly

import "strings"

// `sed -i` is the edit a model reaches for first, and it is the one sed form this package refuses
// outright, because it rewrites files. The mode where writing is the point does not need it
// refused: it needs to know WHICH files are rewritten, and that the script does nothing besides.
// SedInPlace answers both, so that question is put to the workspace check and not to a person.

// SedInPlace reads `sed -i` with args, the arguments after the program. ok is true when the line
// is an in-place edit whose script read-only mode would run once the in-place option is taken
// out (no `w`, `r` or `e`, no `-f` script it cannot see), with a backup suffix, if any, that
// stays beside the file it backs up; files are the files the edit rewrites. Anything else is not
// ok, and the caller answers it as it always did.
func SedInPlace(args []string) (files []string, ok bool) {
	rest, inPlace := withoutInPlace(args)
	if !inPlace {
		return nil, false
	}
	if _, bad := sedRule(rest); bad {
		return nil, false
	}
	o := &optionScan{args: rest}
	for ; o.i < len(rest); o.i++ {
		_, _ = o.sedArgument() // sedRule has already read these arguments without a refusal
	}
	files = o.operands
	if !o.explicit && len(files) > 0 {
		files = files[1:]
	}
	return files, len(files) > 0
}

// withoutInPlace is args with the in-place option taken out, and whether there was one. A line
// that cannot be read safely - `--follow-symlinks`, which writes through a link to wherever it
// points, a suffix that names another directory, an empty argument - comes back without one, so
// SedInPlace leaves it to the question.
func withoutInPlace(args []string) (rest []string, inPlace bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "" || a == "--":
			// An empty argument means different things to GNU and BSD sed; `--` ends the options.
			return nil, false
		case strings.HasPrefix(a, "--"):
			keep, edit, safe := inPlaceLong(a)
			if !safe {
				return nil, false
			}
			inPlace = inPlace || edit
			rest = append(rest, keep...)
		case isShortOption(a):
			keep, edit, value, safe := inPlaceShort(a)
			if !safe || (edit && a == "-i" && i+1 < len(args) && strings.Contains(args[i+1], "..")) {
				// BSD sed takes the argument after a bare -i as the backup suffix.
				return nil, false
			}
			inPlace = inPlace || edit
			rest = append(rest, keep...)
			if value && i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
		default:
			rest = append(rest, a)
		}
	}
	return rest, inPlace
}

// inPlaceLong reads a long option: keep is what stays of it, edit says it was --in-place.
func inPlaceLong(a string) (keep []string, edit, safe bool) {
	name, suffix, _ := strings.Cut(a, "=")
	switch resolveLong(name, sedLongOptions) {
	case "--in-place":
		return nil, true, safeSuffix(suffix)
	case "--follow-symlinks":
		return nil, false, false
	default:
		return []string{a}, false, true
	}
}

// inPlaceShort reads a short option or a cluster: keep is what stays of it, edit says it held
// -i, and value that the next argument is the value of its last option (`-e SCRIPT`).
func inPlaceShort(a string) (keep []string, edit, value, safe bool) {
	for j := 1; j < len(a); j++ {
		switch a[j] {
		case 'e', 'l', 'f':
			// The rest of the cluster, or the next argument, is this option's value.
			return []string{a}, false, j == len(a)-1, true
		case 'i':
			// GNU's -i[SUFFIX]: the rest of the cluster is the backup suffix.
			if j > 1 {
				keep = []string{a[:j]}
			}
			return keep, true, false, safeSuffix(a[j+1:])
		default:
			// Any other letter is read by sedRule.
		}
	}
	return []string{a}, false, false, true
}

// safeSuffix says a backup suffix keeps the backup beside the file: GNU sed reads a `/` as a
// directory and a `*` as the file's name, which could put the backup anywhere.
func safeSuffix(s string) bool {
	return !strings.ContainsAny(s, `/\*`) && !strings.Contains(s, "..")
}
