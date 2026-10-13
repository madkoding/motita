package voice

import (
	"strings"
	"unicode"
)

// Wake is a wake phrase found in a transcript.
type Wake struct {
	// Phrase is the configured phrase that matched, as it was written in the configuration.
	Phrase string `json:"phrase"`
	// Rest is what was said after it, in the transcript's own spelling.
	Rest string `json:"rest"`
}

// maxLead is how many words may come before the phrase: speech recognisers hear a breath as
// "eh" or "ok", and a phrase that only matched as the very first word would miss those.
const maxLead = 2

// minFuzzy is the shortest word that may differ by one letter and still match. Shorter words
// are compared exactly: one letter is the whole difference between two short words.
const minFuzzy = 4

// token is one word of a transcript: its folded form and where it ends in the original text.
type token struct {
	word string
	end  int
}

// folds maps the accented Latin letters a recogniser writes to the plain ones a person types,
// so "motita" in the configuration matches whatever accent the transcript put on it. Written
// as escapes so the source stays ASCII.
var folds = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u',
	'ñ': 'n', 'ç': 'c',
}

// fold lower-cases a rune and strips its accent.
func fold(r rune) rune {
	r = unicode.ToLower(r)
	if f, ok := folds[r]; ok {
		return f
	}
	return r
}

// tokenize splits text into folded words. Anything that is not a letter or a digit separates
// words, so punctuation and spacing never decide a match.
func tokenize(text string) []token {
	var out []token
	var cur strings.Builder
	flush := func(end int) {
		if cur.Len() > 0 {
			out = append(out, token{word: cur.String(), end: end})
			cur.Reset()
		}
	}
	for i, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(fold(r))
			continue
		}
		flush(i)
	}
	flush(len(text))
	return out
}

// Match finds the first configured phrase at the start of a transcript (after at most maxLead
// words) and returns it with what followed. When two phrases match at the same place, the
// longer one wins, so "motita new task" is preferred to "motita" for the same speech.
func Match(text string, phrases []string) (Wake, bool) {
	words := tokenize(text)
	best, bestStart, bestLen := -1, 0, 0
	for i, phrase := range phrases {
		want := tokenize(phrase)
		if len(want) == 0 {
			continue
		}
		start, ok := find(words, want)
		if !ok {
			continue
		}
		if best < 0 || start < bestStart || (start == bestStart && len(want) > bestLen) {
			best, bestStart, bestLen = i, start, len(want)
		}
	}
	if best < 0 {
		return Wake{}, false
	}
	end := words[bestStart+bestLen-1].end
	rest := strings.TrimLeftFunc(text[end:], func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return Wake{Phrase: phrases[best], Rest: strings.TrimSpace(rest)}, true
}

// find returns where want starts in words, looking no further than maxLead words in.
func find(words, want []token) (int, bool) {
	for start := 0; start <= maxLead && start+len(want) <= len(words); start++ {
		if sameWords(words[start:start+len(want)], want) {
			return start, true
		}
	}
	return 0, false
}

// sameWords reports whether two runs of words match one by one.
func sameWords(got, want []token) bool {
	for i := range want {
		if !near(got[i].word, want[i].word) {
			return false
		}
	}
	return true
}

// near reports whether a heard word is the configured one: equal, or, for a word of minFuzzy
// letters or more, one edit away ("mutita" for "motita").
func near(heard, want string) bool {
	if heard == want {
		return true
	}
	if len([]rune(want)) < minFuzzy {
		return false
	}
	return distance([]rune(heard), []rune(want)) <= 1
}

// distance is the Levenshtein distance between two words.
func distance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
