package agent

import (
	"fmt"
	"strings"
	"time"
)

// liveInterval is the least time between two live snapshots. A model writes dozens of
// fragments a second; a snapshot per fragment would flood the interface and the socket for no
// visible gain. A variable so a test can take every snapshot.
var liveInterval = 250 * time.Millisecond

// liveView turns a streamed completion into snapshots of what the model is saying.
type liveView struct {
	a       *Agent
	phase   string
	answer  strings.Builder
	thought strings.Builder
	last    time.Time
	sent    string
}

// add takes one fragment and reports a snapshot when enough time has passed.
func (l *liveView) add(fragment string, thinking bool) {
	if thinking {
		l.thought.WriteString(fragment)
	} else {
		l.answer.WriteString(fragment)
	}
	if time.Since(l.last) < liveInterval {
		return
	}
	view := liveText(l.phase, l.thought.String(), l.answer.String())
	if view == "" || view == l.sent {
		return
	}
	l.sent, l.last = view, time.Now()
	l.a.report("%s%s", LivePrefix, view)
}

// liveFields are, per phase, the JSON fields a person wants to read while the reply is being
// written, and what to put in front of each. The rest of the reply is structure.
var liveFields = map[string][]struct{ name, lead string }{
	"analyze":    {{"summary", ""}, {"reply", ""}, {"question", ""}},
	"plan":       {{"action", "- "}},
	"execute":    {{"reasoning", ""}, {"command", "$ "}},
	"synthesize": {{"summary", ""}},
}

// liveMaxRunes bounds one snapshot: the TAIL is kept, because it is what is being written.
const liveMaxRunes = 1500

// liveText is the readable part of a reply that is still being written.
//
// The model answers in JSON, so the text is read out of the fields it is filling in, even
// half-written. Reasoning tokens the provider streamed apart are shown while the answer has
// nothing readable yet, and prose - a model thinking out loud before its JSON - is shown as is.
func liveText(phase, thought, answer string) string {
	var parts []string
	for _, f := range liveFields[phase] {
		for _, v := range partialStrings(answer, f.name) {
			if v = strings.TrimSpace(v); v != "" {
				parts = append(parts, f.lead+v)
			}
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		trimmed := strings.TrimSpace(answer)
		if trimmed != "" && !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "`") {
			text = trimmed
		}
	}
	if text == "" {
		text = strings.TrimSpace(thought)
	}
	if r := []rune(text); len(r) > liveMaxRunes {
		text = "…" + string(r[len(r)-liveMaxRunes:])
	}
	return text
}

// partialStrings returns every value of the string field `name` in a JSON text that may be
// cut off anywhere: the last value is returned as far as it goes.
func partialStrings(text, name string) []string {
	var out []string
	key := `"` + name + `"`
	for i := 0; ; {
		at := strings.Index(text[i:], key)
		if at < 0 {
			return out
		}
		j := i + at + len(key)
		i = j
		j = skipJSONSpace(text, j)
		if j >= len(text) || text[j] != ':' {
			continue
		}
		j = skipJSONSpace(text, j+1)
		if j >= len(text) || text[j] != '"' {
			continue
		}
		value, end := decodePartial(text[j+1:])
		out = append(out, value)
		i = j + 1 + end
	}
}

// decodePartial decodes a JSON string body up to its closing quote or the end of the text,
// and returns the value and how many bytes it consumed. An escape cut off at the end stops the
// value there rather than guessing.
// skipJSONSpace is the first index at or after j that is not JSON whitespace.
func skipJSONSpace(text string, j int) int {
	for j < len(text) && strings.IndexByte(" \t\n\r", text[j]) >= 0 {
		j++
	}
	return j
}

func decodePartial(s string) (string, int) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			return b.String(), i + 1
		case c != '\\':
			b.WriteByte(c)
		case i+1 >= len(s):
			return b.String(), len(s)
		default:
			var ok bool
			if i, ok = decodeEscape(s, i+1, &b); !ok {
				return b.String(), len(s)
			}
		}
	}
	return b.String(), len(s)
}

// decodeEscape writes the character escaped at s[i], the one after a backslash, and returns the
// index of the last byte it consumed; false when the escape is cut short.
func decodeEscape(s string, i int, b *strings.Builder) (int, bool) {
	switch s[i] {
	case 'n':
		b.WriteByte('\n')
	case 't':
		b.WriteByte('\t')
	case 'r':
		// A carriage return is dropped.
	case 'u':
		return decodeUnicode(s, i, b)
	default:
		b.WriteByte(s[i])
	}
	return i, true
}

// decodeUnicode writes the \uXXXX escape whose u is at s[i].
func decodeUnicode(s string, i int, b *strings.Builder) (int, bool) {
	if i+4 >= len(s) {
		return i, false
	}
	var r rune
	if _, err := fmt.Sscanf(s[i+1:i+5], "%04x", &r); err == nil {
		b.WriteRune(r)
	}
	return i + 4, true
}
