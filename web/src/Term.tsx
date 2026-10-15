// The terminal drawer's text: syntax colouring and the typewriter that brings it in.
//
// The command is highlighted as bash by the SAME highlight.js the answers use, loaded on
// demand: the grammar is not paid for until a command is actually shown. What a command
// PRINTS is not one language, so it is coloured by what each line says instead of by a
// grammar: a failure is red, a pass is green, a `file.go:42` location is underlined, a
// diff has its own colours, and JSON goes through the JSON grammar.
//
// Everything reaching `dangerouslySetInnerHTML` is escaped exactly once, here or by
// highlight.js, so the text on screen is the text that was printed.
//
// THE TYPEWRITER works on the DOM that colouring produced, not on a string: the full HTML
// is mounted, and the text nodes are then cut to the first N characters and grown. Cutting
// text nodes leaves every <span class="hljs-..."> in place, so the colours are there from the
// first letter and nothing has to be re-highlighted per keystroke.
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "preact/hooks";
import DOMPurify from "dompurify";
import { t } from "./i18n";
import { choose } from "./choose";

export function esc(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// A `path/file.ext:line` or `path/file.ext:line:col` location, which is what a compiler or a
// test names when it says WHERE. Escaped text is matched, and none of the characters a
// location is made of is one that escaping changes.
//
// Locations are found by a scan, not by one global pattern: a pattern that opens with a run of
// path characters is retried from every character of a long run that holds no location, which
// is quadratic. The scan reads each run once and finds exactly what
// /[\w./-]+\.\w{1,6}:\d+(?::\d+)?/g found: a run of path characters that ends in `.ext` with
// something before the dot, right before `:line` and an optional `:col`.
const PATH_CHAR = /[\w./-]/;
const EXTENSION = /\.\w{1,6}$/;
const LINE_COL = /:\d+(?::\d+)?/y;

// pathRunEnd is where the run of path characters that starts at `i` ends.
function pathRunEnd(s: string, i: number): number {
  let j = i;
  while (j < s.length && PATH_CHAR.test(s[j])) {
    j++;
  }
  return j;
}

// locationEnd is where the location that starts at `i` ends, or -1 when none starts there. A run
// that does not hold one from its first character holds none from a later one either: the run, its
// extension and what follows it are the same, and a later start only has less before the dot.
function locationEnd(s: string, i: number, runEnd: number): number {
  const ext = EXTENSION.exec(s.slice(i, runEnd));
  if (!ext || ext.index === 0) {
    return -1;
  }
  LINE_COL.lastIndex = runEnd;
  const tail = LINE_COL.exec(s);
  if (!tail) {
    return -1;
  }
  return runEnd + tail[0].length;
}

// markLocations wraps every location in the line in a `to-loc` span.
function markLocations(s: string): string {
  let out = "";
  let copied = 0;
  let i = 0;
  while (i < s.length) {
    if (PATH_CHAR.test(s[i])) {
      const runEnd = pathRunEnd(s, i);
      const end = locationEnd(s, i, runEnd);
      if (end < 0) {
        i = runEnd;
      } else {
        out +=
          s.slice(copied, i) +
          '<span class="to-loc">' +
          s.slice(i, end) +
          "</span>";
        copied = end;
        i = end;
      }
    } else {
      i++;
    }
  }
  return out + s.slice(copied);
}

// A failure line. Leading blanks are skipped by `^\s*`; "exit status N" anywhere in the line
// counts too, reached through a first non-blank so the two runs of blanks cannot trade.
const FAIL =
  /^\s*(?:--- FAIL|FAIL\b|panic:|fatal:|error\b|Error\b|ERROR\b|E\d{3,}|\S.*\bexit status [1-9]|\bexit status [1-9])/;
const PASS = /^\s*(ok\b|--- PASS|PASS\b|OK\b|success\b)/;
const WARN = /^\s*(warn(ing)?\b|WARN\b|deprecated\b)/i;

// diffHeadClass is the colour of a diff's file header: red when it reports a failure.
function diffHeadClass(line: string): string {
  if (line.startsWith("---") && FAIL.test(line)) {
    return "to-err";
  }
  return "to-diffhead";
}

// lineClass names the colour a whole output line takes, or '' for the plain one.
function lineClass(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) {
    return diffHeadClass(line);
  }
  if (line.startsWith("@@")) {
    return "to-hunk";
  }
  if (line.startsWith("+")) {
    return "to-add";
  }
  if (line.startsWith("-")) {
    return "to-del";
  }
  if (FAIL.test(line)) {
    return "to-err";
  }
  if (PASS.test(line)) {
    return "to-ok";
  }
  if (WARN.test(line)) {
    return "to-warn";
  }
  return "";
}

/** Colours a command's output line by line. Returns HTML, escaped. */
export function colorizeOutput(text: string): string {
  return text
    .split("\n")
    .map((line) => {
      const cls = lineClass(line);
      const html = markLocations(esc(line));
      if (cls) {
        return '<span class="' + cls + '">' + html + "</span>";
      }
      return html;
    })
    .join("\n");
}

// looksLikeJSON is deliberately narrow: a `{` or `[` start AND a matching end. A log line that
// merely begins with a bracket ("[ok] built") must not be sent to a grammar that would
// colour it as nonsense.
function looksLikeJSON(text: string): boolean {
  const trimmed = text.trim();
  if (trimmed.startsWith("{") && trimmed.endsWith("}")) {
    return true;
  }
  return (
    trimmed.startsWith("[") && trimmed.endsWith("]") && trimmed.includes('"')
  );
}

// ── The typewriter ────────────────────────────────────────────────────────────

// The reveal is NOT switched off by `prefers-reduced-motion`. It moves nothing: it is the text
// arriving in order, which is the whole point of the effect and what was asked for. It was
// switched off at first, and on a machine that asks for less motion the terminal then showed
// every command at once - which read as "there is no typewriter". Only the blinking cursor
// respects the preference (see index.css).

interface Slice {
  node: Text;
  full: string;
  start: number;
}

// collect lists the text nodes under `root` with the offset each one starts at.
function collect(root: HTMLElement): { slices: Slice[]; total: number } {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const slices: Slice[] = [];
  let total = 0;
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const node = n as Text;
    slices.push({ node, full: node.data, start: total });
    total += node.data.length;
  }
  return { slices, total };
}

// The UTF-16 range of the first half of a surrogate pair.
const HIGH_SURROGATE_MIN = 0xd800;
const HIGH_SURROGATE_MAX = 0xdbff;
// The first code point outside the Basic Multilingual Plane: one written as a surrogate pair.
const SUPPLEMENTARY_MIN = 0x10000;

// reveal shows the first `n` characters of the whole text and hides the rest.
function reveal(slices: Slice[], n: number) {
  for (const s of slices) {
    let k = Math.max(0, Math.min(s.full.length, n - s.start));
    // Never cut between the two halves of a surrogate pair: that draws a broken glyph.
    // codePointAt reads the whole pair when the cut falls inside one, and a lone first half as
    // itself: either way the character before the cut starts with a first half.
    if (k > 0 && k < s.full.length) {
      // 0 < k - 1 < length here, so there is a code point to read.
      const c = s.full.codePointAt(k - 1) as number;
      if (
        c >= SUPPLEMENTARY_MIN ||
        (c >= HIGH_SURROGATE_MIN && c <= HIGH_SURROGATE_MAX)
      ) {
        k--;
      }
    }
    if (s.node.data.length !== k) {
      s.node.data = s.full.slice(0, k);
    }
  }
}

// What the colouring emits: spans with a class, nothing else.
const TERM_PURIFY = { ALLOWED_TAGS: ["span"], ALLOWED_ATTR: ["class"] };

interface TypedProps {
  html: string;
  as?: "span" | "pre" | "div";
  class?: string;
  // cps overrides the speed, in characters per second.
  cps?: number;
  // onTick fires whenever the text grew: the drawer uses it to stay at the bottom.
  onTick?: () => void;
  onDone?: () => void;
  // instant shows the whole text at once: for what the reader has already seen.
  instant?: boolean;
  // boost multiplies the speed. The drawer raises it when commands arrive faster than they can
  // be typed, so the terminal catches up by typing FASTER, never by skipping the typing.
  boost?: number;
}

/**
 * Mounts `html` and types it in. When `html` changes to a different rendering of the SAME text
 * (the plain command replaced by its coloured version once the grammar arrives), what was
 * already typed stays typed and the typing goes on from there.
 *
 * The speed is chosen so a short line is unhurried and a long output never takes more than
 * about a second and a quarter: 70 to 900 characters per second.
 */
// The typing speed's bounds, in characters per second, and the time a long text is typed in.
const TYPE_MIN_CPS = 70;
const TYPE_MAX_CPS = 900;
const TYPE_TARGET_S = 1.2;

export function Typed({
  html,
  as = "span",
  class: cls,
  cps,
  onTick,
  onDone,
  instant,
  boost = 1,
}: TypedProps) {
  const ref = useRef<HTMLElement>(null);
  const shown = useRef(0);
  const finished = useRef(false);
  // The text nodes AS MOUNTED, kept for as long as `html` is the same. They are read once per
  // rendering: once typing has cut them down, reading them again would take the cut text for
  // the whole text, and a change of speed or of `instant` half way would finish on a stub.
  const mounted = useRef<{
    html: string;
    slices: Slice[];
    total: number;
  } | null>(null);
  // The callbacks are read through a ref so a parent re-render does not restart the typing.
  const cb = useRef({ onTick, onDone });
  cb.current = { onTick, onDone };

  useLayoutEffect(() => {
    // A layout effect runs once the element is mounted.
    const el = ref.current as HTMLElement;
    if (mounted.current?.html !== html) {
      mounted.current = { html, ...collect(el) };
    }
    const { slices, total } = mounted.current;
    const finish = () => {
      el.classList.remove("is-typing");
      if (!finished.current) {
        finished.current = true;
        cb.current.onDone?.();
      }
    };
    if (instant) {
      reveal(slices, total);
      shown.current = total;
      cb.current.onTick?.();
      finish();
      return undefined;
    }
    if (shown.current < total) {
      finished.current = false;
    }
    reveal(slices, shown.current);
    cb.current.onTick?.();
    if (shown.current >= total) {
      finish();
      return undefined;
    }
    el.classList.add("is-typing");
    const rate =
      (cps ??
        Math.min(TYPE_MAX_CPS, Math.max(TYPE_MIN_CPS, total / TYPE_TARGET_S))) *
      boost;
    const base = shown.current;
    const t0 = performance.now();
    let raf = 0;
    const step = (now: number) => {
      const n = Math.min(total, base + Math.floor(((now - t0) / 1000) * rate));
      if (n !== shown.current) {
        shown.current = n;
        reveal(slices, n);
        cb.current.onTick?.();
      }
      if (n >= total) {
        finish();
      } else {
        raf = requestAnimationFrame(step);
      }
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [html, cps, instant, boost]);

  const Tag = as as "span";
  // The markup is ours (escaped text and highlight.js output), and goes through DOMPurify anyway:
  // only span/class survive, which is all the colouring uses.
  return (
    <Tag
      ref={ref as never}
      class={cls}
      dangerouslySetInnerHTML={{
        __html: DOMPurify.sanitize(html, TERM_PURIFY),
      }}
    />
  );
}

// ── Pieces ────────────────────────────────────────────────────────────────────

interface PieceProps {
  onTick?: () => void;
  onDone?: () => void;
  instant?: boolean;
  boost?: number;
}

/** One command line: the prompt, then the command coloured as bash and typed in. */
export function TermCommand({
  cmd,
  onTick,
  onDone,
  instant,
  boost,
}: { cmd: string } & PieceProps) {
  // Plain and escaped first, so the line is readable before the grammar has loaded.
  const [html, setHtml] = useState(() => esc(cmd));
  useEffect(() => {
    let live = true;
    import("./highlight").then(
      (m) => {
        if (live) {
          setHtml(m.highlight(cmd, "bash"));
        }
      },
      () => {
        /* the escaped text stays: colour is a courtesy */
      },
    );
    return () => {
      live = false;
    };
  }, [cmd]);
  return (
    <div class="term-line">
      <span class="term-prompt">$</span>{" "}
      <Typed
        as="span"
        class="term-code"
        html={html}
        instant={instant}
        boost={boost}
        onTick={onTick}
        onDone={onDone}
      />
    </div>
  );
}

/** What a command printed, coloured and typed in. */
export function TermOutput({
  text,
  onTick,
  onDone,
  instant,
  boost,
}: { text: string } & PieceProps) {
  const json = looksLikeJSON(text);
  const [html, setHtml] = useState(() => colorizeOutput(text));
  useEffect(() => {
    let live = true;
    if (json) {
      import("./highlight").then(
        (m) => {
          if (live) {
            setHtml(m.highlight(text, "json"));
          }
        },
        () => {
          /* keep the line colouring */
        },
      );
    }
    return () => {
      live = false;
    };
  }, [text, json]);
  return (
    <Typed
      as="pre"
      class="term-output"
      html={html}
      instant={instant}
      boost={boost}
      onTick={onTick}
      onDone={onDone}
    />
  );
}

export interface ShellStatusView {
  label: string;
  // '' for a success, 'bad' for a failure, 'running' while the command has not answered.
  kind: "" | "bad" | "running";
}

// EntryPhase is where a history entry is in the queue the drawer plays:
//   'wait' - not its turn (or the drawer is closed): nothing is drawn, so nothing has been typed;
//   'live' - being typed now;
//   'done' - already seen: drawn whole, at once.
export type EntryPhase = "wait" | "live" | "done";

/**
 * One command of the history. The three parts come in ONE AFTER THE OTHER, the way a real
 * terminal would show them: the command is typed, then what it printed, then how it ended.
 * The output can arrive long after the command; it waits for the command to finish typing.
 *
 * `onFinished` fires once the entry has a final status and all of it is on screen, which is
 * what lets the drawer start the next one.
 */
export function TermEntry({
  cmd,
  out,
  status,
  phase,
  boost,
  onTick,
  onFinished,
}: {
  cmd: string;
  out: string;
  status: ShellStatusView;
  phase: EntryPhase;
  boost?: number;
  onFinished?: () => void;
} & Pick<PieceProps, "onTick">) {
  const [cmdDone, setCmdDone] = useState(false);
  const [outDone, setOutDone] = useState(false);
  const onCmdDone = useCallback(() => setCmdDone(true), []);
  const onOutDone = useCallback(() => setOutDone(true), []);
  if (phase === "wait") {
    return null;
  }
  const instant = phase === "done";
  const showOut = !!out && (instant || cmdDone);
  const outPending = !!out && !instant && !outDone;
  const showStatus = instant || (cmdDone && !outPending);
  return (
    <div class="term-entry">
      <TermCommand
        cmd={cmd}
        instant={instant}
        boost={boost}
        onTick={onTick}
        onDone={onCmdDone}
      />
      {showOut && (
        <TermOutput
          text={out}
          instant={instant}
          boost={boost}
          onTick={onTick}
          onDone={onOutDone}
        />
      )}
      {showStatus && (
        <TermStatus
          status={status}
          instant={instant}
          boost={boost}
          onTick={onTick}
          onFinished={onFinished}
        />
      )}
    </div>
  );
}

/** How a command ended, typed in after its output; a running one keeps the caret blinking. */
function TermStatus({
  status,
  instant,
  boost,
  onTick,
  onFinished,
}: {
  status: ShellStatusView;
  instant: boolean;
  boost?: number;
  onFinished?: () => void;
} & Pick<PieceProps, "onTick">) {
  const onDone = useCallback(() => {
    if (status.kind !== "running") {
      onFinished?.();
    }
  }, [status.kind, onFinished]);
  return (
    <div class={`term-status${choose(status.kind, " " + status.kind, "")}`}>
      <Typed
        key={status.label}
        html={esc(status.label)}
        instant={instant}
        boost={boost}
        onTick={onTick}
        onDone={onDone}
      />
      {status.kind === "running" && <span class="term-caret" />}
    </div>
  );
}

/** The screen before anything ran. */
export function TermEmpty({ onTick }: Pick<PieceProps, "onTick">) {
  return (
    <div class="term-empty">
      <Typed html={t("no commands yet")} onTick={onTick} />
      <span class="term-caret" />
    </div>
  );
}
