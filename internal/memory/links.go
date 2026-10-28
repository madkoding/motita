package memory

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Link is one [[wiki link]] in an entry: the target as written, the text shown for it when the link
// has an alias ([[target|alias]]), and the heading it points into ([[target#heading]]).
type Link struct {
	Target  string `json:"target"`
	Alias   string `json:"alias,omitempty"`
	Heading string `json:"heading,omitempty"`
	// Path is the entry the target resolves to, "" when there is none yet.
	Path string `json:"path,omitempty"`
}

var (
	wikiLink = regexp.MustCompile(`!?\[\[([^\[\]\n]+)\]\]`)
	fence    = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")
)

// ParseLinks returns the wiki links of body, in order and without repeats. Links inside code are
// text, not links, as in Obsidian.
func ParseLinks(body string) []Link {
	body = fence.ReplaceAllString(body, "")
	out := []Link{}
	seen := map[string]bool{}
	for _, m := range wikiLink.FindAllStringSubmatch(body, -1) {
		l := parseLink(m[1])
		if l.Target == "" || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, l)
	}
	return out
}

// parseLink splits "target#heading|alias".
func parseLink(inner string) Link {
	target, alias, _ := strings.Cut(inner, "|")
	target, heading, _ := strings.Cut(target, "#")
	return Link{
		Target:  strings.TrimSuffix(strings.TrimSpace(target), ext),
		Alias:   strings.TrimSpace(alias),
		Heading: strings.TrimSpace(heading),
	}
}

// Resolve finds the entry a link target names, as Obsidian does: a target with a folder matches the
// end of a path, a bare name matches an entry of that name anywhere; the case does not matter, and
// when several match the shallowest wins.
func Resolve(all []Entry, target string) (string, bool) {
	want := strings.ToLower(strings.Trim(strings.ReplaceAll(target, "\\", "/"), "/"))
	if want == "" {
		return "", false
	}
	want = strings.TrimSuffix(want, ext)
	var hits []string
	for _, n := range all {
		stem := strings.ToLower(strings.TrimSuffix(n.Path, path.Ext(n.Path)))
		if stem == want || strings.HasSuffix(stem, "/"+want) {
			hits = append(hits, n.Path)
		}
	}
	if len(hits) == 0 {
		return "", false
	}
	sort.Slice(hits, func(i, j int) bool {
		di, dj := strings.Count(hits[i], "/"), strings.Count(hits[j], "/")
		if di != dj {
			return di < dj
		}
		return hits[i] < hits[j]
	})
	return hits[0], true
}

// LinksOf returns the links of an entry with the entries they resolve to.
func LinksOf(all []Entry, body string) []Link {
	links := ParseLinks(body)
	for i := range links {
		links[i].Path, _ = Resolve(all, links[i].Target)
	}
	return links
}

// Backlinks returns the entries that link to p, with the line each links from.
func (v *Store) Backlinks(p string) []Backlink {
	all := v.List()
	out := []Backlink{}
	for _, n := range all {
		if n.Path == p {
			continue
		}
		body, _, err := v.Read(n.Path)
		if err != nil {
			continue
		}
		for _, l := range ParseLinks(body) {
			if got, ok := Resolve(all, l.Target); ok && got == p {
				out = append(out, Backlink{Entry: n, Context: contextOf(body, l)})
				break
			}
		}
	}
	return out
}

// Backlink is an entry that links to another, with the first line holding the link.
type Backlink struct {
	Entry
	Context string `json:"context"`
}

// contextOf is the first line of body that holds l, trimmed.
func contextOf(body string, l Link) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "[["+l.Target) {
			line = strings.TrimSpace(line)
			if r := []rune(line); len(r) > 160 {
				line = string(r[:160]) + "…"
			}
			return line
		}
	}
	return ""
}

// movedLink answers, for the target of a link, the new target when it led to the entry src: the
// full path for a link written with one, the bare name otherwise.
func movedLink(before []Entry, src, full, bare string) func(string) (string, bool) {
	return func(target string) (string, bool) {
		if got, ok := Resolve(before, target); !ok || got != src {
			return "", false
		}
		if strings.Contains(target, "/") {
			return full, true
		}
		return bare, true
	}
}

// RenameLinked moves an entry like Rename and rewrites the [[links]] of the other entries that led to
// it, so none of them is left pointing nowhere. It returns how many entries it rewrote. Links in
// code and the heading and alias of a link are kept.
func (v *Store) RenameLinked(from, to string) (int, error) {
	src, err := Clean(from)
	if err != nil {
		return 0, err
	}
	before := v.List()
	if err := v.Rename(from, to); err != nil {
		return 0, err
	}
	dst, _ := Clean(to)
	after := v.List()
	// A bare name is kept bare while it still finds the moved entry; otherwise the path is written.
	bare := strings.TrimSuffix(path.Base(dst), ext)
	if got, _ := Resolve(after, bare); got != dst {
		bare = strings.TrimSuffix(dst, ext)
	}
	full := strings.TrimSuffix(dst, ext)
	updated := 0
	for _, n := range after {
		body, _, err := v.Read(n.Path)
		if err != nil {
			continue
		}
		next := rewriteLinks(body, movedLink(before, src, full, bare))
		if next == body {
			continue
		}
		if _, err := v.Write(n.Path, next, false); err == nil {
			updated++
		}
	}
	return updated, nil
}

// rewriteLinks replaces the target of every link outside code for which swap says so.
func rewriteLinks(body string, swap func(target string) (string, bool)) string {
	var b strings.Builder
	last := 0
	for _, loc := range fence.FindAllStringIndex(body, -1) {
		b.WriteString(rewriteText(body[last:loc[0]], swap))
		b.WriteString(body[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(rewriteText(body[last:], swap))
	return b.String()
}

func rewriteText(text string, swap func(string) (string, bool)) string {
	return wikiLink.ReplaceAllStringFunc(text, func(m string) string {
		embed := strings.HasPrefix(m, "!")
		inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(m, "!"), "[["), "]]")
		rest := ""
		if i := strings.IndexAny(inner, "#|"); i >= 0 {
			inner, rest = inner[:i], inner[i:]
		}
		to, ok := swap(strings.TrimSuffix(strings.TrimSpace(inner), ext))
		if !ok {
			return m
		}
		out := "[[" + to + rest + "]]"
		if embed {
			out = "!" + out
		}
		return out
	})
}
