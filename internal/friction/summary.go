package friction

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/madkoding/motita/internal/i18n"
)

// WeekDays is the span of the weekly summary.
const WeekDays = 7

// maxRepeated and maxFlagged cap the two lists of a summary: it is read in a glance.
const (
	maxRepeated = 5
	maxFlagged  = 5
)

// Repeated is something that got in the way more than once, with how many times.
type Repeated struct {
	Kind  string `json:"kind"`
	Why   string `json:"why,omitempty"`
	Text  string `json:"text"`
	Count int    `json:"count"`
}

// Summary is the journal over the last days, as the settings page draws it.
type Summary struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	Days  int       `json:"days"`
	Total int       `json:"total"`
	// Counts is how many entries of each kind: every kind is there, a quiet one as 0.
	Counts map[string]int `json:"counts"`
	// Repeated are the things that came up more than once, most frequent first.
	Repeated []Repeated `json:"repeated"`
	// Flagged are the answers the person marked as not good, newest first, with their own words.
	Flagged []Entry `json:"flagged"`
}

// Summarize reads the entries of the last days up to now.
func Summarize(entries []Entry, now time.Time, days int) Summary {
	s := Summary{
		Since: now.AddDate(0, 0, -days), Until: now, Days: days,
		Counts: map[string]int{}, Repeated: []Repeated{}, Flagged: []Entry{},
	}
	for _, k := range Kinds {
		s.Counts[k] = 0
	}
	groups := map[string]*Repeated{}
	var order []string
	for _, e := range entries {
		if e.At.Before(s.Since) || e.At.After(now) {
			continue
		}
		s.Total++
		s.Counts[e.Kind]++
		if e.Kind == KindFlagged {
			s.Flagged = append(s.Flagged, e)
			continue
		}
		key := e.Kind + "\x00" + e.Why + "\x00" + shape(e.Text)
		if g := groups[key]; g != nil {
			g.Count++
			continue
		}
		groups[key] = &Repeated{Kind: e.Kind, Why: e.Why, Text: e.Text, Count: 1}
		order = append(order, key)
	}
	for _, key := range order {
		if g := groups[key]; g.Count > 1 {
			s.Repeated = append(s.Repeated, *g)
		}
	}
	// Stable, so two things that happened as often keep the order they first came in.
	sort.SliceStable(s.Repeated, func(a, b int) bool { return s.Repeated[a].Count > s.Repeated[b].Count })
	if len(s.Repeated) > maxRepeated {
		s.Repeated = s.Repeated[:maxRepeated]
	}
	sort.SliceStable(s.Flagged, func(a, b int) bool { return s.Flagged[a].At.After(s.Flagged[b].At) })
	if len(s.Flagged) > maxFlagged {
		s.Flagged = s.Flagged[:maxFlagged]
	}
	return s
}

// shape is a text with what changes between two occurrences of the same thing taken out: case
// and digits (a line number, a duration, an attempt count), so "attempt 2/3" and "attempt 3/3"
// are one repeated step.
func shape(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// Text is the summary in plain words, in lang.
func (s Summary) Text(lang i18n.Lang) string {
	if s.Total == 0 {
		return i18n.Tf(lang, "Nothing got in the way in the last %d days.", s.Days)
	}
	var b strings.Builder
	b.WriteString(i18n.Tf(lang, "In the last %d days, this got in the way (%d in all):", s.Days, s.Total))
	b.WriteByte('\n')
	for _, k := range Kinds {
		if n := s.Counts[k]; n > 0 {
			fmt.Fprintf(&b, "- %s\n", countLine(lang, k, n))
		}
	}
	if len(s.Repeated) > 0 {
		b.WriteString("\n" + i18n.T(lang, "Came up more than once:") + "\n")
		for _, r := range s.Repeated {
			fmt.Fprintf(&b, "- %dx %s: %s\n", r.Count, i18n.T(lang, kindWord(r.Kind)), repeatedText(lang, r))
		}
	}
	if len(s.Flagged) > 0 {
		b.WriteString("\n" + i18n.T(lang, "Answers you marked as not good:") + "\n")
		for _, f := range s.Flagged {
			fmt.Fprintf(&b, "- %s\n", flaggedLine(lang, f))
		}
	}
	return b.String()
}

// kindWord names a kind of the repeated list in the singular, as the English text the catalog
// translates. A flagged answer is never in that list: it has a list of its own.
func kindWord(kind string) string {
	switch kind {
	case KindError:
		return "failure"
	case KindRetry:
		return "retry"
	default:
		return "refused action"
	}
}

// repeatedText is what a repeated entry was. A refusal says who refused, in words; the rest is
// the text the agent itself wrote.
func repeatedText(lang i18n.Lang, r Repeated) string {
	if r.Kind != KindDenied {
		return r.Text
	}
	if r.Why == WhyNobody {
		return i18n.Tf(lang, "%s (refused: nobody was there to ask)", r.Text)
	}
	return i18n.Tf(lang, "%s (you refused it)", r.Text)
}

// countLine is "Failed runs: 5" and the like: the count comes last, so the line reads right for 1
// and for 5 in every language.
func countLine(lang i18n.Lang, kind string, n int) string {
	switch kind {
	case KindError:
		return i18n.Tf(lang, "Runs that ended with a failure: %d", n)
	case KindRetry:
		return i18n.Tf(lang, "Steps that had to be tried again: %d", n)
	case KindDenied:
		return i18n.Tf(lang, "Actions that were refused: %d", n)
	default:
		return i18n.Tf(lang, "Answers marked as not good: %d", n)
	}
}

// flaggedLine is what the person said about an answer, and what it was an answer to.
func flaggedLine(lang i18n.Lang, e Entry) string {
	said := e.Note
	if said == "" {
		said = i18n.T(lang, "(no comment)")
	}
	if e.Ask == "" {
		return said
	}
	return i18n.Tf(lang, "%s — about: %s", said, e.Ask)
}
