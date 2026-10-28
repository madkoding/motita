package memory

import (
	"strings"
	"unicode"
)

// terms are the words of s as the index knows them: lower case, accents folded (so a word with
// and without its accents is one word), the little words of Spanish and English left out, a plural "s" cut.
func terms(s string) []string {
	words := strings.FieldsFunc(strings.Map(fold, strings.ToLower(s)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(words))
	for _, w := range words {
		if len([]rune(w)) < 2 || stopwords[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = w[:len(w)-1]
		}
		out = append(out, w)
	}
	return out
}

// fold maps the accented letters of Spanish to their plain ones.
func fold(r rune) rune {
	switch r {
	case '\u00e1', '\u00e0', '\u00e4', '\u00e2':
		return 'a'
	case '\u00e9', '\u00e8', '\u00eb', '\u00ea':
		return 'e'
	case '\u00ed', '\u00ec', '\u00ef', '\u00ee':
		return 'i'
	case '\u00f3', '\u00f2', '\u00f6', '\u00f4':
		return 'o'
	case '\u00fa', '\u00f9', '\u00fc', '\u00fb':
		return 'u'
	case '\u00f1':
		return 'n'
	default:
		return r
	}
}

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	english := "the an of and to in is it for on with as at by or be are was this that from"
	spanish := "de la el los las un una unos unas y o en que por para con se del al es lo su sus como mas pero sin sobre" // spanish-fixture: stopwords of the second language of the memory
	for _, w := range strings.Fields(english + " " + spanish) {
		m[w] = true
	}
	return m
}()
