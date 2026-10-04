// Package i18n is the language motita's interfaces speak: English or Spanish.
//
// The codebase is English-only, and so is every message it writes. A translation is a lookup
// keyed by that English text: T(ES, "Plan mode") returns the Spanish catalog's entry, and any
// text the catalog does not have comes back in English. A missing translation is therefore never
// a broken screen, only an English line among Spanish ones.
//
// The Spanish catalog lives in this package's es_*.go files, one per surface (es_tui.go,
// es_onboard.go, ...), each registering its entries from init. They are the only files where
// Spanish is expected, and scripts/verify.sh exempts exactly them.
package i18n

import (
	"fmt"
	"strings"
)

// Lang is a language an interface can be shown in.
type Lang string

// The languages motita speaks.
const (
	EN Lang = "en"
	ES Lang = "es"
)

// Auto is the setting that follows the environment: the terminal's locale, the browser's
// language. It is the default.
const Auto = "auto"

// Valid reports whether setting is a value ui.language accepts: auto, en or es (or empty, which
// means auto).
func Valid(setting string) bool {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case "", Auto, string(EN), string(ES):
		return true
	}
	return false
}

// Resolve turns the ui.language setting into a language. en and es are taken as they are;
// auto (or empty, or anything else) looks at the locale variables in the order the C library
// does - LC_ALL, then LC_MESSAGES, then LANG - and picks Spanish when the first one that is set
// names it (es, es_CL.UTF-8, ...). Everything else is English.
func Resolve(setting string, getenv func(string) string) Lang {
	switch Lang(strings.ToLower(strings.TrimSpace(setting))) {
	case EN:
		return EN
	case ES:
		return ES
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			continue
		}
		if l := strings.ToLower(v); l == "es" || strings.HasPrefix(l, "es_") || strings.HasPrefix(l, "es-") || strings.HasPrefix(l, "es.") {
			return ES
		}
		return EN
	}
	return EN
}

// catalogs holds every translation, by language, keyed by the English text.
var catalogs = map[Lang]map[string]string{}

// Register adds entries to lang's catalog. It is called from the init of each es_*.go file; a key
// registered twice keeps the last value.
func Register(lang Lang, entries map[string]string) {
	if catalogs[lang] == nil {
		catalogs[lang] = map[string]string{}
	}
	for k, v := range entries {
		catalogs[lang][k] = v
	}
}

// T returns text in lang: the catalog's entry when there is one, the English text otherwise.
func T(lang Lang, text string) string {
	if lang == EN {
		return text
	}
	if v, ok := catalogs[lang][text]; ok && v != "" {
		return v
	}
	return text
}

// Tf is T for a format string: the format is translated first, then filled in, so the catalog is
// keyed by the format with its verbs ("%d actions"), never by one rendered instance of it.
func Tf(lang Lang, format string, args ...any) string {
	return fmt.Sprintf(T(lang, format), args...)
}
