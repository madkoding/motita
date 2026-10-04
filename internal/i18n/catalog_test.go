package i18n

import (
	"regexp"
	"strings"
	"testing"
)

// verb matches one fmt verb, flags and width included, and the literal %%.
var verb = regexp.MustCompile(`%[-+# 0]*[0-9]*(\.[0-9]+)?[a-zA-Z%]`)

// TestEveryTranslationKeepsItsVerbs: a translation is filled with the same arguments as the English
// format, so it must carry the same verbs in the same order - a %s turned into a %d, or two verbs
// swapped, would print the wrong value in the wrong place, and only in Spanish. The layout that
// code relies on is checked too: a prompt's "[1" (the keyboard rewrites it) and a trailing newline.
func TestEveryTranslationKeepsItsVerbs(t *testing.T) {
	// Both surfaces register from init: the interface's catalog is the larger one, so a count that
	// only the two together reach fails if either stops registering, instead of passing by accident.
	if len(catalogs[ES]) < 300 {
		t.Fatalf("the Spanish catalog has %d entries; a surface's catalog did not register", len(catalogs[ES]))
	}
	for key, value := range catalogs[ES] {
		if strings.HasSuffix(key, " test") {
			continue // the entries the package's own tests register
		}
		if got, want := strings.Join(verb.FindAllString(value, -1), " "), strings.Join(verb.FindAllString(key, -1), " "); got != want {
			t.Errorf("%q -> %q: verbs %q, want %q", key, value, got, want)
		}
		if strings.Contains(key, "[1") && !strings.Contains(value, "[1") {
			t.Errorf("%q -> %q: the prompt lost its [1", key, value)
		}
		if strings.HasSuffix(key, "\n") != strings.HasSuffix(value, "\n") {
			t.Errorf("%q -> %q: the line ending differs", key, value)
		}
		if value == "" {
			t.Errorf("%q has an empty translation", key)
		}
	}
}
