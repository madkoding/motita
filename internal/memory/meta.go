package memory

import (
	"strings"
	"time"
)

// Metadata an entry carries as Markdown front matter (the "properties" Obsidian shows):
//
//	---
//	author: Ana
//	date: 2026-10-28
//	tags: salary, hiring
//	---
//
// Without it the entry's date is when its file last changed and it has no author, so a vault the
// person already had indexes as it is.

// AgentAuthor is the author stamped on an entry the agent creates.
const AgentAuthor = "motita"

// Meta is what an entry says about itself.
type Meta struct {
	Author string    `json:"author,omitempty"`
	Date   time.Time `json:"date"`
	Tags   []string  `json:"tags,omitempty"`
}

// maxFrontLines is how far down an entry its front matter may close: a stray "---" rule much further
// down is a divider, not metadata.
const maxFrontLines = 30

var dateLayouts = []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"}

// ParseDate reads a date written the way front matter does; ok is false when it is none.
func ParseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// ParseMeta reads the front matter of body. modified stands in for a missing or unreadable date.
// rest is the text after the front matter and skipped how many lines the front matter took, so a
// line number of rest can be turned back into one of the file.
func ParseMeta(body string, modified time.Time) (m Meta, rest string, skipped int) {
	m = Meta{Date: modified}
	lines := strings.Split(body, "\n")
	if len(lines) < 3 || strings.TrimRight(lines[0], " \r") != "---" {
		return m, body, 0
	}
	end := -1
	for i := 1; i < len(lines) && i <= maxFrontLines; i++ {
		if strings.TrimRight(lines[i], " \r") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return m, body, 0
	}
	for _, line := range lines[1:end] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "author":
			m.Author = strings.Trim(value, "\"'")
		case "date":
			if t, ok := ParseDate(strings.Trim(value, "\"'")); ok {
				m.Date = t
			}
		case "tags":
			m.Tags = splitTags(value)
		default:
			// any other property is the person's and is left alone
		}
	}
	return m, strings.Join(lines[end+1:], "\n"), end + 1
}

// splitTags reads "a, b" or "[a, b]".
func splitTags(value string) []string {
	var out []string
	for _, t := range strings.Split(strings.Trim(value, "[]"), ",") {
		if t = strings.Trim(strings.TrimSpace(t), "\"'#"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Stamp puts front matter with the author and the day at the top of body, unless it already has
// some: what the person wrote as properties is theirs.
func Stamp(body, author string, now time.Time) string {
	if _, _, skipped := ParseMeta(body, now); skipped > 0 {
		return body
	}
	return "---\nauthor: " + author + "\ndate: " + now.UTC().Format("2006-01-02") + "\n---\n\n" + body
}

// Namespace is the collection an entry belongs to: its first folder. An entry at the top of the
// memory is in the namespace "".
func Namespace(p string) string {
	if i := strings.IndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return ""
}
