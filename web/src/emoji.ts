// Replaces emoji in an answer with the FluentUI image for it, at the point where the
// character is already a text node in the rendered message.
//
// ## Why this runs on the DOM and not on the Markdown
//
// Doing it in the parser would mean touching the source text, and the message's copy
// payload, the code blocks and the `data-raw` attribute all have to stay byte-identical to
// what the model sent. A substitution on the DOM touches only what is DISPLAYED: code
// blocks are skipped by walking text nodes and refusing to descend into `pre`, and the copy
// payload is never read from the DOM.
//
// ## Why the names are computed rather than looked up
//
// The files are named after the codepoint (`/emoji/1f600.webp`), so the name for a
// character is derived from the character. Only the SET of codepoints that have an asset
// has to ship — see `emoji-codes.ts` — and a character that is not in it is left alone,
// which is the behaviour that matters: the font's own emoji still renders instead of a
// broken image, so this can never make a message worse than not having it.
import { CODEPOINTS } from './emoji-codes'

let set: Set<string> | null = null

/** The codepoint names in use, built once from the generated string. */
function codepoints(): Set<string> {
  if (!set) set = new Set(CODEPOINTS.split(','))
  return set
}

/**
 * The asset name for one emoji string, or null when there is no asset for it.
 *
 * A character from an input method or a picker usually carries a trailing VARIATION
 * SELECTOR-16 (U+FE0F) and one typed by hand usually does not; both are the same glyph and
 * map to the same file, so it is stripped before the lookup.
 *
 * A ZWJ sequence (the rainbow flag, the black cat, "heart on fire") is matched as a WHOLE,
 * never as its parts: FluentUI ships an asset for those sequences (measured: 212 of 1595
 * assets carry a ZWJ), and showing 🏳 plus 🌈 as two pictures would be a picture of
 * something the answer did not say. When no asset exists for the whole sequence, the
 * original characters are left alone — which is also what happens to a country flag, since
 * FluentUI ships no per-country flags at all (8 flag assets: black, white, chequered,
 * triangular, crossed, pirate, rainbow, transgender). A character with no asset keeps
 * rendering as the font's own glyph, so this can never make a message worse than not
 * having the feature.
 */
function assetFor(part: string): string | null {
  const key = partsOf(part)
  if (!key) return null
  return codepoints().has(key) ? key : null
}

/**
 * The lookup key for one matched emoji, or null when the shape is not one this can name.
 *
 * Written to mirror how the generator named the files (`scripts/build-emoji.py`) rather than
 * to re-derive Unicode: a codepoint is 4 hex digits if it is BMP and unpadded otherwise, the
 * presentation selectors are dropped, and the joining characters stay as the generator wrote
 * them. A mismatch here is silent — the key is simply not in the set and the emoji stays a
 * character — so the two sides are kept in step by testing the real assets.
 */
function partsOf(part: string): string | null {
  // No special case for a keycap (`1️⃣` = `1` + FE0F + 20E3): the loop below already produces
  // the key the generator writes (`31-20e3`), because it drops FE0F the same way. A branch
  // that kept it produced `31-fe0f-20e3`, which matched nothing and looked exactly like a
  // character that has no asset.
  const out: string[] = []
  for (const ch of part) {
    const cp = ch.codePointAt(0)
    if (cp === undefined) return null
    if (cp === 0xFE0F || cp === 0xFE0E || cp === 0x200D) {
      // The presentation selector is dropped: a picker-inserted character carries it and a
      // hand-typed one does not, and both name the same glyph — keeping it would give the
      // same emoji two keys and let half of real-world input miss.
      if (cp === 0x200D) out.push('200d')
      continue
    }
    out.push(hex(cp))
  }
  // `fe0f` only ever modifies the character before it; on its own it names nothing. The
  // generator drops it for the same reason, so this has to as well.
  const cleaned = out.filter((p, i) => !(p === 'fe0f' && i > 0))
  return cleaned.length > 0 ? cleaned.join('-') : null
}

// Ranges that are emoji, kept wide on purpose: the codepoint set is what decides whether an
// asset exists, so this only has to avoid walking every ordinary character in the text.
//
// The character class is defined ONCE and reused, because spelling it twice is how this had
// a bug: the second copy (inside the ZWJ chain) had lost `2B00-2BFF`, and `⬛` — the second
// half of the black cat 🐈‍⬛ — lives there, so the sequence was split into three images
// instead of matched as one. Two copies of a range drift; one cannot.
const CLS = '\u{1F000}-\u{1FAFF}\u{2600}-\u{27BF}\u{2B00}-\u{2BFF}\u{FE0F}\u{200D}\u{1F1E6}-\u{1F1FF}\u{2190}-\u{21FF}\u{2900}-\u{297F}\u{3030}\u{303D}\u{3297}\u{3299}\u{00A9}\u{00AE}\u{203C}\u{2049}\u{2122}\u{2139}\u{24C2}\u{25AA}-\u{25FE}'
// Tag characters: a black flag followed by these spells England/Scotland/Wales. Consumed
// into the match so the lookup sees the whole thing; FluentUI ships no tag-sequence asset, so
// the sequence stays characters instead of degrading to the plain black flag that shares its
// base — which would be a picture of a different flag.
const TAGS = '\u{E0020}-\u{E007F}'

/** Whether a text node contains anything worth walking into. */
const EMOJI = new RegExp(`[${CLS}]`, 'u')

// One match is ONE emoji as a reader sees it: an optional keycap, then a base with any
// variation selector and tag characters, then any number of ZWJ-joined parts. Keeping the
// whole sequence in one match is what lets a single image replace it — a family emoji split
// into its members, or a black cat split into a cat and a square, is a different picture.
const SEQUENCE = new RegExp(
  `([0-9#*]\uFE0F?\u20E3` +
  `|([${CLS}])\uFE0F?[${TAGS}]*` +
  `(?:\u200D([${CLS}])\uFE0F?[${TAGS}]*)*` +
  `)`,
  'gu',
)

/**
 * The name a single codepoint contributes.
 *
 * UNPADDED, which is what the generator writes (`format(int(p, 16), 'x')`): `0031` here and
 * `31` in the set is the difference between the keycap `1️⃣` converting and silently not,
 * because a lookup that misses has the same behaviour as a character that has no asset.
 */
function hex(cp: number): string {
  return cp.toString(16)
}

/** Skips these: converting inside them would destroy their meaning or their content. */
const SKIP = new Set(['PRE', 'CODE', 'SCRIPT', 'STYLE', 'TEXTAREA', 'KBD', 'TT'])

/**
 * Converts every emoji in `root` that has an asset, in place.
 *
 * Returns how many were converted, so the caller can assert on it.
 */
export function renderEmoji(root: HTMLElement): number {
  const count = replaceInNode(root)
  // The renderer's own copy button does not print the text, so this only touches what a
  // reader sees. `data-raw` and `data-copy-text` are attributes, never read here.
  return count
}

function replaceInNode(root: HTMLElement): number {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const p = node.parentElement
      if (!p) return NodeFilter.FILTER_REJECT
      if (SKIP.has(p.tagName)) return NodeFilter.FILTER_REJECT
      // A diagram's SVG labels are text too, and mermaid sized its boxes for the glyph it
      // measured: swapping in an image there changes the layout of a drawing.
      if (p.closest('.mermaid-block, .math-inline, .math-block, svg')) {
        return NodeFilter.FILTER_REJECT
      }
      const t = node as Text
      return EMOJI.test(t.data) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT
    },
  })

  const targets: Text[] = []
  for (let n = walker.nextNode(); n; n = walker.nextNode()) targets.push(n as Text)

  let count = 0
  for (const node of targets) {
    const text = node.data
    // A FRESH regex per node: `lastIndex` on a shared /g/ is state that leaks between
    // calls, which is the classic way this converts correctly once and then not at all.
    const re = new RegExp(SEQUENCE.source, 'gu')
    const frag = document.createDocumentFragment()
    let last = 0
    let changed = false
    for (let m = re.exec(text); m; m = re.exec(text)) {
      const part = m[0]
      if (m.index > last) frag.appendChild(document.createTextNode(text.slice(last, m.index)))
      last = m.index + part.length

      const name = assetFor(part)
      if (!name) {
        // No asset for the whole sequence: leave the characters exactly as they were rather
        // than replacing half of it, which would show something the answer did not say.
        frag.appendChild(document.createTextNode(part))
        continue
      }
      const img = document.createElement('img')
      img.src = `/emoji/${name}.webp`
      // The original characters are the accessible name AND what a copy of the visible text
      // produces, so the message still reads correctly where images do not load.
      img.alt = part
      img.className = 'emoji'
      img.setAttribute('loading', 'lazy')
      img.setAttribute('draggable', 'false')
      frag.appendChild(img)
      changed = true
      count += 1
    }
    if (!changed) continue
    if (last < text.length) frag.appendChild(document.createTextNode(text.slice(last)))
    node.replaceWith(frag)
  }
  return count
}
