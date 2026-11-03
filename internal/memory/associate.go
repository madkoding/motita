package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Saving works like a brain: what is new is tied to what is already known. A fact that an entry
// already covers goes into that entry, and a new entry is associated with the entries closest to
// it, so the memory grows as one web and not as a pile of loose notes.

const (
	// associateMax is how many entries a new one is associated with on its own.
	associateMax = 3
	// associateMinShared is how many words a text must share with an entry to be associated with it.
	associateMinShared = 3
	// associateMinOverlap is the share of the text's words an associated entry must hold.
	associateMinOverlap = 0.25
	// MergeMinOverlap is the share of a text's words an entry must hold for the text to belong in it.
	MergeMinOverlap = 0.6
	// similarProbeRunes bounds the text a likeness is searched with.
	similarProbeRunes = 600
)

// Similar is an entry close to a text: how many of the text's words its best section holds, and
// which share of them.
type Similar struct {
	Path    string  `json:"path"`
	Title   string  `json:"title"`
	Shared  int     `json:"shared"`
	Overlap float64 `json:"overlap"`
}

// distinct is the words of a text, each once.
func distinct(words []string) map[string]bool {
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}

// Similar lists up to n entries close to text, the closest first, leaving out the entry skip. An
// entry shares at least associateMinShared words with the text, or it is not listed.
func (v *Store) Similar(text, skip string, n int) []Similar {
	want := distinct(terms(text))
	if len(want) == 0 || n <= 0 {
		return nil
	}
	res := v.Search(context.Background(), Query{Text: clip(text, similarProbeRunes), Limit: n + 4})
	seen := map[string]bool{skip: true}
	var out []Similar
	for _, h := range res.Hits {
		if seen[h.Path] {
			continue
		}
		seen[h.Path] = true
		have := distinct(terms(h.Title + " " + h.Block))
		shared := 0
		for w := range want {
			if have[w] {
				shared++
			}
		}
		if shared < associateMinShared {
			continue
		}
		overlap := float64(shared) / float64(len(want))
		out = append(out, Similar{Path: h.Path, Title: h.Title, Shared: shared, Overlap: overlap})
		if len(out) == n {
			break
		}
	}
	return out
}

// Home is the entry a text without a title belongs in: the closest entry when it already holds most
// of the text's words. ok is false when none does.
func (v *Store) Home(text string) (string, bool) {
	best := v.Similar(text, "", 1)
	if len(best) == 0 || best[0].Overlap < MergeMinOverlap {
		return "", false
	}
	return best[0].Path, true
}

// LinkName is how a [[link]] names the entry at p: its bare title when no other entry has it, its
// path without ".md" otherwise, so a link never depends on which of two namesakes wins.
func LinkName(all []Entry, p string) string {
	title := Title(p)
	for _, e := range all {
		if e.Path != p && strings.EqualFold(e.Title, title) {
			return strings.TrimSuffix(p, ext)
		}
	}
	return title
}

// Keep is how the agent remembers: text is added to the end of the entry ref names (created when
// there is none), as kind of memory when kind names one. A new entry is associated with the entries
// closest to it. The answer says where it went and what it was tied to.
func (v *Store) Keep(ref, text, kind string) string {
	if strings.TrimSpace(ref) == "" || strings.TrimSpace(text) == "" {
		return errPrefix + "give the entry's path or title and the text to add."
	}
	path := v.appendTarget(ref)
	body, _, err := v.Read(path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return errPrefix + err.Error()
	}
	text = strings.TrimRight(text, "\n") + "\n"
	var added string
	var linked []string
	if body == "" {
		added = v.belongs(path, StampKind(text, AgentAuthor, kind, now()))
		added, linked = v.associate(added, text, path)
	} else {
		added = strings.TrimRight(body, "\n") + "\n\n" + text
	}
	if meta, _, _ := ParseMeta(added, now()); kind != "" && meta.Type == "" && NormalizeKind(kind) != "" {
		added = SetKind(added, NormalizeKind(kind))
	}
	n, err := v.Write(path, added, false)
	if err != nil {
		return errPrefix + err.Error()
	}
	reply := fmt.Sprintf("Added to [[%s]] (%s).", n.Title, n.Path)
	if len(linked) > 0 {
		reply += " Associated with [[" + strings.Join(linked, "]], [[") + "]]."
	}
	return reply + v.projectHint(n.Path)
}

// associate ties a new entry (body, about text, to be written at path) to the entries closest to
// it, and says which.
func (v *Store) associate(body, text, path string) (string, []string) {
	near := v.Similar(text, path, associateMax)
	if len(near) == 0 {
		return body, nil
	}
	all := v.List()
	var linked []string
	for _, s := range near {
		if s.Overlap < associateMinOverlap {
			continue
		}
		name := LinkName(all, s.Path)
		body = AddRelation(body, RelRelated, name)
		linked = append(linked, name)
	}
	return body, linked
}
