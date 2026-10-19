package notes

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Link is one [[wiki link]] in a note: the target as written, the text shown for it when the link
// has an alias ([[target|alias]]), and the heading it points into ([[target#heading]]).
type Link struct {
	Target  string `json:"target"`
	Alias   string `json:"alias,omitempty"`
	Heading string `json:"heading,omitempty"`
	// Path is the note the target resolves to, "" when there is none yet.
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

// Resolve finds the note a link target names, as Obsidian does: a target with a folder matches the
// end of a path, a bare name matches a note of that name anywhere; the case does not matter, and
// when several match the shallowest wins.
func Resolve(all []Note, target string) (string, bool) {
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

// LinksOf returns the links of a note with the notes they resolve to.
func LinksOf(all []Note, body string) []Link {
	links := ParseLinks(body)
	for i := range links {
		links[i].Path, _ = Resolve(all, links[i].Target)
	}
	return links
}

// Backlinks returns the notes that link to p, with the line each links from.
func (v *Vault) Backlinks(p string) []Backlink {
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
				out = append(out, Backlink{Note: n, Context: contextOf(body, l)})
				break
			}
		}
	}
	return out
}

// Backlink is a note that links to another, with the first line holding the link.
type Backlink struct {
	Note
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
