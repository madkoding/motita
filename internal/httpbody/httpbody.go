// Package httpbody turns the body of a failed HTTP response into something fit to show a person.
package httpbody

import "strings"

// Note is the body as one short line, or "" when it is a web page. A page (a firewall challenge, an
// error page, a login wall) is markup and scripts, never a message for the user: the status already
// says what happened, so a page is dropped, not quoted. max bounds the runes kept.
func Note(body []byte, max int) string {
	s := strings.TrimSpace(string(body))
	if IsPage(s) {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); max > 0 && len(r) > max {
		s = string(r[:max]) + "..."
	}
	return s
}

// IsPage reports whether text looks like HTML or XML rather than a plain message or JSON.
func IsPage(s string) bool {
	head := strings.ToLower(strings.TrimSpace(s))
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.HasPrefix(head, "<") || strings.Contains(head, "<html") || strings.Contains(head, "<!doctype")
}
