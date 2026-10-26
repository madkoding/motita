package readonly

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Filter is a program run inside this process on the output of the step before it: what a pipe
// into `head`, `tail`, `wc` or `grep` is for. It exists because read-only mode has no shell to
// connect two programs, and the pipes a plan actually writes are these (`git log | head -20`).
// It returns the filtered text and the exit code the real program would have.
type Filter func(in string) (string, int)

// FilterNames are the programs a pipe may end in.
const FilterNames = "head, tail, wc and grep"

// ParseFilter reads the step after a pipe. It returns an error naming what is supported when the
// program is not one of the filters, or is given a file (a filter reads the pipe, nothing else).
func ParseFilter(command string) (Filter, error) {
	name, args, err := SplitCommand(command)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(baseOf(name)) {
	case "head":
		return lineFilter(args, true)
	case "tail":
		return lineFilter(args, false)
	case "wc":
		return wcFilter(args)
	case "grep":
		return grepFilter(args)
	default:
		return nil, fmt.Errorf("%q cannot follow a pipe in read-only mode; only %s can, and a command "+
			"that needs a file takes it as an argument instead", name, FilterNames)
	}
}

// lineCount reads the count of head and tail from `-n N`, `-nN`, `--lines=N` or `-N`.
func lineCount(args []string) (int, error) {
	count := 10
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := ""
		switch {
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return 0, fmt.Errorf("%s needs a number", a)
			}
			i++
			value = args[i]
		case strings.HasPrefix(a, "--lines="):
			value = strings.TrimPrefix(a, "--lines=")
		case strings.HasPrefix(a, "-n"):
			value = a[2:]
		case len(a) > 1 && a[0] == '-' && strings.Trim(a[1:], "0123456789") == "":
			value = a[1:]
		default:
			return 0, fmt.Errorf("%q is not an option read-only mode supports after a pipe (use -n N)", a)
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not a line count", value)
		}
		count = n
	}
	return count, nil
}

// lineFilter is head (first true) or tail.
func lineFilter(args []string, first bool) (Filter, error) {
	n, err := lineCount(args)
	if err != nil {
		return nil, err
	}
	return func(in string) (string, int) {
		lines := splitLines(in)
		switch {
		case n >= len(lines):
		case first:
			lines = lines[:n]
		default:
			lines = lines[len(lines)-n:]
		}
		return joinLines(lines), 0
	}, nil
}

// splitLines splits text into lines without the final newline's empty element.
func splitLines(in string) []string {
	if in == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(in, "\n"), "\n")
}

// joinLines is the inverse of splitLines.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// wcFilter counts lines (-l), words (-w) or bytes (-c); with none, it prints the three.
func wcFilter(args []string) (Filter, error) {
	mode := ""
	for _, a := range args {
		if a != "-l" && a != "-w" && a != "-c" {
			return nil, fmt.Errorf("%q is not an option read-only mode supports after a pipe (use -l, -w or -c)", a)
		}
		mode = a
	}
	return func(in string) (string, int) {
		lines, words, bytes := len(splitLines(in)), len(strings.Fields(in)), len(in)
		switch mode {
		case "-l":
			return strconv.Itoa(lines) + "\n", 0
		case "-w":
			return strconv.Itoa(words) + "\n", 0
		case "-c":
			return strconv.Itoa(bytes) + "\n", 0
		default:
			return fmt.Sprintf("%d %d %d\n", lines, words, bytes), 0
		}
	}, nil
}

// grepFilter keeps the lines that match (-v: that do not). Patterns are Go regular expressions,
// which is close to grep -E; -F makes the pattern literal.
func grepFilter(args []string) (Filter, error) {
	var ignoreCase, invert, count, number, literal bool
	pattern, havePattern := "", false
	for _, a := range args {
		switch {
		case a == "--":
		case len(a) > 1 && a[0] == '-' && !havePattern:
			for _, c := range a[1:] {
				switch c {
				case 'i':
					ignoreCase = true
				case 'v':
					invert = true
				case 'c':
					count = true
				case 'n':
					number = true
				case 'F':
					literal = true
				case 'E':
				default:
					return nil, fmt.Errorf("grep -%c is not supported after a pipe (use -i, -v, -c, -n, -E or -F)", c)
				}
			}
		case !havePattern:
			pattern, havePattern = a, true
		default:
			return nil, fmt.Errorf("grep after a pipe reads the pipe, not the file %q", a)
		}
	}
	if !havePattern {
		return nil, fmt.Errorf("grep after a pipe needs a pattern")
	}
	if literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("the grep pattern %q is not valid: %v", pattern, err)
	}
	return func(in string) (string, int) {
		var kept []string
		for i, line := range splitLines(in) {
			if re.MatchString(line) == invert {
				continue
			}
			if number {
				line = fmt.Sprintf("%d:%s", i+1, line)
			}
			kept = append(kept, line)
		}
		exit := 0
		if len(kept) == 0 {
			exit = 1
		}
		if count {
			return strconv.Itoa(len(kept)) + "\n", exit
		}
		return joinLines(kept), exit
	}, nil
}
