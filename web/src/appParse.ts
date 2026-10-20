// Readers of the text the gateway and the person send: durations, progress lines, server-sent
// events, repository URLs and shell lines. No value imports beyond the translations and the
// constants, so each one can be tried on its own.
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { SSE_DATA, SSE_EVENT, SSE_ID } from "./appConst";

// durationOf reads a Go duration string ("24h0m0s") twice over, which is the
// only reason it is one function: the cadence is needed as MILLISECONDS (for the
// clock skew) and as the SHORT form a person typed (for the row's tag).
// Two parsers over the same grammar is two things to keep in step, and the
// normalised form the gateway sends is not what belongs on a tag - "24h0m0s" is
// a machine's spelling of "24h". A cadence that cannot be read comes back with
// the text it arrived as, which is more useful than an empty tag on a row that
// does have one.
const DURATION_UNITS: Record<string, number> = {
  ns: 1e-6,
  us: 1e-3,
  µs: 1e-3,
  ms: 1,
  s: 1000,
  m: 60000,
  h: 3600000,
};
// The units in the order they are tried at a position: a two-letter unit before the letter it
// starts with, so "ms" is never read as "m" followed by "s".
const UNIT_ORDER = ["ns", "us", "µs", "ms", "s", "m", "h"];

const isDigit = (c: string | undefined) =>
  c !== undefined && c >= "0" && c <= "9";

// digitsEnd is where the run of digits that starts at `i` ends.
function digitsEnd(text: string, i: number): number {
  let j = i;
  while (isDigit(text[j])) {
    j++;
  }
  return j;
}

// numberEnd is where the number that starts at `i` ends: its digits, and a fraction when a dot
// followed by a digit comes after them.
function numberEnd(text: string, i: number): number {
  const j = digitsEnd(text, i);
  if (text[j] === "." && isDigit(text[j + 1])) {
    return digitsEnd(text, j + 1);
  }
  return j;
}

// unitAt is the duration unit written at `i`, or '' when there is none.
function unitAt(text: string, i: number): string {
  return UNIT_ORDER.find((u) => text.startsWith(u, i)) ?? "";
}

// durationTerms reads every "<number><unit>" of a duration, left to right, the way the pattern
// /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g finds them: a number with no unit after it is skipped
// and the reading goes on from the next character. It walks the text once per start, with no
// regular expression to backtrack.
function durationTerms(text: string): [string, string][] {
  const terms: [string, string][] = [];
  let i = 0;
  while (i < text.length) {
    const unit = choose(isDigit(text[i]), unitAt(text, numberEnd(text, i)), "");
    if (unit) {
      const end = numberEnd(text, i);
      terms.push([text.slice(i, end), unit]);
      i = end + unit.length;
    } else {
      i++;
    }
  }
  return terms;
}

export function durationOf(every: string): { ms: number; short: string } {
  let ms = 0;
  let short = "";
  for (const [num, unit] of durationTerms(every)) {
    const n = Number.parseFloat(num);
    ms += n * DURATION_UNITS[unit];
    // Only whole units reach the tag: "0h" out of "24h0m0s" is noise, and the
    // unit order the parser walks is the order Go prints, h first.
    if (n > 0) {
      short += num + unit;
    }
  }
  return { ms, short: short || every };
}

// TurnMode is the mode a turn declares: planning, or carrying out a task.
export type TurnMode = "plan" | "task";

// modeOf reads the line auto mode opens a turn with, "mode: plan (question)", and returns the
// mode it picked, or null for any other line.
export function modeOf(text: string): TurnMode | null {
  const m = /^mode: (plan|task) \(/.exec(text);
  if (!m) {
    return null;
  }
  return m[1] as TurnMode;
}

// switchedText is a message sent again in the other mode: a leading /plan or /task (or its
// alias) is replaced, so the gateway runs it in the mode named and records the correction.
export function switchedText(text: string, from: TurnMode): string {
  const rest = text.replace(/^\/(plan|p|task|t)(\s+|$)/i, "");
  return choose(from === "plan", "/task ", "/plan ") + rest;
}

// classifyProgress reads the prefix of a progress line and returns the kind.
const PROGRESS_PREFIXES: [string, string][] = [
  ["thinking:", "thought"],
  ["output (exit", "output"],
  ["approved (all commands", "check"],
  ["running:", "command"],
  ["action:", "reasoning"],
  ["mode:", "analysis"],
];
const PROGRESS_PATTERNS: [RegExp, string][] = [
  [/^(validating|validation)/, "check"],
  [/^(planning|plan ready|subtask)/, "plan"],
  [/^(analysing|understood)/, "analysis"],
  [/^(deciding|round \d+ done)/, "reasoning"],
  [/^(task complete|synthesizing)/, "synthesis"],
];

// isPhaseLine says whether a progress line only names the phase the pipeline is in. It is the
// activity line while the run lasts and nothing more: the trail keeps the work, not its labels.
// The gateway drops the same lines from the steps it keeps (agent.IsPhaseLine), so a conversation
// opened again shows the trail the live view did.
const PHASE_LINE =
  /^(analysing the task\.\.\.|planning\.\.\.|plan ready: \d+ steps?|deciding action \(round \d+\/\d+\)\.\.\.|round \d+ done; |validating with anchor\.\.\.|validation passed; |synthesizing answer\.\.\.|task complete: )/;

export function isPhaseLine(text: string): boolean {
  return PHASE_LINE.test(text);
}

export function classifyProgress(text: string): string | null {
  const byPrefix = PROGRESS_PREFIXES.find(([prefix]) =>
    text.startsWith(prefix),
  );
  if (byPrefix) {
    return byPrefix[1];
  }
  const byPattern = PROGRESS_PATTERNS.find(([re]) => re.test(text));
  if (byPattern) {
    return byPattern[1];
  }
  return null;
}

// repoNameOf is the repository's name in a clone URL: git@github.com:user/repo.git and
// https://github.com/user/repo.git are both "repo".
//
// It reads what /[/:][^/]+\/([^/]+?)(?:\.git)?\/?$/ matches, without the pattern: the last path
// segment (one trailing slash allowed), with one ".git" taken off when something is left, and
// only when an owner segment comes before it - after a slash, or after a colon as in the scp form.
export function repoNameOf(url: string): string {
  const s = url.trim();
  const base = choose(s.endsWith("/"), s.slice(0, -1), s);
  const last = base.lastIndexOf("/");
  const seg = base.slice(last + 1);
  if (last < 0 || !seg) {
    return "";
  }
  const prev = base.lastIndexOf("/", last - 1);
  const owner = base.slice(prev + 1, last);
  const colon = owner.indexOf(":");
  const hasOwner =
    (prev >= 0 && owner.length > 0) || (colon >= 0 && colon < owner.length - 1);
  if (!hasOwner) {
    return "";
  }
  if (seg.length > ".git".length && seg.endsWith(".git")) {
    return seg.slice(0, -".git".length);
  }
  return seg;
}

// trimDashes takes the dashes off both ends of a name.
function trimDashes(s: string): string {
  let start = 0;
  let end = s.length;
  while (start < end && s[start] === "-") {
    start++;
  }
  while (end > start && s[end - 1] === "-") {
    end--;
  }
  return s.slice(start, end);
}

// folderNameOf is the last name of a folder's path ("" when there is none), whatever the separator
// and with or without a trailing one.
export function folderNameOf(path: string): string {
  return path.split(/[/\\]/).findLast(Boolean) ?? "";
}

// slugOf turns a name into a folder name the gateway accepts: a simple name with no path
// separators and no dots.
export function slugOf(name: string): string {
  return trimDashes(
    name
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, "-"),
  );
}

// shellCommandOf reads a line the person typed as a shell command: `!ls -la` or `/shell ls -la`.
// Anything else is a message for the agent, and null.
export function shellCommandOf(text: string): string | null {
  const m = /^(?:!|\/shell\s)\s*(\S[\s\S]*)$/.exec(text.trim());
  if (m) {
    return m[1].trim();
  }
  return null;
}

// splitOutput reads a command's output line ("output (exit N):\n<body>") into the
// exit code and the body, so the trail can show them as a terminal block instead of the
// raw progress text. A line that does not match is returned whole with no code.
export function splitOutput(text: string): {
  exit: number | null;
  body: string;
} {
  const m = /^output \(exit (-?\d+)\):\n?([\s\S]*)$/.exec(text);
  if (m) {
    return { exit: Number(m[1]), body: m[2] };
  }
  return { exit: null, body: text };
}

// The prefix of a command step, before the command itself.
const RUNNING_PREFIX = /^running:\s*/;

// commandOf is the command a "running: ..." line ran.
export function commandOf(text: string): string {
  return text.replace(RUNNING_PREFIX, "");
}

// oneLine folds a progress line into one short row for the trail: a synthesized
// summary is reported as progress too, and it can be a whole Markdown document.
export function oneLine(text: string, max = 160): string {
  const flat = text.replace(/\s+/g, " ").trim();
  if (flat.length > max) {
    return flat.slice(0, max - 1) + "…";
  }
  return flat;
}

// PROGRESS_FORMS are the agent's own phase lines (agent.report in Go), in English on the wire, and
// the format each one is shown with. Anything that is not one of these - a command, the model's
// reasoning, a gateway message - is shown exactly as it arrived.
const PROGRESS_FORMS: [RegExp, string, string[]][] = [
  [/^analysing the task\.\.\.$/, "analysing the task...", []],
  [/^understood: ([\s\S]*)$/, "understood: {x}", ["x"]],
  [/^planning\.\.\.$/, "planning...", []],
  [/^plan ready: (\d+) steps?$/, "plan ready: {n} steps", ["n"]],
  [
    /^deciding action \(round (\d+)\/(\d+)\)\.\.\.$/,
    "deciding action (round {a}/{b})...",
    ["a", "b"],
  ],
  [
    /^round (\d+) done; the model reports more to do$/,
    "round {n} done; the model reports more to do",
    ["n"],
  ],
  [/^validating with anchor\.\.\.$/, "validating with anchor...", []],
  [
    /^validation passed; running final action\.\.\.$/,
    "validation passed; running final action...",
    [],
  ],
  [/^task complete: ([\s\S]*)$/, "task complete: {x}", ["x"]],
  [/^synthesizing answer\.\.\.$/, "synthesizing answer...", []],
  [
    /^subtask (\d+)\/(\d+): ([\s\S]*)$/,
    "subtask {a}/{b}: {x}",
    ["a", "b", "x"],
  ],
  [
    /^waiting for (\d+) background agent\(s\)\.\.\.$/,
    "waiting for {n} background agent(s)...",
    ["n"],
  ],
  [/^asking you to approve: ([\s\S]*)$/, "asking you to approve: {x}", ["x"]],
  [
    /^approved \(all commands allowed in this session\): ([\s\S]*)$/,
    "approved (all commands allowed in this session): {x}",
    ["x"],
  ],
  [
    /^the reply could not be used, asking again: ([\s\S]*)$/,
    "the reply could not be used, asking again: {x}",
    ["x"],
  ],
];

// progressText shows one of the agent's phase lines in the reader's language.
export function progressText(text: string): string {
  for (const [re, format, names] of PROGRESS_FORMS) {
    const m = re.exec(text);
    if (!m) {
      continue;
    }
    const args: Record<string, string> = {};
    names.forEach((n, i) => {
      args[n] = m[i + 1];
    });
    return tf(format, args);
  }
  return text;
}

// SubStep is what a background agent did: a command with what it printed, or a thought.
export interface SubStep {
  agent: string;
  role?: string;
  kind: string;
  text: string;
  exit?: number;
  out?: string;
}

// parseSub reads the line a background agent's command or thought arrives as, after its prefix.
// A line that is not one is null.
export function parseSub(body: string): SubStep | null {
  try {
    const sub = JSON.parse(body) as SubStep;
    return choose(sub.agent, sub, null);
  } catch {
    return null;
  }
}

// subTag is how the agent is named beside what it did: "a1", or "a2 · review".
export function subTag(sub: SubStep): string {
  return sub.agent + choose(sub.role, " · " + sub.role, "");
}

// upgradeMessage shows a stage message of /v1/update/run in the reader's language. The gateway
// writes them in English; the ones that carry a number or a version are matched by shape.
export function upgradeMessage(message = ""): string {
  let x = /^Downloaded (\d+)%$/.exec(message);
  if (x) {
    return tf("Downloaded {p}%", { p: x[1] });
  }
  x = /^Downloading (.+)$/.exec(message);
  if (x) {
    return tf("Downloading {v}", { v: x[1] });
  }
  x = /^Installed (.+)$/.exec(message);
  if (x) {
    return tf("Installed {v}", { v: x[1] });
  }
  return t(message);
}

// sseData is the data line of a server-sent event frame (the last one, when it has several).
export function sseData(frame: string): string {
  let dataLine = "";
  for (const line of frame.split("\n")) {
    if (line.startsWith(SSE_DATA)) {
      dataLine = line.slice(SSE_DATA.length);
    }
  }
  return dataLine;
}

// parseFrame reads one SSE frame (text between two blank lines).
export function parseFrame(raw: string): {
  id: string | null;
  event: string | null;
  data: string | null;
} {
  const lines = raw.split("\n");
  let id: string | null = null;
  let event: string | null = null;
  let data: string | null = null;
  for (const line of lines) {
    if (line.startsWith(SSE_ID)) {
      id = line.slice(SSE_ID.length).trim();
    } else if (line.startsWith(SSE_EVENT)) {
      event = line.slice(SSE_EVENT.length).trim();
    } else if (line.startsWith(SSE_DATA)) {
      data = line.slice(SSE_DATA.length);
    } else {
      /* a comment or a field this client does not use */
    }
  }
  return { id, event, data };
}
