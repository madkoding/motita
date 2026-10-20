package notes

import (
	"errors"
	"fmt"
	"strings"
)

// The text the agent reads and writes the vault through. Every answer names the note it came from
// as a [[link]] and its path, so the model can say where a fact is from, and every refusal says
// what to do instead: the answer is read by a model, not shown to a person.

const (
	errPrefix    = "Error: "
	maxToolChars = 12000
	toolHits     = 8
)

// SearchText answers "which notes say something about query".
func (v *Vault) SearchText(query string) string {
	if strings.TrimSpace(query) == "" {
		return errPrefix + "say what to search the notes for."
	}
	hits := v.Search(query, toolHits)
	if len(hits) == 0 {
		return "No note matches " + fmt.Sprintf("%q", query) +
			". Search with fewer or different words; the notes only hold what the person wrote down."
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "[[%s]] (%s)", h.Title, h.Path)
		if h.Line > 0 {
			fmt.Fprintf(&b, " line %d: %s", h.Line, h.Snippet)
		}
		b.WriteString("\n")
	}
	b.WriteString("Open one with read_note before relying on it, and cite it as [[Title]].")
	return b.String()
}

// ReadText answers with a note in full, found by path or by the name a [[link]] would use, and
// with what it links to and what links to it.
func (v *Vault) ReadText(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errPrefix + "name the note to read, by title or path."
	}
	all := v.List()
	path, ok := Resolve(all, ref)
	if !ok {
		return fmt.Sprintf(errPrefix+"there is no note %q. Use search_notes to find the right one.", ref)
	}
	body, _, err := v.Read(path)
	if err != nil {
		return errPrefix + "" + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[[%s]] (%s)\n\n%s", Title(path), path, body)
	writeLinks(&b, LinksOf(all, body))
	writeBacklinks(&b, v.Backlinks(path))
	out := b.String()
	if len(out) > maxToolChars {
		out = out[:maxToolChars] + "\n[the note continues; it was cut here]"
	}
	return out
}

// toolPath is the vault path for a title or path the model gave: ".md" is added when missing.
func toolPath(ref string) string {
	ref = strings.TrimSpace(ref)
	if !strings.EqualFold(ref[max(0, len(ref)-3):], ext) {
		ref += ext
	}
	return ref
}

// WriteText creates a note. It never replaces one: a note is the person's, and the model changes it
// by appending.
func (v *Vault) WriteText(ref, body string) string {
	if strings.TrimSpace(ref) == "" || strings.TrimSpace(body) == "" {
		return errPrefix + "give the note's path or title and the text to put in it."
	}
	n, err := v.Write(toolPath(ref), body, true)
	switch {
	case errors.Is(err, ErrExists):
		return fmt.Sprintf(errPrefix+"the note %q already exists. Read it, then add to it with append_note; "+
			"notes are never overwritten.", ref)
	case err != nil:
		return errPrefix + err.Error()
	default:
		// created
	}
	return fmt.Sprintf("Created [[%s]] (%s). Link it from related notes with [[%s]].", n.Title, n.Path, n.Title)
}

// AppendText adds text to the end of a note, creating the note when it does not exist.
func (v *Vault) AppendText(ref, text string) string {
	if strings.TrimSpace(ref) == "" || strings.TrimSpace(text) == "" {
		return errPrefix + "give the note's path or title and the text to add."
	}
	path := toolPath(ref)
	if found, ok := Resolve(v.List(), ref); ok {
		path = found
	}
	body, _, err := v.Read(path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return errPrefix + "" + err.Error()
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if body != "" {
		body += "\n"
	}
	n, err := v.Write(path, body+strings.TrimRight(text, "\n")+"\n", false)
	if err != nil {
		return errPrefix + "" + err.Error()
	}
	return fmt.Sprintf("Added to [[%s]] (%s).", n.Title, n.Path)
}

// writeLinks lists what a note links to, marking the links that lead nowhere yet.
func writeLinks(b *strings.Builder, links []Link) {
	if len(links) == 0 {
		return
	}
	b.WriteString("\n\nLinks to: ")
	for i, l := range links {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("[[" + l.Target + "]]")
		if l.Path == "" {
			b.WriteString(" (no note yet)")
		}
	}
}

// writeBacklinks lists the notes that link to a note.
func writeBacklinks(b *strings.Builder, back []Backlink) {
	if len(back) == 0 {
		return
	}
	b.WriteString("\nLinked from: ")
	for i, bl := range back {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("[[" + bl.Title + "]]")
	}
}
