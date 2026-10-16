// The text a local voice is given: Piper reads exactly what it is handed, and stumbles on what a
// person would read at a glance, so the sentence is prepared first. Abbreviations are written
// out (Sr. is Señor), numbers become words (25 is veinticinco), and the punctuation that sets the
// rhythm is made explicit: a comma is a short pause, a semicolon a medium one, a full stop a long
// one, and an ellipsis a dramatic one.
//
// Pure and free of imports, so the tests run it as it is. Spanish and English are prepared in
// full; any other language gets the punctuation only.

type Words = "es" | "en";

// baseOf is the language a text is prepared for: es and en, whatever the region (es-MX), and
// null for the rest.
function baseOf(lang: string): Words | null {
  const base = lang.toLowerCase().slice(0, 2);
  if (base === "es" || base === "en") {
    return base;
  }
  return null;
}

// ABBREVIATIONS are written out. A key is how the abbreviation is typed, in lower case and with no
// space inside (p.ej. is also found as "p. ej."); its capital is kept in the word (Dr. is Doctor,
// dr. is doctor). "more" marks those that may end a sentence, which keep their full stop.
interface Abbreviation {
  said: string;
  more?: boolean;
}
const ABBREVIATIONS: Record<Words, Record<string, Abbreviation>> = {
  es: {
    "sr.": { said: "señor" },
    "sra.": { said: "señora" },
    "srta.": { said: "señorita" },
    "sres.": { said: "señores" },
    "dr.": { said: "doctor" },
    "dra.": { said: "doctora" },
    "lic.": { said: "licenciado" },
    "ing.": { said: "ingeniero" },
    "arq.": { said: "arquitecto" },
    "prof.": { said: "profesor" },
    "profa.": { said: "profesora" },
    "gral.": { said: "general" },
    "av.": { said: "avenida" },
    "ud.": { said: "usted" },
    "uds.": { said: "ustedes" },
    "pág.": { said: "página" },
    "págs.": { said: "páginas" },
    "núm.": { said: "número" },
    "tel.": { said: "teléfono" },
    "aprox.": { said: "aproximadamente" },
    "vs.": { said: "versus" },
    "etc.": { said: "etcétera", more: true },
    "p.ej.": { said: "por ejemplo" },
    "ee.uu.": { said: "Estados Unidos", more: true },
  },
  en: {
    "mr.": { said: "mister" },
    "mrs.": { said: "missus" },
    "ms.": { said: "miss" },
    "dr.": { said: "doctor" },
    "prof.": { said: "professor" },
    "jr.": { said: "junior" },
    "sr.": { said: "senior" },
    "mt.": { said: "mount" },
    "fig.": { said: "figure" },
    "approx.": { said: "approximately" },
    "vs.": { said: "versus" },
    "etc.": { said: "et cetera", more: true },
    "e.g.": { said: "for example" },
    "i.e.": { said: "that is" },
    "a.m.": { said: "A M", more: true },
    "p.m.": { said: "P M", more: true },
    "inc.": { said: "incorporated", more: true },
    "ltd.": { said: "limited", more: true },
    "u.s.": { said: "U S", more: true },
  },
};

// escapeRegExp makes a literal of text for a pattern.
function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, String.raw`\$&`);
}

// abbreviationPattern finds any abbreviation of a language where it stands alone, not inside a
// word. Longest first, so "págs." is not "pág."; a space may follow the dots inside one.
function abbreviationPattern(lang: Words): RegExp {
  const keys = Object.keys(ABBREVIATIONS[lang])
    .sort((a, b) => b.length - a.length)
    .map((k) => escapeRegExp(k).replace(/\\\.(?=.)/g, String.raw`\.\s?`));
  return new RegExp(
    String.raw`(?<![\p{L}\p{N}])(?:${keys.join("|")})(?![\p{L}\p{N}])`,
    "giu",
  );
}
const ABBREVIATION_PATTERNS: Record<Words, RegExp> = {
  es: abbreviationPattern("es"),
  en: abbreviationPattern("en"),
};

// writeAbbreviations writes out every abbreviation of a text. A word keeps the capital of the
// abbreviation it replaces; one that may end a sentence keeps its full stop when a new sentence
// follows.
function writeAbbreviations(text: string, lang: Words): string {
  const table = ABBREVIATIONS[lang];
  return text.replace(ABBREVIATION_PATTERNS[lang], (found, at: number) => {
    const entry = table[found.toLowerCase().replace(/\s+/g, "")];
    const capital = /^\p{Lu}/u.test(found);
    let word = entry.said;
    if (capital) {
      word = word.charAt(0).toUpperCase() + word.slice(1);
    }
    if (entry.more && /^\s*(?:\p{Lu}|$)/u.test(text.slice(at + found.length))) {
      word += ".";
    }
    return word;
  });
}

// The numbers, as words.
const ES_UNITS = [
  "cero",
  "uno",
  "dos",
  "tres",
  "cuatro",
  "cinco",
  "seis",
  "siete",
  "ocho",
  "nueve",
  "diez",
  "once",
  "doce",
  "trece",
  "catorce",
  "quince",
  "dieciséis",
  "diecisiete",
  "dieciocho",
  "diecinueve",
  "veinte",
  "veintiuno",
  "veintidós",
  "veintitrés",
  "veinticuatro",
  "veinticinco",
  "veintiséis",
  "veintisiete",
  "veintiocho",
  "veintinueve",
];
const ES_TENS = [
  "",
  "",
  "",
  "treinta",
  "cuarenta",
  "cincuenta",
  "sesenta",
  "setenta",
  "ochenta",
  "noventa",
];
const ES_HUNDREDS = [
  "",
  "ciento",
  "doscientos",
  "trescientos",
  "cuatrocientos",
  "quinientos",
  "seiscientos",
  "setecientos",
  "ochocientos",
  "novecientos",
];
const EN_UNITS = [
  "zero",
  "one",
  "two",
  "three",
  "four",
  "five",
  "six",
  "seven",
  "eight",
  "nine",
  "ten",
  "eleven",
  "twelve",
  "thirteen",
  "fourteen",
  "fifteen",
  "sixteen",
  "seventeen",
  "eighteen",
  "nineteen",
];
const EN_TENS = [
  "",
  "",
  "twenty",
  "thirty",
  "forty",
  "fifty",
  "sixty",
  "seventy",
  "eighty",
  "ninety",
];
const EN_SCALES = ["", "thousand", "million", "billion"];

// The most digits a number is read as one: up to 999 999 999 999. A longer one, or one that starts
// with a zero, is read digit by digit.
const MAX_DIGITS = 12;

// ES_TENS_FROM is where Spanish stops having one word per number (veintinueve, then treinta).
const ES_TENS_FROM = 30;

// esBelowHundred is 0 to 99 in Spanish, as the words that say it (none for 0).
function esBelowHundred(n: number): string[] {
  if (n === 0) {
    return [];
  }
  if (n < ES_TENS_FROM) {
    return [ES_UNITS[n]];
  }
  const words = [ES_TENS[Math.floor(n / 10)]];
  if (n % 10 > 0) {
    words.push("y", ES_UNITS[n % 10]);
  }
  return words;
}

// esBelowThousand is 1 to 999 in Spanish. before says it stands before a noun or mil, where the
// one is "un" (veintiún, treinta y un).
function esBelowThousand(n: number, before: boolean): string {
  const words: string[] = [];
  const hundreds = Math.floor(n / 100);
  const rest = n % 100;
  if (hundreds === 1 && rest === 0) {
    return "cien";
  }
  if (hundreds > 0) {
    words.push(ES_HUNDREDS[hundreds]);
  }
  words.push(...esBelowHundred(rest));
  const said = words.join(" ");
  if (!before) {
    return said;
  }
  return said.replace(/uno$/, "un").replace(/veintiun$/, "veintiún");
}

// esCardinal is a whole number below a million million, in Spanish.
function esCardinal(n: number, before: boolean): string {
  if (n === 0) {
    return ES_UNITS[0];
  }
  const millions = Math.floor(n / 1e6);
  const rest = n % 1e6;
  const words: string[] = [];
  if (millions > 0) {
    words.push(
      ...choose(
        millions === 1,
        ["un millón"],
        [esThousands(millions, true), "millones"],
      ),
    );
  }
  if (rest > 0) {
    words.push(esThousands(rest, before));
  }
  return words.join(" ");
}

// esThousands is 1 to 999 999 in Spanish.
function esThousands(n: number, before: boolean): string {
  const thousands = Math.floor(n / 1000);
  const rest = n % 1000;
  const words: string[] = [];
  if (thousands > 0) {
    words.push(
      ...choose(
        thousands === 1,
        ["mil"],
        [esBelowThousand(thousands, true), "mil"],
      ),
    );
  }
  if (rest > 0) {
    words.push(esBelowThousand(rest, before));
  }
  return words.join(" ");
}

// EN_TENS_FROM is where English stops having one word per number (nineteen, then twenty).
const EN_TENS_FROM = 20;

// enBelowHundred is 1 to 99 in English.
function enBelowHundred(n: number): string {
  if (n < EN_TENS_FROM) {
    return EN_UNITS[n];
  }
  const unit = n % 10;
  return (
    EN_TENS[Math.floor(n / 10)] + choose(unit > 0, "-" + EN_UNITS[unit], "")
  );
}

// enBelowThousand is 1 to 999 in English.
function enBelowThousand(n: number): string {
  const words: string[] = [];
  const hundreds = Math.floor(n / 100);
  const rest = n % 100;
  if (hundreds > 0) {
    words.push(EN_UNITS[hundreds], "hundred");
  }
  if (rest > 0) {
    words.push(enBelowHundred(rest));
  }
  return words.join(" ");
}

// enCardinal is a whole number below a million million, in English.
function enCardinal(n: number): string {
  if (n === 0) {
    return EN_UNITS[0];
  }
  const words: string[] = [];
  let scale = 0;
  for (let left = n; left > 0; left = Math.floor(left / 1000)) {
    const group = left % 1000;
    if (group > 0) {
      words.unshift(
        enBelowThousand(group) +
          choose(!!EN_SCALES[scale], " " + EN_SCALES[scale], ""),
      );
    }
    scale++;
  }
  return words.join(" ");
}

// digitWords reads digits one at a time: how a decimal part, a long number or one with leading
// zeros is said.
function digitWords(digits: string, lang: Words): string {
  const units = choose(lang === "es", ES_UNITS, EN_UNITS);
  return [...digits].map((d) => units[Number(d)]).join(" ");
}

// numberWords is a whole number, as a string of digits, in words in a language; before says it
// stands before a noun, where a one is "un". A number too long to be read as one, or written with
// leading zeros, is read digit by digit.
export function numberWords(
  digits: string,
  lang: Words,
  before = false,
): string {
  if (
    digits.length > MAX_DIGITS ||
    (digits.length > 1 && digits.startsWith("0"))
  ) {
    return digitWords(digits, lang);
  }
  const n = Number(digits);
  if (lang === "es") {
    return esCardinal(n, before);
  }
  return enCardinal(n);
}

// splitNumber reads a written number: its whole part and its decimals, with the marks in their
// language's use (1.234,5 in Spanish, 1,234.5 in English). Null when it is not a number (a version
// such as 1.2.3).
function splitNumber(
  text: string,
  lang: Words,
): { whole: string; decimals: string } | null {
  const thousands = choose(lang === "es", ".", ",");
  const marks = text.match(/[.,]/g) ?? [];
  const groups = text.split(/[.,]/);
  const last = marks[marks.length - 1];
  const head = groups[0];
  // Thousands are groups of three digits after one of at most three, with no zero in front.
  const grouped = (rest: string[]) =>
    rest.every((g) => g.length === GROUP_DIGITS) &&
    head.length <= GROUP_DIGITS &&
    !head.startsWith("0");
  if (marks.length === 0) {
    return { whole: text, decimals: "" };
  }
  if (marks.length === 1) {
    if (last === thousands && grouped(groups.slice(1))) {
      return { whole: groups.join(""), decimals: "" };
    }
    return { whole: head, decimals: groups[1] };
  }
  // Several marks: thousands all through, or thousands and a last decimal mark.
  const before = marks.slice(0, -1).every((m) => m === thousands);
  if (last === thousands) {
    return choose(
      before && grouped(groups.slice(1)),
      { whole: groups.join(""), decimals: "" },
      null,
    );
  }
  return choose(
    before && grouped(groups.slice(1, -1)),
    {
      whole: groups.slice(0, -1).join(""),
      decimals: groups[groups.length - 1],
    },
    null,
  );
}

// choose is a when b, c otherwise.
function choose<T>(a: boolean, b: T, c: T): T {
  if (a) {
    return b;
  }
  return c;
}

// ONE_STAYS are the words after which a one is "uno", not "un": the number is not counting what
// follows (el 1 de mayo, 21 y 31).
const ONE_STAYS = new Set(
  "de del y e o u a al en con por para sin sobre hasta desde entre es son fue era se que ni pero".split(
    " ",
  ),
);

// GROUP_DIGITS is the digits in a group of thousands (1,000 or 1.000).
const GROUP_DIGITS = 3;

// NUMBER is a number as typed, with an optional minus and percent sign.
const NUMBER = /(?<![\p{L}\p{N}.,])(-)?(\d+(?:[.,]\d+)*)(%)?(?!\d)/gu;

// ORDINALS are the English ordinals that are not the cardinal and "th".
const ORDINALS: Record<string, string> = {
  one: "first",
  two: "second",
  three: "third",
  five: "fifth",
  eight: "eighth",
  nine: "ninth",
  twelve: "twelfth",
};

// ordinal is an English cardinal as an ordinal: its last word changes (twenty-one is twenty-first).
function ordinal(cardinal: string): string {
  const cut =
    Math.max(cardinal.lastIndexOf(" "), cardinal.lastIndexOf("-")) + 1;
  const last = cardinal.slice(cut);
  const said =
    ORDINALS[last] ??
    choose(last.endsWith("y"), last.slice(0, -1) + "ieth", last + "th");
  return cardinal.slice(0, cut) + said;
}

// writeOrdinals writes English ordinals (3rd, 21st) in words.
function writeOrdinals(text: string): string {
  return text.replace(
    /(?<![\p{L}\p{N}.,])(\d{1,9})(?:st|nd|rd|th)\b/giu,
    (_, digits: string) => ordinal(numberWords(digits, "en")),
  );
}

// writeTimes writes a time such as 10:30 as its two numbers, and 10:00 as the hour alone.
function writeTimes(text: string): string {
  return text
    .replace(/(?<![\p{L}\p{N}:])(\d{1,2}):00(?![\p{L}\p{N}:])/gu, "$1")
    .replace(/(?<![\p{L}\p{N}:])(\d{1,2}):(\d{2})(?![\p{L}\p{N}:])/gu, "$1 $2");
}

// writeNumbers writes every number of a text in words, with its decimals and percent sign.
function writeNumbers(text: string, lang: Words): string {
  return text.replace(
    NUMBER,
    (
      found,
      minus: string | undefined,
      digits: string,
      percent: string | undefined,
      at: number,
    ) => {
      const parts = splitNumber(digits, lang);
      if (!parts) {
        return found;
      }
      const next = /^\s+(\p{L}+)/u.exec(text.slice(at + found.length))?.[1];
      const noun = next !== undefined && !ONE_STAYS.has(next.toLowerCase());
      const before = lang === "es" && noun && !(percent || parts.decimals);
      const said = [numberWords(parts.whole, lang, before)];
      if (parts.decimals) {
        said.push(
          choose(lang === "es", "coma", "point"),
          digitWords(parts.decimals, lang),
        );
      }
      if (percent) {
        said.push(choose(lang === "es", "por ciento", "percent"));
      }
      return (
        choose(!!minus, choose(lang === "es", "menos ", "minus "), "") +
        said.join(" ")
      );
    },
  );
}

// The punctuation that sets the rhythm.

// CONNECTORS open a sentence and are set off by a comma, as they are said: a short pause after.
const CONNECTORS: Record<Words, string[]> = {
  es: [
    "Sin embargo",
    "No obstante",
    "Por ejemplo",
    "Además",
    "Por lo tanto",
    "Por lo general",
    "En resumen",
    "En conclusión",
    "Por otro lado",
    "Es decir",
    "De hecho",
    "Por cierto",
    "En cambio",
    "Por último",
    "Ahora bien",
    "En primer lugar",
    "En segundo lugar",
    "Por consiguiente",
  ],
  en: [
    "However",
    "Moreover",
    "Furthermore",
    "Therefore",
    "For example",
    "For instance",
    "In addition",
    "In conclusion",
    "In summary",
    "On the other hand",
    "Meanwhile",
    "Nevertheless",
    "Finally",
    "Additionally",
    "Of course",
    "In fact",
    "As a result",
    "By the way",
    "Also",
    "Overall",
    "Instead",
  ],
};
const CONNECTOR_PATTERNS: Record<Words, RegExp> = {
  es: connectorPattern("es"),
  en: connectorPattern("en"),
};

function connectorPattern(lang: Words): RegExp {
  const list = [...CONNECTORS[lang]]
    .sort((a, b) => b.length - a.length)
    .join("|");
  return new RegExp(String.raw`(^|[.!?…]\s+)(${list})(?=\s+\p{L})`, "gu");
}

// setRhythm marks the pauses: ellipses are kept as they are, dashes, colons, brackets and slashes
// become commas, quotes go, stray spaces and doubled marks are cleaned up, and the sentence ends
// with a full stop when it ends with nothing.
function setRhythm(text: string, lang: Words | null): string {
  let t = text
    .replace(/\n+/g, (_, at: number) =>
      choose(/[.!?…,;:]$/.test(text.slice(0, at).trimEnd()), " ", ". "),
    )
    .replace(/\s+/g, " ")
    .replace(/(?: ?\.){3,} ?| ?… ?/g, "... ")
    .replace(/ [-–—]+ | ?[–—] ?/g, ", ")
    .replace(/ ?:(?= |$)/g, ",")
    .replace(/ ?[()[\]{}] ?/g, ", ")
    .replace(/ \/ /g, ", ")
    .replace(/[“”«»"]/g, "")
    .replace(/[‘’]/g, "'");
  if (lang) {
    t = t.replace(CONNECTOR_PATTERNS[lang], "$1$2,");
  }
  t = t
    .replace(/\s+/g, " ")
    .replace(/ ([,.;:!?])/g, "$1")
    .replace(/,(?:\s*,)+/g, ",")
    .replace(/,\s*([.!?;])/g, "$1")
    .replace(/([;:])\s*,/g, "$1")
    .replace(/^[\s,;:]+/, "")
    .trim();
  if (/[,;:]$/.test(t)) {
    return t.slice(0, -1) + ".";
  }
  return t + choose(/[.!?…]$/.test(t), "", ".");
}

// speechText is a sentence as a Piper voice is given it, in the language it is said in: its
// abbreviations written out, its numbers in words, and its pauses made explicit. A text with
// nothing to read in it comes back as it was.
export function speechText(text: string, lang: string): string {
  const base = baseOf(lang);
  let t = text;
  if (base) {
    t = writeTimes(writeAbbreviations(t, base));
    t = writeNumbers(choose(base === "en", writeOrdinals(t), t), base);
  }
  t = setRhythm(t, base);
  return choose(/[\p{L}\p{N}]/u.test(t), t, text);
}

// ENDS_IN_ABBREVIATION is a title or other abbreviation that is followed by what it introduces: a
// full stop after it is not the end of the sentence.
const ENDS_IN_ABBREVIATION = new RegExp(
  String.raw`(?:^|[\s("“«¿¡])(?:${[
    "sr",
    "sra",
    "srta",
    "sres",
    "dr",
    "dra",
    "lic",
    "ing",
    "arq",
    "prof",
    "profa",
    "gral",
    "mr",
    "mrs",
    "ms",
    "jr",
    "st",
    "mt",
    "vs",
    "núm",
    "pág",
    "págs",
    "fig",
    "tel",
    String.raw`e\.g`,
    String.raw`i\.e`,
    String.raw`p\.\s?ej`,
    String.raw`ee\.\s?uu`,
    "approx",
    "aprox",
  ].join("|")})\.$`,
  "iu",
);

// endsInAbbreviation says a piece of text ends with an abbreviation the sentence goes on after.
export function endsInAbbreviation(piece: string): boolean {
  return ENDS_IN_ABBREVIATION.test(piece);
}
