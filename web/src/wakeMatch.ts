// matchWake finds a wake phrase at the start of a text heard in this browser, so hands-free mode
// with a local model never sends what was said anywhere: the phrases come with GET /v1/voice
// (wake_phrases), and this is the gateway's own matching (internal/voice/match.go), word for word.
//
// Keep the two in step: a change to how the gateway matches is a change here too.

// MAX_LEAD is how many words may come before the phrase: a recogniser hears a breath as "eh" or
// "ok", and a phrase that only matched as the very first word would miss those.
const MAX_LEAD = 2;

// MIN_FUZZY is the shortest word that may differ by one letter and still match. Shorter words are
// compared exactly: one letter is the whole difference between two short words.
const MIN_FUZZY = 4;

// FOLDS maps the accented Latin letters a recogniser writes to the plain ones a person types.
const FOLDS: Record<string, string> = {
  à: "a", á: "a", â: "a", ã: "a", ä: "a",
  è: "e", é: "e", ê: "e", ë: "e",
  ì: "i", í: "i", î: "i", ï: "i",
  ò: "o", ó: "o", ô: "o", õ: "o", ö: "o",
  ù: "u", ú: "u", û: "u", ü: "u",
  ñ: "n", ç: "c",
};

// A letter or a digit (Go's unicode.IsLetter and unicode.IsDigit); anything else separates words.
const WORD_CHAR = /[\p{L}\p{Nd}]/u;

// A word of a text: its folded form, and where it ends in the original.
interface Token {
  word: string;
  end: number;
}

// fold lower-cases a character and strips its accent.
function fold(ch: string): string {
  const lower = ch.toLowerCase();
  return FOLDS[lower] ?? lower;
}

// tokenize splits text into folded words.
function tokenize(text: string): Token[] {
  const out: Token[] = [];
  let cur = "";
  let at = 0;
  const flush = (end: number) => {
    if (cur) {
      out.push({ word: cur, end });
      cur = "";
    }
  };
  for (const ch of text) {
    if (WORD_CHAR.test(ch)) {
      cur += fold(ch);
    } else {
      flush(at);
    }
    at += ch.length;
  }
  flush(text.length);
  return out;
}

// distance is the Levenshtein distance between two words, letter by letter.
function distance(a: string[], b: string[]): number {
  let prev = Array.from({ length: b.length + 1 }, (_, j) => j);
  for (let i = 1; i <= a.length; i++) {
    const cur = [i];
    for (let j = 1; j <= b.length; j++) {
      const cost = Number(a[i - 1] !== b[j - 1]);
      cur[j] = Math.min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost);
    }
    prev = cur;
  }
  return prev[b.length];
}

// near says whether a heard word is the configured one: equal, or, for a word of MIN_FUZZY letters
// or more, one edit away ("mutita" for "motita").
function near(heard: string, want: string): boolean {
  if (heard === want) {
    return true;
  }
  const w = [...want];
  return w.length >= MIN_FUZZY && distance([...heard], w) <= 1;
}

// find is where want starts in words, looking no further than MAX_LEAD words in; -1 when nowhere.
function find(words: Token[], want: Token[]): number {
  for (let start = 0; start <= MAX_LEAD && start + want.length <= words.length; start++) {
    if (want.every((t, i) => near(words[start + i].word, t.word))) {
      return start;
    }
  }
  return -1;
}

// matchWake finds the first phrase at the start of text (after at most MAX_LEAD words) and returns
// it with what followed; undefined when none is there. When two phrases match at the same place,
// the longer one wins.
export function matchWake(text: string, phrases: readonly string[]): { phrase: string; rest: string } | undefined {
  const words = tokenize(text);
  let best = -1;
  let bestStart = 0;
  let bestLen = 0;
  phrases.forEach((phrase, i) => {
    const want = tokenize(phrase);
    // An empty phrase never matches.
    const start = want.length ? find(words, want) : -1;
    if (start < 0) {
      return;
    }
    if (best < 0 || start < bestStart || (start === bestStart && want.length > bestLen)) {
      best = i;
      bestStart = start;
      bestLen = want.length;
    }
  });
  if (best < 0) {
    return undefined;
  }
  const end = words[bestStart + bestLen - 1].end;
  let from = end;
  for (const ch of text.slice(end)) {
    if (WORD_CHAR.test(ch)) {
      break;
    }
    from += ch.length;
  }
  return { phrase: phrases[best], rest: text.slice(from).trim() };
}
