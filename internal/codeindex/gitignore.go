package codeindex

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// rule is one line of a .gitignore, compiled.
type rule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// parseIgnore compiles the lines of a .gitignore. A line that cannot be compiled is dropped: an
// ignore file that git would also reject must not stop the index.
func parseIgnore(text string) []rule {
	var rules []rule
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := rule{}
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		anchored := strings.HasPrefix(line, "/") || strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		re, err := regexp.Compile(globRegexp(line, anchored || strings.HasPrefix(line, "**/")))
		if line == "" || err != nil {
			continue
		}
		r.re = re
		rules = append(rules, r)
	}
	return rules
}

// globRegexp turns a gitignore glob into a regexp over a slash path relative to the ignore file.
// Without a slash in the pattern it matches at any depth, like git.
func globRegexp(glob string, anchored bool) string {
	var sb strings.Builder
	sb.WriteString("^")
	if !anchored {
		sb.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; {
		case strings.HasPrefix(glob[i:], "**/"):
			sb.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "/**") && i+3 == len(glob):
			sb.WriteString("/.*")
			i += 2
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(glob[i:], ']')
			if end < 0 {
				sb.WriteString(`\[`)
				continue
			}
			sb.WriteString(strings.Replace(glob[i:i+end+1], "[!", "[^", 1))
			i += end
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return sb.String()
}

// ignores holds the .gitignore rules of the folders walked so far, by folder relative to the root.
type ignores map[string][]rule

// load reads dir's .gitignore, when it has one, as the rules for the folder rel.
func (ig ignores) load(root, rel string) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel), ".gitignore"))
	if err == nil {
		ig[rel] = parseIgnore(string(data))
	}
}

// matched reports whether rel (a slash path from the root) is ignored. The rules of every folder
// above it apply, the deepest last, and the last rule that matches decides.
func (ig ignores) matched(rel string, isDir bool) bool {
	ignored := false
	dir := ""
	for {
		sub := rel
		if dir != "" {
			sub = strings.TrimPrefix(rel, dir+"/")
		}
		for _, r := range ig[dir] {
			if (!r.dirOnly || isDir) && r.re.MatchString(sub) {
				ignored = !r.negate
			}
		}
		next := nextDir(rel, dir)
		if next == "" {
			return ignored
		}
		dir = next
	}
}

// nextDir is the folder one level below dir on the way to rel, or "" when rel is directly in dir.
func nextDir(rel, dir string) string {
	rest := rel
	if dir != "" {
		rest = strings.TrimPrefix(rel, dir+"/")
	}
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return ""
	}
	return path.Join(dir, rest[:i])
}
