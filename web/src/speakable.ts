// What is worth reading out of an answer, and how it is cut into the sentences the voice says one
// by one. It needs neither a voice nor a page, so it is tested as it is; speech.ts re-exports it.
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
