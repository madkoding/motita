// Speech output: the agent's answers read aloud with the voices of the device (the Web Speech
// API's speechSynthesis), or with the gateway's voice (POST /v1/voice/speak) when it has one. With
// the device's voices the text never leaves this page for Motita's sake, although a browser may
// hand it to a voice of its own vendor (Chrome's "Google" voices are remote).
//
// The parts decided without a voice live here, so they can be tested without one: what is worth
// reading out of an answer, how it is cut into sentences, how words are pronounced, which voice is
// used, and the reader that speaks the sentences one after another.
import type { Lang } from "./i18n";
import type { Api } from "./prApi";

// The words said in place of what is not read. The page passes them translated; this module does
// not import the translations, so it runs in a test as it is.
export interface SpeechWords {
  link: string;
  code: string;
  formula: string;
  table: string;
  rest: string;
}
export const ENGLISH_WORDS: SpeechWords = {
  link: "link",
  code: "Code block.",
  formula: "Formula.",
  table: "Table.",
  rest: "The rest is on screen.",
};

// When answers are read without being asked: never, only after a turn that was spoken (dictated or
// heard after the wake phrase), or always.
export type SpeakMode = "off" | "voice" | "always";

// What is said: the answer word for word, or, in the natural style, short lines the session's
// model writes about the turn (when it starts, now and then while the agent works, and the gist
// of the answer when it ends).
export type SpeakStyle = "literal" | "natural";

// Who speaks: the device's own voices, the gateway's voice (voice.tts), the same on every device,
// or a local voice: a Piper model the gateway downloaded, run in this browser.
export type SpeechEngine = "device" | "gateway" | "local";

// The read-aloud settings, kept in this browser only: voices are a property of the device.
export interface SpeechPrefs {
  mode: SpeakMode;
  style: SpeakStyle;
  engine: SpeechEngine;
  // gatewayVoice is the gateway voice chosen; empty is the gateway's default.
  gatewayVoice: string;
  // localVoice is the local voice chosen, by its catalog id; empty is none.
  localVoice: string;
  // voiceURI is the chosen voice; empty picks the device's own for the interface language.
  voiceURI: string;
  rate: number;
  // pitch is how high the device's voices speak (0.5 to 2, 1 is theirs). expressiveness and
  // variation shape a local voice: how much its intonation varies, and its rhythm (0 to 2, times
  // the voice's own; 1 is the voice as it was trained).
  pitch: number;
  expressiveness: number;
  variation: number;
  // kokoroMix is the Kokoro voice the chosen one is mixed with ("" is none), and kokoroMixAmount
  // how much of it (0 to 1: 0 is the chosen voice alone, 1 the other one).
  kokoroMix: string;
  kokoroMixAmount: number;
  // chatterboxEmotion is how much emotion a Chatterbox voice puts in (its exaggeration).
  chatterboxEmotion: number;
  // maxChars is the longest answer read whole; a longer one is read up to its first paragraph.
  // Zero reads everything.
  maxChars: number;
  // conversation listens again when an answer to a spoken turn has been read, and sends what it
  // hears: a conversation without touching the page, until an exit phrase or a tap.
  conversation: boolean;
  // notices says aloud what the page shows as a notice: the CI of a pull request, an approval
  // the agent waits for. Only while the tab is visible.
  notices: boolean;
  // byLanguage reads each sentence with a voice of its own language; langVoices are the voices
  // chosen per language ("" or absent: the device's own for it).
  byLanguage: boolean;
  langVoices: Partial<Record<Lang, string>>;
  // pronunciations are the person's own: a term and how to say it, before the built-in ones.
  pronunciations: Pronunciation[];
  // subtitles shows on screen, like a film's subtitles, the sentence the voice is saying;
  // subtitleSize is how big it is drawn.
  subtitles: boolean;
  subtitleSize: SubtitleSize;
}

// How big the subtitles are drawn.
export type SubtitleSize = "small" | "medium" | "large";

export interface Pronunciation {
  term: string;
  say: string;
}

// The lengths an answer can be read whole up to, in characters: short, medium and long.
export const SHORT_CHARS = 300;
export const MEDIUM_CHARS = 600;
export const LONG_CHARS = 1500;

// The speeds besides the normal one (1) and twice as fast (2).
const SLOW_RATE = 0.75;
const QUICK_RATE = 1.25;
const FAST_RATE = 1.5;

export const SPEECH_KEY = "motita.ui.speech";
export const DEFAULT_SPEECH_PREFS: SpeechPrefs = {
  mode: "voice",
  style: "literal",
  engine: "device",
  gatewayVoice: "",
  localVoice: "",
  voiceURI: "",
  rate: 1,
  pitch: 1,
  expressiveness: 1,
  variation: 1,
  kokoroMix: "",
  kokoroMixAmount: 0.5,
  chatterboxEmotion: 0.5,
  maxChars: MEDIUM_CHARS,
  conversation: false,
  notices: false,
  byLanguage: false,
  langVoices: {},
  pronunciations: [],
  subtitles: false,
  subtitleSize: "medium",
};
// SPEECH_PREFS_EVENT tells the page the settings changed in the settings screen.
export const SPEECH_PREFS_EVENT = "motita-speech-prefs";

export const RATES = [SLOW_RATE, 1, QUICK_RATE, FAST_RATE, 2];
export const LENGTHS = [SHORT_CHARS, MEDIUM_CHARS, LONG_CHARS, 0];
const MODES: SpeakMode[] = ["off", "voice", "always"];
const STYLES: SpeakStyle[] = ["literal", "natural"];
const ENGINES: SpeechEngine[] = ["device", "gateway", "local"];
export const SUBTITLE_SIZES: SubtitleSize[] = ["small", "medium", "large"];

function oneOf<T>(v: unknown, list: T[], fallback: T): T {
  if (list.includes(v as T)) {
    return v as T;
  }
  return fallback;
}

// The ranges of the voice's sliders: the device's pitch, and a local voice's expressiveness and
// variation.
export const PITCH_RANGE: [number, number] = [0.5, 2];
export const TUNE_RANGE: [number, number] = [0, 2];
// MIX_RANGE is how much of another Kokoro voice is mixed in.
export const MIX_RANGE: [number, number] = [0, 1];
// EMOTION_RANGE is how much emotion a Chatterbox voice can be asked for.
export const EMOTION_RANGE: [number, number] = [0.25, 2];

// numberIn is v when it is a number within range, fallback otherwise.
function numberIn(v: unknown, range: [number, number], fallback: number): number {
  if (typeof v === "number" && v >= range[0] && v <= range[1]) {
    return v;
  }
  return fallback;
}

function stringOr(v: unknown, fallback: string): string {
  if (typeof v === "string") {
    return v;
  }
  return fallback;
}

// The longest list of pronunciations kept, and the longest term or reading.
const MAX_PRONUNCIATIONS = 200;
const MAX_TERM = 80;

// langVoicesOf keeps the voices of the languages this page knows.
function langVoicesOf(v: unknown): Partial<Record<Lang, string>> {
  const out: Partial<Record<Lang, string>> = {};
  if (typeof v !== "object" || v === null) {
    return out;
  }
  const raw = v as Record<string, unknown>;
  for (const l of ["es", "en"] as Lang[]) {
    if (typeof raw[l] === "string" && raw[l]) {
      out[l] = raw[l];
    }
  }
  return out;
}

// textOf is a saved string, trimmed, or "" for anything else.
function textOf(v: unknown): string {
  return stringOr(v, "").trim();
}

// pronunciationsOf keeps the well-formed entries of a saved list.
function pronunciationsOf(v: unknown): Pronunciation[] {
  if (!Array.isArray(v)) {
    return [];
  }
  const out: Pronunciation[] = [];
  for (const e of v.slice(0, MAX_PRONUNCIATIONS)) {
    const term = textOf(e?.term);
    const say = textOf(e?.say);
    if (term && say && term.length <= MAX_TERM && say.length <= MAX_TERM) {
      out.push({ term, say });
    }
  }
  return out;
}

// parsePronunciations reads the list as typed in the settings: one "term = how to say it" a line.
// A line without "=" or with an empty side is left out; a term typed twice keeps its last reading.
export function parsePronunciations(text: string): Pronunciation[] {
  const byTerm = new Map<string, Pronunciation>();
  for (const line of text.split("\n")) {
    const at = line.indexOf("=");
    if (at < 0) {
      continue;
    }
    const term = line.slice(0, at).trim();
    const say = line.slice(at + 1).trim();
    if (term && say) {
      byTerm.delete(term.toLowerCase());
      byTerm.set(term.toLowerCase(), { term, say });
    }
  }
  return pronunciationsOf([...byTerm.values()]);
}

// formatPronunciations writes the list back the way parsePronunciations reads it.
export function formatPronunciations(list: Pronunciation[]): string {
  return list.map((p) => p.term + " = " + p.say).join("\n");
}

export function loadSpeechPrefs(): SpeechPrefs {
  try {
    const raw = JSON.parse(localStorage.getItem(SPEECH_KEY) || "{}");
    const d = DEFAULT_SPEECH_PREFS;
    return {
      mode: oneOf(raw.mode, MODES, d.mode),
      style: oneOf(raw.style, STYLES, d.style),
      engine: oneOf(raw.engine, ENGINES, d.engine),
      gatewayVoice: stringOr(raw.gatewayVoice, d.gatewayVoice),
      localVoice: stringOr(raw.localVoice, d.localVoice),
      voiceURI: stringOr(raw.voiceURI, d.voiceURI),
      rate: oneOf(raw.rate, RATES, d.rate),
      pitch: numberIn(raw.pitch, PITCH_RANGE, d.pitch),
      expressiveness: numberIn(raw.expressiveness, TUNE_RANGE, d.expressiveness),
      variation: numberIn(raw.variation, TUNE_RANGE, d.variation),
      kokoroMix: stringOr(raw.kokoroMix, d.kokoroMix),
      kokoroMixAmount: numberIn(raw.kokoroMixAmount, MIX_RANGE, d.kokoroMixAmount),
      chatterboxEmotion: numberIn(raw.chatterboxEmotion, EMOTION_RANGE, d.chatterboxEmotion),
      maxChars: oneOf(raw.maxChars, LENGTHS, d.maxChars),
      conversation: raw.conversation === true,
      notices: raw.notices === true,
      byLanguage: raw.byLanguage === true,
      langVoices: langVoicesOf(raw.langVoices),
      pronunciations: pronunciationsOf(raw.pronunciations),
      subtitles: raw.subtitles === true,
      subtitleSize: oneOf(raw.subtitleSize, SUBTITLE_SIZES, d.subtitleSize),
    };
  } catch {
    return { ...DEFAULT_SPEECH_PREFS };
  }
}

export function saveSpeechPrefs(p: SpeechPrefs): void {
  try {
    localStorage.setItem(SPEECH_KEY, JSON.stringify(p));
  } catch {
    /* ignore */
  }
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(SPEECH_PREFS_EVENT));
  }
}

// speechSupported says whether this browser can read aloud.
export function speechSupported(): boolean {
  const g = globalThis as {
    speechSynthesis?: unknown;
    SpeechSynthesisUtterance?: unknown;
  };
  return (
    typeof g.speechSynthesis === "object" &&
    g.speechSynthesis !== null &&
    typeof g.SpeechSynthesisUtterance === "function"
  );
}

// PRONUNCIATIONS are how the voices of a language should say the terms they spell badly. Applied
// to the spoken text only, as whole words, matching case: they are acronyms.
const PRONUNCIATIONS: Record<Lang, Record<string, string>> = {
  es: {
    PRs: "pe erres",
    PR: "pe erre",
    CI: "ce i",
    URL: "u erre ele",
    API: "a pe i",
    CLI: "ce ele i",
    LLM: "ele ele eme",
    UI: "u i",
  },
  en: {},
};

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, String.raw`\$&`);
}

// wholeWord matches a term as a whole word: no letter or digit on either side.
function wholeWord(term: string, flags: string): RegExp {
  return new RegExp(
    String.raw`(^|[^\p{L}\p{N}])` +
      escapeRegExp(term) +
      String.raw`(?![\p{L}\p{N}])`,
    flags,
  );
}

// pronounce rewrites the person's own terms (whole words, any case), then the built-in ones of the
// language (whole words, matching case: they are acronyms).
export function pronounce(
  text: string,
  lang: Lang,
  own: Pronunciation[] = [],
): string {
  let out = text;
  for (const p of own) {
    out = out.replace(
      wholeWord(p.term, "giu"),
      (_m, pre: string) => pre + p.say,
    );
  }
  const list = PRONUNCIATIONS[lang];
  for (const term of Object.keys(list)) {
    out = out.replace(
      wholeWord(term, "gu"),
      (_m, pre: string) => pre + list[term],
    );
  }
  return out;
}

// LANGUAGE OF A SENTENCE: the most frequent words of each language, the ones the other one does
// not share. Counting them is enough to tell a Spanish sentence from an English one in an answer
// that mixes both, with no dictionary and no dependency.
const COMMON: Record<Lang, Set<string>> = {
  es: new Set(
    (
      "el la los las un una unos unas de del que y en es por para con no se lo le su sus al " +
      "como pero más este esta estos estas ese esa hay ya muy también porque cuando donde " +
      "sí son está están ser fue hace tiene puede todo todos otra otro sobre entre sin hasta"
    ).split(" "),
  ),
  en: new Set(
    (
      "the of and to is in that it for with as was on are be this by at from or an but not " +
      "have has had which you they we he she his her there their what when where will would " +
      "can could should all one been if do does did into than then these those"
    ).split(" "),
  ),
};

// detectLang says which language a sentence is in, or the fallback when it cannot tell: it takes
// two more of one language's words than of the other's, so a name or a stray word changes nothing.
export function detectLang(text: string, fallback: Lang): Lang {
  const words = text.toLowerCase().match(/\p{L}+/gu) || [];
  let es = 0;
  let en = 0;
  for (const w of words) {
    es += Number(COMMON.es.has(w));
    en += Number(COMMON.en.has(w));
  }
  if (es >= en + 2) {
    return "es";
  }
  if (en >= es + 2) {
    return "en";
  }
  return fallback;
}

const FENCE = /^\s*(```|~~~)/;
const TABLE_ROW = /^\s*\|.*\|\s*$/;
const RULE = /^\s*([-*_])(\s*\1){2,}\s*$/;
const URL_RE = /\b(?:https?:\/\/|www\.)[^\s<>()]*[^\s<>().,;:!?]/g;
const END_PUNCT = /[.!?…:;]$/;

// shortenPath says a file path by its last name: "web/src/App.tsx" reads "App.tsx".
function shortenPath(code: string): string {
  if (/\s/.test(code) || !code.includes("/")) {
    return code;
  }
  const parts = code.split("/").filter(Boolean);
  return parts[parts.length - 1] || code;
}

// linkTexts replaces each Markdown link, [text](address), with its text. It scans rather than
// matching a pattern, so a line full of brackets costs no backtracking: the first "]" after a "["
// and the first ")" after a "(" are found once and kept while the scan has not passed them.
function linkTexts(line: string): string {
  let out = "";
  let copied = 0;
  let close = -1;
  let end = -1;
  let at = line.indexOf("[");
  while (at >= 0) {
    if (close <= at) {
      close = line.indexOf("]", at + 1);
    }
    if (close < 0) {
      break;
    }
    if (end <= close + 1) {
      end = line.indexOf(")", close + 2);
    }
    if (end < 0) {
      // No ")" from here on: kept past the end, so it is not looked for again.
      end = line.length;
    }
    const linked =
      close > at + 1 && line[close + 1] === "(" && end < line.length;
    if (linked) {
      out += line.slice(copied, at) + line.slice(at + 1, close);
      copied = end + 1;
      at = line.indexOf("[", copied);
    } else {
      at = line.indexOf("[", at + 1);
    }
  }
  return out + line.slice(copied);
}

// inline turns one line of Markdown into the words it shows.
function inline(line: string, link: string): string {
  return linkTexts(
    line
      // Images read their description, links their text.
      .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1"),
  )
    .replace(/\[\^[^\]]+\]/g, "")
    .replace(/<(https?:\/\/[^>]+)>/g, () => link)
    .replace(URL_RE, () => link)
    .replace(/`([^`]+)`/g, (_m, code: string) => shortenPath(code))
    .replace(/<\/?[a-zA-Z][^>]*>/g, "")
    .replace(/(\*\*|__)(.+?)\1/g, "$2")
    .replace(
      /(^|[^\p{L}\p{N}*_])[*_]([^*_\s][^*_]*?)[*_](?![\p{L}\p{N}])/gu,
      "$1$2",
    )
    .replace(/~~(.+?)~~/g, "$1")
    .replace(/\s+/g, " ")
    .trim();
}

// withStop ends a heading or a list item with a full stop, so it is read as a sentence of its own
// and not run into the next one.
function withStop(s: string): string {
  if (!s || END_PUNCT.test(s)) {
    return s;
  }
  return s + ".";
}

// The characters that open a code fence: ``` or ~~~.
const FENCE_LEN = 3;
const HEADING_MARK = /^\s*#{1,6}\s+/;
const ITEM_MARK = /^\s*(?:[-*+]|\d+[.)])\s+/;
const TASK_BOX = /^\[[ xX]\]\s+/;

// endOf is the first line from `from` on that closes a block, or the end of the text.
function endOf(lines: string[], from: number, closes: (l: string) => boolean) {
  let i = from;
  while (i < lines.length && !closes(lines[i])) {
    i++;
  }
  return i;
}

// blockAt names the block that starts at line i (a code block, a formula or a table) and says on
// which line it ends; null when no block starts there.
function blockAt(
  lines: string[],
  i: number,
  words: SpeechWords,
): { end: number; word: string } | null {
  const line = lines[i];
  if (FENCE.test(line)) {
    const fence = line.trim().slice(0, FENCE_LEN);
    const end = endOf(lines, i + 1, (l) => l.trim().startsWith(fence));
    return { end, word: words.code };
  }
  if (line.trim() === "$$") {
    const end = endOf(lines, i + 1, (l) => l.trim() === "$$");
    return { end, word: words.formula };
  }
  if (TABLE_ROW.test(line)) {
    const end = endOf(lines, i + 1, (l) => !TABLE_ROW.test(l)) - 1;
    return { end, word: words.table };
  }
  return null;
}

// headingText is the title of a Markdown heading, without its closing hashes; null for a line
// that is not a heading.
function headingText(line: string): string | null {
  const mark = HEADING_MARK.exec(line);
  if (!mark) {
    return null;
  }
  return withoutClosingHashes(line.slice(mark[0].length).trimEnd()).trimEnd();
}

// withoutClosingHashes drops the #s an ATX heading may close with ("## Title ##").
function withoutClosingHashes(s: string): string {
  let end = s.length;
  while (end > 0 && s[end - 1] === "#") {
    end--;
  }
  return s.slice(0, end);
}

// itemText is the text of a list item, without its bullet or task box; null for a line that is
// not an item.
function itemText(line: string): string | null {
  const mark = ITEM_MARK.exec(line);
  if (!mark) {
    return null;
  }
  const rest = line.slice(mark[0].length);
  const box = TASK_BOX.exec(rest);
  if (box) {
    return rest.slice(box[0].length);
  }
  return rest;
}

// A line of prose as it is read: a heading is a paragraph of its own, anything else joins the
// paragraph it is in.
interface Prose {
  text: string;
  own: boolean;
}

// proseLine reads one line outside a block; null for a line that ends the paragraph (blank, or a
// horizontal rule).
function proseLine(line: string, link: string): Prose | null {
  if (!line.trim() || RULE.test(line)) {
    return null;
  }
  const heading = headingText(line);
  if (heading !== null) {
    return { text: withStop(inline(heading, link)), own: true };
  }
  const item = itemText(line);
  if (item !== null) {
    return { text: withStop(inline(item, link)), own: false };
  }
  return { text: inline(line.replace(/^\s*(>\s*)+/, ""), link), own: false };
}

// toSpeakable is the text of an answer worth reading, as paragraphs separated by a blank line:
// code blocks, tables and formulas are named rather than read, links read their text, addresses
// are said to be links, and Markdown marks are dropped.
export function toSpeakable(
  markdown: string,
  words: SpeechWords = ENGLISH_WORDS,
): string {
  const paragraphs: string[] = [];
  let current: string[] = [];
  const flush = () => {
    const p = current.join(" ").replace(/\s+/g, " ").trim();
    if (p) {
      paragraphs.push(p);
    }
    current = [];
  };
  const lines = markdown.replace(/\r\n?/g, "\n").split("\n");
  let i = 0;
  while (i < lines.length) {
    const block = blockAt(lines, i, words);
    if (block) {
      flush();
      paragraphs.push(block.word);
      i = block.end + 1;
      continue;
    }
    const prose = proseLine(lines[i], words.link);
    if (prose === null) {
      flush();
    } else if (prose.own) {
      flush();
      paragraphs.push(prose.text);
    } else {
      current.push(prose.text);
    }
    i++;
  }
  flush();
  return paragraphs.join("\n\n");
}

// limitLength keeps an answer within max characters: a longer one is read up to the end of its
// first paragraph (or of its last whole sentence within the limit), and says the rest is on
// screen. Zero means no limit.
export function limitLength(
  text: string,
  max: number,
  rest: string = ENGLISH_WORDS.rest,
): string {
  if (max <= 0 || text.length <= max) {
    return text;
  }
  const first = text.split("\n\n")[0];
  if (first.length <= max) {
    return first + "\n\n" + rest;
  }
  const head = first.slice(0, max);
  const stop = Math.max(
    head.lastIndexOf(". "),
    head.lastIndexOf("! "),
    head.lastIndexOf("? "),
  );
  if (stop > 0) {
    return head.slice(0, stop + 1) + "\n\n" + rest;
  }
  const space = head.lastIndexOf(" ");
  let cut = head;
  if (space > 0) {
    cut = head.slice(0, space);
  }
  return cut + "…\n\n" + rest;
}

// The longest piece handed to the voice at once. Chrome stops an utterance that runs for about
// fifteen seconds, and a short piece also starts sooner.
const MAX_PIECE = 220;
// A comma closer to the start than this is no place to cut: the space nearest the end is used.
const MIN_COMMA_SHARE = 3;
const MIN_COMMA_CUT = MAX_PIECE / MIN_COMMA_SHARE;

// splitLong cuts a sentence that is too long at a comma, or at a space.
function splitLong(s: string): string[] {
  const out: string[] = [];
  let rest = s;
  while (rest.length > MAX_PIECE) {
    const head = rest.slice(0, MAX_PIECE);
    let cut = head.lastIndexOf(", ");
    if (cut < MIN_COMMA_CUT) {
      cut = head.lastIndexOf(" ");
    }
    if (cut <= 0) {
      cut = MAX_PIECE - 1;
    }
    out.push(rest.slice(0, cut + 1).trim());
    rest = rest.slice(cut + 1).trim();
  }
  if (rest) {
    out.push(rest);
  }
  return out;
}

// sentences cuts the speakable text in the pieces the voice says one by one.
export function sentences(text: string): string[] {
  const out: string[] = [];
  for (const para of text.split(/\n{2,}/)) {
    const parts = para.trim().split(/(?<=[.!?…])\s+/);
    for (const p of parts) {
      if (p.trim()) {
        out.push(...splitLong(p.trim()));
      }
    }
  }
  return out;
}

// A sentence as it is shown (to find it on screen), as it is said (pronunciations applied), and
// the language it is said in.
export interface Sentence {
  shown: string;
  said: string;
  lang?: Lang;
}

// How a text is turned into sentences: by language (each sentence in its own) and with the
// person's pronunciations.
export interface ScriptOptions {
  byLanguage?: boolean;
  pronunciations?: Pronunciation[];
}

// script is what an answer is read as.
export function script(
  markdown: string,
  maxChars: number,
  lang: Lang,
  words: SpeechWords = ENGLISH_WORDS,
  opts: ScriptOptions = {},
): Sentence[] {
  const text = limitLength(toSpeakable(markdown, words), maxChars, words.rest);
  return sentences(text).map((s) => {
    let l = lang;
    if (opts.byLanguage) {
      l = detectLang(s, lang);
    }
    return { shown: s, said: pronounce(s, l, opts.pronunciations), lang: l };
  });
}

// The part of a voice this page uses: SpeechSynthesisVoice, or a test's stand-in.
export interface VoiceLike {
  voiceURI: string;
  name: string;
  lang: string;
  localService?: boolean;
  default?: boolean;
}

function speaks(v: VoiceLike, lang: Lang): boolean {
  return v.lang.toLowerCase().replace("_", "-").split("-")[0] === lang;
}

// voicesFor are the voices of a language, those on the device first, then by name.
export function voicesFor<V extends VoiceLike>(all: V[], lang: Lang): V[] {
  return all
    .filter((v) => speaks(v, lang))
    .sort((a, b) => {
      const local =
        Number(b.localService === true) - Number(a.localService === true);
      return local || a.name.localeCompare(b.name);
    });
}

// pickVoice is the chosen voice when it speaks the language, otherwise the device's default for
// the language, otherwise its first one; null leaves the choice to the browser.
export function pickVoice<V extends VoiceLike>(
  all: V[],
  uri: string,
  lang: Lang,
): V | null {
  const mine = voicesFor(all, lang);
  return (
    mine.find((v) => uri && v.voiceURI === uri) ??
    mine.find((v) => v.default) ??
    mine[0] ??
    null
  );
}

// The locale a language is read in when no voice was found.
const LOCALE: Record<Lang, string> = { es: "es-ES", en: "en-US" };

// The parts of speechSynthesis and of an utterance the reader uses, so a test can stand in.
export interface Synth {
  speak(u: UtteranceLike): void;
  cancel(): void;
}
export interface UtteranceLike {
  text: string;
  lang: string;
  rate: number;
  pitch?: number;
  voice: unknown;
  onstart: ((e: unknown) => void) | null;
  onend: ((e: unknown) => void) | null;
  onerror: ((e: { error?: string }) => void) | null;
}
export type MakeUtterance = (text: string) => UtteranceLike;

export interface ReadOptions {
  voice: VoiceLike | null;
  // voiceFor is the voice of a sentence in another language than lang; absent, voice says all.
  voiceFor?: (lang: Lang) => VoiceLike | null;
  lang: Lang;
  rate: number;
  // pitch is the device's voice's (1 when absent); the clip readers have none.
  pitch?: number;
  // onSentence is told which sentence the voice starts.
  onSentence?: (i: number) => void;
  // onEnd is told once when the reading ends: finished, stopped (stopped is true), or failed
  // (with the reason).
  onEnd?: (error?: string, stopped?: boolean) => void;
}

// Reader says sentences one after another. One utterance at a time: a queue of many is where
// browsers lose the end events, and a piece at a time is what lets the page follow along.
export class Reader {
  private run = 0;
  private active: ReadOptions | null = null;
  private readonly synth: Synth;
  private readonly make: MakeUtterance;
  constructor(synth: Synth, make: MakeUtterance) {
    this.synth = synth;
    this.make = make;
  }

  get reading(): boolean {
    return this.active !== null;
  }

  read(list: Sentence[], opts: ReadOptions): void {
    this.stop();
    if (list.length === 0) {
      opts.onEnd?.();
      return;
    }
    this.run++;
    const run = this.run;
    this.active = opts;
    const next = (i: number) => {
      if (run !== this.run) {
        return;
      }
      if (i >= list.length) {
        this.finish();
        return;
      }
      const u = this.make(list[i].said);
      const l = list[i].lang ?? opts.lang;
      let voice = opts.voice;
      if (l !== opts.lang && opts.voiceFor) {
        voice = opts.voiceFor(l);
      }
      u.lang = voice?.lang || LOCALE[l];
      u.voice = voice;
      u.rate = opts.rate;
      u.pitch = opts.pitch ?? 1;
      u.onstart = () => {
        if (run === this.run) {
          opts.onSentence?.(i);
        }
      };
      u.onend = () => next(i + 1);
      u.onerror = (e) => {
        if (run !== this.run) {
          return;
        }
        // A cancel ends the utterance with an error in some browsers: that is a stop, not a fault.
        if (e.error === "interrupted" || e.error === "canceled") {
          this.finish();
          return;
        }
        this.finish(e.error || "error");
      };
      this.synth.speak(u);
    };
    next(0);
  }

  // stop silences the voice; the reading in progress ends without an error.
  stop(): void {
    this.run++;
    const was = this.active;
    this.active = null;
    this.synth.cancel();
    was?.onEnd?.(undefined, true);
  }

  private finish(error?: string): void {
    const was = this.active;
    this.active = null;
    this.run++;
    was?.onEnd?.(error);
  }
}

// THE GATEWAY'S VOICE: each sentence is sent to POST /v1/voice/speak and the audio that comes
// back is played. The next sentence is asked for while one plays, so the voice does not pause
// between them for longer than the endpoint takes.

// The parts of an <audio> element the clip reader uses, so a test can stand in.
export interface AudioLike {
  src: string;
  playbackRate: number;
  onplaying: (() => void) | null;
  onended: (() => void) | null;
  onerror: (() => void) | null;
  play(): Promise<void>;
  pause(): void;
}

// fetchClip says one text with the gateway's voice or a local one: the audio, or a thrown error.
// ahead, when more than 1, is how many sentences it can say at once, and so how many the reader
// asks for beyond the one playing (1 when absent).
// lang is the sentence's language, for a voice that speaks several.
export type FetchClip = ((text: string, signal: AbortSignal, lang?: string) => Promise<Blob>) & { ahead?: number };

// reasonOf is why a clip could not be had: the gateway's message, or "error".
function reasonOf(e: unknown): string {
  if (e instanceof Error) {
    return e.message;
  }
  return "error";
}

// playError is why a clip could not be played: the browser wants a tap first, or it could not.
function playError(e: { name?: string }): string {
  if (e?.name === "NotAllowedError") {
    return "not-allowed";
  }
  return "audio";
}

// clipsPlaying counts the clips playing now, for voiceBusy.
let clipsPlaying = 0;

// speakClip is the FetchClip of a gateway: POST /v1/voice/speak with the voice chosen.
export function speakClip(api: Api, voice: string): FetchClip {
  return async (text, signal) => {
    const res = await api("/v1/voice/speak", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text, voice }),
      signal,
    });
    if (!res.ok) {
      let msg = "HTTP " + res.status;
      try {
        const body = (await res.json()) as { error?: string };
        msg = body.error || msg;
      } catch {
        /* not JSON */
      }
      throw new Error(msg);
    }
    return res.blob();
  };
}

// ClipReader reads sentences with the gateway's voice or a local one, with the same contract as
// Reader.
export class ClipReader {
  private run = 0;
  private active: ReadOptions | null = null;
  private abort: AbortController | null = null;
  private audio: AudioLike | null = null;
  private urls: string[] = [];
  private playing = false;
  private readonly fetchClip: () => FetchClip;
  private readonly makeAudio: () => AudioLike;
  private readonly url: { create(b: Blob): string; revoke(u: string): void };
  // fetchClip is read at each reading, so a change of voice applies to the next one.
  constructor(
    fetchClip: () => FetchClip,
    makeAudio: () => AudioLike,
    url: { create(b: Blob): string; revoke(u: string): void },
  ) {
    this.fetchClip = fetchClip;
    this.makeAudio = makeAudio;
    this.url = url;
  }

  get reading(): boolean {
    return this.active !== null;
  }

  read(list: Sentence[], opts: ReadOptions): void {
    this.stop();
    if (list.length === 0) {
      opts.onEnd?.();
      return;
    }
    this.run++;
    const run = this.run;
    this.active = opts;
    const abort = new AbortController();
    this.abort = abort;
    const fetchClip = this.fetchClip();
    const ahead = Math.max(1, fetchClip.ahead ?? 1);
    const clips: Promise<string>[] = [];
    // clip asks for sentence i once; the answer is kept until it is played.
    const clip = (i: number): Promise<string> => {
      clips[i] ??= fetchClip(list[i].said, abort.signal, list[i].lang ?? opts.lang).then((b) => {
        const u = this.url.create(b);
        this.urls.push(u);
        return u;
      });
      // A failure is reported when the sentence is reached, not while another one plays.
      clips[i].catch(() => undefined);
      return clips[i];
    };
    const next = async (i: number) => {
      if (run !== this.run) {
        return;
      }
      if (i >= list.length) {
        this.finish();
        return;
      }
      const current = clip(i);
      // A voice that says several at once is asked for the next ones before this one is ready.
      for (let k = 1; k < ahead && i + k < list.length; k++) {
        void clip(i + k);
      }
      let src: string;
      try {
        src = await current;
      } catch (e) {
        if (run === this.run) {
          this.finish(reasonOf(e));
        }
        return;
      }
      if (run !== this.run) {
        return;
      }
      if (i + ahead < list.length) {
        void clip(i + ahead);
      }
      const a = this.makeAudio();
      this.audio = a;
      a.playbackRate = opts.rate;
      a.onplaying = () => {
        if (run === this.run) {
          this.setPlaying(true);
          opts.onSentence?.(i);
        }
      };
      a.onended = () => {
        this.setPlaying(false);
        void next(i + 1);
      };
      a.onerror = () => {
        if (run === this.run) {
          this.finish("audio");
        }
      };
      a.src = src;
      a.play().catch((e: { name?: string }) => {
        if (run === this.run) {
          this.finish(playError(e));
        }
      });
    };
    void next(0);
  }

  // stop silences the voice; the reading in progress ends without an error.
  stop(): void {
    const was = this.active;
    this.end();
    was?.onEnd?.(undefined, true);
  }

  private finish(error?: string): void {
    const was = this.active;
    this.end();
    was?.onEnd?.(error);
  }

  private end(): void {
    this.run++;
    this.active = null;
    this.abort?.abort();
    this.abort = null;
    if (this.audio) {
      this.audio.onended = null;
      this.audio.onerror = null;
      this.audio.pause();
      this.audio = null;
    }
    this.setPlaying(false);
    for (const u of this.urls) {
      this.url.revoke(u);
    }
    this.urls = [];
  }

  private setPlaying(on: boolean): void {
    if (on !== this.playing) {
      this.playing = on;
      clipsPlaying += Number(on) * 2 - 1;
    }
  }
}

// FlatText is the text an element shows with its spacing collapsed, and where each of its
// characters comes from: [node, offset].
interface FlatText {
  flat: string;
  map: { node: Text; offset: number }[];
}

// flatten collapses the text of root, leaving out the interface's own buttons.
function flatten(root: Node, doc: Document): FlatText {
  const out: FlatText = { flat: "", map: [] };
  const walker = doc.createTreeWalker(root, 4 /* NodeFilter.SHOW_TEXT */);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const text = n as Text;
    if (!text.parentElement?.closest("button")) {
      flattenNode(text, out);
    }
  }
  return out;
}

// flattenNode adds one text node to out. Every node starts as if after a space: the one before it
// ended on one, or was given one.
function flattenNode(text: Text, out: FlatText): void {
  const s = text.data;
  let last = true;
  for (const [i, ch] of s.split("").entries()) {
    const space = /\s/.test(ch);
    if (space && last) {
      continue;
    }
    if (space) {
      out.flat += " ";
    } else {
      out.flat += ch;
    }
    out.map.push({ node: text, offset: i });
    last = space;
  }
  if (!last) {
    // Blocks next to each other are separate words.
    out.flat += " ";
    out.map.push({ node: text, offset: s.length });
  }
}

// textRange finds a sentence in the text an element shows, ignoring spacing and the interface's
// own buttons, and returns its range; null when it cannot be found (a code block named instead of
// read, a pronunciation, a cut).
export function textRange(root: Node, needle: string): Range | null {
  const doc = root.ownerDocument || (root as Document);
  const { flat, map } = flatten(root, doc);
  const want = needle.replace(/\s+/g, " ").trim();
  if (!want) {
    return null;
  }
  const at = flat.indexOf(want);
  if (at < 0) {
    return null;
  }
  const from = map[at];
  const to = map[at + want.length - 1];
  const range = doc.createRange();
  range.setStart(from.node, from.offset);
  range.setEnd(to.node, Math.min(to.offset + 1, to.node.data.length));
  return range;
}

// SPEECH_STOP_EVENT asks the reader to be quiet: the microphone is about to listen.
export const SPEECH_STOP_EVENT = "motita-speech-stop";

// SPEECH_NOTICE_EVENT asks the page to say a notice aloud (the CI of a pull request, an approval):
// it is said when the person turned voice notices on, and the tab is visible.
export const SPEECH_NOTICE_EVENT = "motita-speech-notice";

export function sayNotice(text: string): void {
  if (typeof window !== "undefined" && text.trim()) {
    window.dispatchEvent(
      new CustomEvent(SPEECH_NOTICE_EVENT, { detail: text.trim() }),
    );
  }
}

// SUBTITLE_EVENT carries the sentence the voice starts saying, or "" when it stops: the
// subtitles on screen follow it, whichever voice speaks.
export const SUBTITLE_EVENT = "motita-subtitle";

export function showSubtitle(text: string): void {
  window.dispatchEvent(new CustomEvent(SUBTITLE_EVENT, { detail: text }));
}

// SPEECH_TURN_READ_EVENT tells the page the answer to a spoken turn was read to its end: in
// conversation mode, the microphone listens again.
export const SPEECH_TURN_READ_EVENT = "motita-speech-turn-read";

// EXIT_PHRASES end a conversation: said alone, or with "Motita" and the punctuation around them.
const EXIT_PHRASES = new Set([
  "basta",
  "para",
  "detente",
  "eso es todo",
  "gracias eso es todo",
  "terminamos",
  "stop",
  "enough",
  "that's all",
  "thats all",
  "that is all",
  "we're done",
  "were done",
]);

// isExitPhrase says whether what was heard ends the conversation.
export function isExitPhrase(text: string): boolean {
  const plain = text
    .toLowerCase()
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[^\p{L}\p{N}' ]+/gu, " ")
    .replace(/\bmotita\b/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  return EXIT_PHRASES.has(plain);
}

export function stopSpeaking(): void {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(SPEECH_STOP_EVENT));
  }
}

// voiceBusy says whether the device is speaking, so hands-free listening does not take Motita's
// own voice for the person's.
export function voiceBusy(): boolean {
  const synth = (globalThis as { speechSynthesis?: { speaking?: boolean } })
    .speechSynthesis;
  return synth?.speaking === true || clipsPlaying > 0;
}

// NATURAL STYLE: the lines the session's model writes about a turn.

export type NarrateStage = "start" | "progress" | "answer";

export interface NarrateBody {
  stage: NarrateStage;
  lang: Lang;
  request: string;
  notes?: string[];
  said?: string[];
  answer?: string;
}

// narrate asks the gateway what to say; "" means nothing is worth saying. A refusal throws.
export async function narrate(
  api: Api,
  sessionId: string,
  body: NarrateBody,
): Promise<string> {
  const res = await api(
    "/v1/sessions/" + encodeURIComponent(sessionId) + "/narrate",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok) {
    throw new Error("HTTP " + res.status);
  }
  const out = (await res.json()) as { text?: string };
  return (out.text || "").trim();
}

// notesOf turns the steps of a run into the notes the model is told about: what each step did,
// leaving out the line that names the mode auto mode picked.
export function notesOf(steps: { kind: string; text: string }[]): string[] {
  return steps
    .filter((st) => st.text.trim() && !st.text.startsWith("mode: "))
    .map(noteOf);
}

// noteOf is one step as a note: its kind, then what it did.
function noteOf(st: { kind: string; text: string }): string {
  if (st.kind) {
    return st.kind + ": " + st.text.trim();
  }
  return st.text.trim();
}

// When the natural style says something while the agent works: no sooner than PROGRESS_GAP_MS
// after the last line, and only when the run has done something new since.
export const PROGRESS_GAP_MS = 15000;

export function progressDue(
  now: number,
  lastAt: number,
  notes: number,
  lastNotes: number,
): boolean {
  return notes > lastNotes && now - lastAt >= PROGRESS_GAP_MS;
}
