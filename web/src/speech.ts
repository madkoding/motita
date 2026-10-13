// Speech output: the agent's answers read aloud with the voices of the device (the Web Speech
// API's speechSynthesis). Nothing here talks to the gateway: the text never leaves this page for
// Motita's sake, although a browser may hand it to a voice of its own vendor (Chrome's "Google"
// voices are remote).
//
// The parts decided without a voice live here, so they can be tested without one: what is worth
// reading out of an answer, how it is cut into sentences, how words are pronounced, which voice is
// used, and the reader that speaks the sentences one after another.
import type { Lang } from "./i18n";

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

// The read-aloud settings, kept in this browser only: voices are a property of the device.
export interface SpeechPrefs {
  mode: SpeakMode;
  // voiceURI is the chosen voice; empty picks the device's own for the interface language.
  voiceURI: string;
  rate: number;
  // maxChars is the longest answer read whole; a longer one is read up to its first paragraph.
  // Zero reads everything.
  maxChars: number;
}

export const SPEECH_KEY = "motita.ui.speech";
export const DEFAULT_SPEECH_PREFS: SpeechPrefs = {
  mode: "voice",
  voiceURI: "",
  rate: 1,
  maxChars: 600,
};
// SPEECH_PREFS_EVENT tells the page the settings changed in the settings screen.
export const SPEECH_PREFS_EVENT = "motita-speech-prefs";

export const RATES = [0.75, 1, 1.25, 1.5, 2];
export const LENGTHS = [300, 600, 1500, 0];
const MODES: SpeakMode[] = ["off", "voice", "always"];

function oneOf<T>(v: unknown, list: T[], fallback: T): T {
  if (list.includes(v as T)) {
    return v as T;
  }
  return fallback;
}

export function loadSpeechPrefs(): SpeechPrefs {
  try {
    const raw = JSON.parse(localStorage.getItem(SPEECH_KEY) || "{}");
    const d = DEFAULT_SPEECH_PREFS;
    return {
      mode: oneOf(raw.mode, MODES, d.mode),
      voiceURI: typeof raw.voiceURI === "string" ? raw.voiceURI : d.voiceURI,
      rate: oneOf(raw.rate, RATES, d.rate),
      maxChars: oneOf(raw.maxChars, LENGTHS, d.maxChars),
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
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// pronounce rewrites the terms of the language's pronunciation list.
export function pronounce(text: string, lang: Lang): string {
  const list = PRONUNCIATIONS[lang];
  let out = text;
  for (const term of Object.keys(list)) {
    const re = new RegExp(
      "(^|[^\\p{L}\\p{N}])" + escapeRegExp(term) + "(?![\\p{L}\\p{N}])",
      "gu",
    );
    out = out.replace(re, (_m, pre: string) => pre + list[term]);
  }
  return out;
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

// inline turns one line of Markdown into the words it shows.
function inline(line: string, link: string): string {
  return (
    line
      // Images read their description, links their text.
      .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
      .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
      .replace(/\[\^[^\]]+\]/g, "")
      .replace(/<(https?:\/\/[^>]+)>/g, () => link)
      .replace(URL_RE, () => link)
      .replace(/`([^`]+)`/g, (_m, code: string) => shortenPath(code))
      .replace(/<\/?[a-zA-Z][^>]*>/g, "")
      .replace(/(\*\*|__)(.+?)\1/g, "$2")
      .replace(/(^|[^\p{L}\p{N}*_])[*_]([^*_\s][^*_]*?)[*_](?![\p{L}\p{N}])/gu, "$1$2")
      .replace(/~~(.+?)~~/g, "$1")
      .replace(/\s+/g, " ")
      .trim()
  );
}

// withStop ends a heading or a list item with a full stop, so it is read as a sentence of its own
// and not run into the next one.
function withStop(s: string): string {
  if (!s || END_PUNCT.test(s)) {
    return s;
  }
  return s + ".";
}

// toSpeakable is the text of an answer worth reading, as paragraphs separated by a blank line:
// code blocks, tables and formulas are named rather than read, links read their text, addresses
// are said to be links, and Markdown marks are dropped.
export function toSpeakable(
  markdown: string,
  words: SpeechWords = ENGLISH_WORDS,
): string {
  const inline1 = (line: string) => inline(line, words.link);
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
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (FENCE.test(line)) {
      const fence = line.trim().slice(0, 3);
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(fence)) {
        i++;
      }
      flush();
      paragraphs.push(words.code);
      continue;
    }
    if (line.trim() === "$$") {
      i++;
      while (i < lines.length && lines[i].trim() !== "$$") {
        i++;
      }
      flush();
      paragraphs.push(words.formula);
      continue;
    }
    if (TABLE_ROW.test(line)) {
      while (i + 1 < lines.length && TABLE_ROW.test(lines[i + 1])) {
        i++;
      }
      flush();
      paragraphs.push(words.table);
      continue;
    }
    if (!line.trim() || RULE.test(line)) {
      flush();
      continue;
    }
    const heading = /^\s*#{1,6}\s+(.*?)\s*#*\s*$/.exec(line);
    if (heading) {
      flush();
      paragraphs.push(withStop(inline1(heading[1])));
      continue;
    }
    const item = /^\s*(?:[-*+]|\d+[.)])\s+(?:\[[ xX]\]\s+)?(.*)$/.exec(line);
    if (item) {
      current.push(withStop(inline1(item[1])));
      continue;
    }
    current.push(inline1(line.replace(/^\s*(>\s*)+/, "")));
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
  return (space > 0 ? head.slice(0, space) : head) + "…\n\n" + rest;
}

// The longest piece handed to the voice at once. Chrome stops an utterance that runs for about
// fifteen seconds, and a short piece also starts sooner.
const MAX_PIECE = 220;

// splitLong cuts a sentence that is too long at a comma, or at a space.
function splitLong(s: string): string[] {
  const out: string[] = [];
  let rest = s;
  while (rest.length > MAX_PIECE) {
    const head = rest.slice(0, MAX_PIECE);
    let cut = head.lastIndexOf(", ");
    if (cut < MAX_PIECE / 3) {
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

// A sentence as it is shown (to find it on screen) and as it is said (pronunciations applied).
export interface Sentence {
  shown: string;
  said: string;
}

// script is what an answer is read as.
export function script(
  markdown: string,
  maxChars: number,
  lang: Lang,
  words: SpeechWords = ENGLISH_WORDS,
): Sentence[] {
  const text = limitLength(toSpeakable(markdown, words), maxChars, words.rest);
  return sentences(text).map((s) => ({
    shown: s,
    said: pronounce(s, lang),
  }));
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
      const local = Number(b.localService === true) - Number(a.localService === true);
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
    mine.find((v) => uri && v.voiceURI === uri) ||
    mine.find((v) => v.default) ||
    mine[0] ||
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
  voice: unknown;
  onstart: ((e: unknown) => void) | null;
  onend: ((e: unknown) => void) | null;
  onerror: ((e: { error?: string }) => void) | null;
}
export type MakeUtterance = (text: string) => UtteranceLike;

export interface ReadOptions {
  voice: VoiceLike | null;
  lang: Lang;
  rate: number;
  // onSentence is told which sentence the voice starts.
  onSentence?: (i: number) => void;
  // onEnd is told once when the reading ends: finished, stopped, or failed (with the reason).
  onEnd?: (error?: string) => void;
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
    const run = ++this.run;
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
      u.lang = opts.voice?.lang || LOCALE[opts.lang];
      u.voice = opts.voice;
      u.rate = opts.rate;
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
    was?.onEnd?.();
  }

  private finish(error?: string): void {
    const was = this.active;
    this.active = null;
    this.run++;
    was?.onEnd?.(error);
  }
}

// textRange finds a sentence in the text an element shows, ignoring spacing and the interface's
// own buttons, and returns its range; null when it cannot be found (a code block named instead of
// read, a pronunciation, a cut).
export function textRange(root: Node, needle: string): Range | null {
  const doc = root.ownerDocument || (root as Document);
  let flat = "";
  // Each kept character of `flat` maps back to [node, offset].
  const map: { node: Text; offset: number }[] = [];
  const walker = doc.createTreeWalker(root, 4 /* NodeFilter.SHOW_TEXT */);
  let lastSpace = true;
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const text = n as Text;
    if (text.parentElement?.closest("button")) {
      continue;
    }
    const s = text.data;
    for (let i = 0; i < s.length; i++) {
      const space = /\s/.test(s[i]);
      if (space && lastSpace) {
        continue;
      }
      flat += space ? " " : s[i];
      map.push({ node: text, offset: i });
      lastSpace = space;
    }
    if (!lastSpace) {
      // Blocks next to each other are separate words.
      flat += " ";
      map.push({ node: text, offset: s.length });
      lastSpace = true;
    }
  }
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
  return synth?.speaking === true;
}
