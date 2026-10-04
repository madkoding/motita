package i18n

import (
	"reflect"
	"regexp"
	"testing"
)

// verb matches one fmt verb, flags and width included; %% is a literal percent and is matched too,
// so a translation that drops one is caught as well.
var verb = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)

// TestTheTUICatalogKeepsTheVerbs: Tf translates the format and then fills it, so a translation
// with a verb missing, added or moved would print "%!v(MISSING)" or put an argument in the wrong
// place. Every entry must carry the same verbs, in the same order, as its English key.
func TestTheTUICatalogKeepsTheVerbs(t *testing.T) {
	if len(catalogs[ES]) < 100 {
		t.Fatalf("the Spanish catalog has %d entries; the interface's catalog did not register", len(catalogs[ES]))
	}
	for en, es := range catalogs[ES] {
		if got, want := verb.FindAllString(es, -1), verb.FindAllString(en, -1); !reflect.DeepEqual(got, want) {
			t.Errorf("%q -> %q: verbs %v, want %v", en, es, got, want)
		}
	}
}
