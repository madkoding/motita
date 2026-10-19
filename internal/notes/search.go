package notes

import (
	"sort"
	"strings"
)

// Hit is a note that matches a search, with the first line that does.
type Hit struct {
	Note
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// Search finds the notes whose name or text holds every word of query, name matches first. It is
// the plain search the gateway falls back to when the code index is off.
func (v *Vault) Search(query string, limit int) []Hit {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	type scored struct {
		Hit
		byName bool
	}
	var found []scored
	for _, n := range v.List() {
		body, _, err := v.Read(n.Path)
		if err != nil {
			continue
		}
		hay := strings.ToLower(n.Path + "\n" + body)
		if !holdsAll(hay, words) {
			continue
		}
		h := scored{Hit: Hit{Note: n}, byName: holdsAll(strings.ToLower(n.Path), words)}
		h.Line, h.Snippet = firstLine(body, words)
		found = append(found, h)
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].byName && !found[j].byName })
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	out := make([]Hit, len(found))
	for i, f := range found {
		out[i] = f.Hit
	}
	return out
}

func holdsAll(hay string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// firstLine is the first line (1-based) that holds any word, and that line trimmed; 0 and "" when
// only the name matched.
func firstLine(body string, words []string) (int, string) {
	for i, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		for _, w := range words {
			if strings.Contains(low, w) {
				line = strings.TrimSpace(line)
				if r := []rune(line); len(r) > 160 {
					line = string(r[:160]) + "…"
				}
				return i + 1, line
			}
		}
	}
	return 0, ""
}
