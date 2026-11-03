// The kinds of memory and the associations between entries, as the person reads them: an entry is a
// fact, an episode, a decision... and it replaces, contradicts or relates to other entries. The
// gateway reads both from the entry's front matter. No value imports: the node tests load it as is.

export const KINDS = [
  "fact",
  "episode",
  "decision",
  "preference",
  "person",
  "procedure",
  "lesson",
] as const;

// KIND_LABELS names each kind in plain words, as keys to translate.
const KIND_LABELS: Record<string, string> = {
  fact: "Facts",
  episode: "What happened",
  decision: "Decisions",
  preference: "Your preferences",
  person: "People",
  procedure: "How it is done",
  lesson: "Project lessons",
};

// kindLabel is the key that names a kind; an unknown or missing kind is "Not sorted yet".
export function kindLabel(kind: string | undefined): string {
  return KIND_LABELS[kind ?? ""] ?? "Not sorted yet";
}

// RELATION_LABELS says how an entry is tied to the ones it names, as keys to translate.
const RELATION_LABELS: Record<string, string> = {
  related: "Related to",
  replaces: "Replaces",
  contradicts: "Contradicts",
  depends_on: "Depends on",
  caused_by: "Comes from",
  part_of: "Part of",
  example_of: "Example of",
};

// relationLabel is the key that says how an entry is tied to another.
export function relationLabel(kind: string): string {
  return RELATION_LABELS[kind] ?? "Related to";
}

// An association an entry states: its kind and the entry it names, as written.
export interface EntryRelation {
  kind: string;
  target: string;
}

// RELATION_KEYS are the front matter keys that state an association: "replaces: "[[Old]]"".
const RELATION_KEYS = new Set([
  "related",
  "replaces",
  "contradicts",
  "depends_on",
  "caused_by",
  "part_of",
  "example_of",
]);

// relationLine reads a line that states an association as its kind and the rest, null for any other
// line: a backlink found in another entry's front matter is told in words, not as a property.
export function relationLine(
  line: string,
): { kind: string; rest: string } | null {
  const colon = line.indexOf(":");
  if (colon < 0) {
    return null;
  }
  const kind = line.slice(0, colon).trim().toLowerCase().replace("-", "_");
  if (!RELATION_KEYS.has(kind)) {
    return null;
  }
  return { kind, rest: line.slice(colon + 1).trim() };
}

// A group of associations of one kind, each with the entry it leads to when there is one.
export interface RelationGroup {
  kind: string;
  items: { target: string; path?: string }[];
}

// groupRelations puts an entry's associations together by kind, in the order they are first stated;
// paths are where its resolved links lead.
export function groupRelations(
  relations: EntryRelation[] | undefined,
  links: { target: string; path?: string }[],
): RelationGroup[] {
  const paths = new Map(links.map((l) => [l.target.toLowerCase(), l.path]));
  const groups: RelationGroup[] = [];
  for (const r of relations ?? []) {
    let g = groups.find((x) => x.kind === r.kind);
    if (!g) {
      g = { kind: r.kind, items: [] };
      groups.push(g);
    }
    g.items.push({
      target: r.target,
      path: paths.get(r.target.toLowerCase()),
    });
  }
  return groups;
}

// FRONT matches the front matter at the top of an entry.
const FRONT = /^---[ \t]*\r?\n[\s\S]*?\r?\n---[ \t]*(\r?\n|$)/;

// stripFront is the entry without its front matter: what the person reads. The facts in it are
// shown apart, in words.
export function stripFront(body: string): string {
  return body.replace(FRONT, "");
}

// authorName is how the author of an entry is shown: the agent by its name.
export function authorName(author: string | undefined): string {
  if (!author) {
    return "";
  }
  if (author.toLowerCase() === "motita") {
    return "Motita";
  }
  return author;
}

// The reorganization of the memory, as the gateway reports it.
export interface ReorgProgress {
  step: string;
  done: number;
  total: number;
}

export interface ReorgReport {
  backup: string;
  entries: number;
  typed: number;
  renamed: number;
  merged: number;
  replaced: number;
  contradictions: number;
  linked: number;
  joined: number;
  unanswered: number;
  used_model: boolean;
}

export interface ReorgStatus {
  running: boolean;
  progress: ReorgProgress;
  report?: ReorgReport;
  error?: string;
}

// STEP_LABELS names the steps of a reorganization, as keys to translate.
const STEP_LABELS: Record<string, string> = {
  backup: "Keeping a copy of your memory…",
  join: "Joining each project's lessons…",
  classify: "Sorting each entry by kind…",
  associate: "Tying related entries together…",
};

// stepLabel is the key that names a step.
export function stepLabel(step: string): string {
  return STEP_LABELS[step] ?? "Working…";
}

// REPORT_LINES are the counts of a report the person is told about, with their keys for many and
// for one, in order.
const REPORT_LINES: [keyof ReorgReport, string, string][] = [
  ["typed", "{n} entries sorted by kind", "1 entry sorted by kind"],
  ["renamed", "{n} entries got a clearer title", "1 entry got a clearer title"],
  ["merged", "{n} duplicates merged", "1 duplicate merged"],
  [
    "replaced",
    "{n} newer facts tied to the ones they replace",
    "1 newer fact tied to the one it replaces",
  ],
  ["contradictions", "{n} contradictions marked", "1 contradiction marked"],
  ["linked", "{n} new associations", "1 new association"],
  [
    "joined",
    "{n} project lesson lists moved into their project",
    "1 project lesson list moved into its project",
  ],
];

// reportLines are the counts of a report worth telling, as keys and numbers.
export function reportLines(r: ReorgReport): { key: string; n: number }[] {
  return REPORT_LINES.filter(([field]) => (r[field] as number) > 0).map(
    ([field, many, one]) => {
      const n = r[field] as number;
      if (n === 1) {
        return { key: one, n };
      }
      return { key: many, n };
    },
  );
}
