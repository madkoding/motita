package i18n

import "testing"

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestResolveTakesAnExplicitSetting(t *testing.T) {
	spanish := env(map[string]string{"LANG": "es_CL.UTF-8"})
	if Resolve("en", spanish) != EN || Resolve(" ES ", env(nil)) != ES {
		t.Fatal("an explicit en or es must win over the environment")
	}
}

func TestResolveAutoFollowsTheLocaleInTheCLibrarysOrder(t *testing.T) {
	cases := []struct {
		vars map[string]string
		want Lang
	}{
		{nil, EN},
		{map[string]string{"LANG": "es_CL.UTF-8"}, ES},
		{map[string]string{"LANG": "es"}, ES},
		{map[string]string{"LANG": "es-ES"}, ES},
		{map[string]string{"LANG": "es.UTF-8"}, ES},
		{map[string]string{"LANG": "en_US.UTF-8"}, EN},
		{map[string]string{"LANG": "estonian"}, EN}, // a prefix of letters is not a locale
		{map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "es_ES.UTF-8"}, EN},
		{map[string]string{"LC_MESSAGES": "es_MX.UTF-8", "LANG": "en_US.UTF-8"}, ES},
		{map[string]string{"LC_ALL": "  ", "LANG": "es_AR.UTF-8"}, ES}, // blank is unset
	}
	for _, c := range cases {
		for _, setting := range []string{"", "auto", "something-else"} {
			if got := Resolve(setting, env(c.vars)); got != c.want {
				t.Errorf("Resolve(%q, %v) = %s, want %s", setting, c.vars, got, c.want)
			}
		}
	}
}

func TestValid(t *testing.T) {
	for _, ok := range []string{"", "auto", "en", "ES", " es "} {
		if !Valid(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	if Valid("fr") {
		t.Error("fr is not a language motita speaks")
	}
}

func TestTFallsBackToEnglish(t *testing.T) {
	Register(ES, map[string]string{"Hello test": "Hola test", "Blank test": ""}) // spanish-fixture: a catalog entry
	Register(ES, map[string]string{"Hello test": "Hola de nuevo"})               // spanish-fixture: the last registration wins
	if got := T(ES, "Hello test"); got != "Hola de nuevo" {                      // spanish-fixture: a catalog entry
		t.Errorf("T = %q", got)
	}
	if got := T(EN, "Hello test"); got != "Hello test" {
		t.Errorf("English must be the text itself: %q", got)
	}
	if got := T(ES, "Not in the catalog"); got != "Not in the catalog" {
		t.Errorf("a missing entry must come back in English: %q", got)
	}
	if got := T(ES, "Blank test"); got != "Blank test" {
		t.Errorf("an empty entry is a missing one: %q", got)
	}
	if got := T(Lang("fr"), "Hello test"); got != "Hello test" {
		t.Errorf("a language with no catalog is English: %q", got)
	}
}

func TestTfTranslatesTheFormatThenFillsIt(t *testing.T) {
	Register(ES, map[string]string{"%d actions test": "%d acciones test"}) // spanish-fixture: a catalog entry
	if got := Tf(ES, "%d actions test", 3); got != "3 acciones test" {     // spanish-fixture: a catalog entry
		t.Errorf("Tf = %q", got)
	}
	if got := Tf(EN, "%d actions test", 3); got != "3 actions test" {
		t.Errorf("Tf = %q", got)
	}
}
