// The note vault's types and the small pure helpers around them: the folder tree, the file name a
// title makes and the wiki links turned into links the Markdown view can draw. No value imports: the
// node tests load this file as is.

export type Api = (path: string, init?: RequestInit) => Promise<Response>;

export interface NoteItem {
  path: string;
  title: string;
  size: number;
  modified: string;
}

// A [[wiki link]] of a note: path is the note it leads to, absent while there is none.
export interface NoteLink {
  target: string;
  alias?: string;
  heading?: string;
  path?: string;
}

export interface NoteBacklink extends NoteItem {
  // The first line that holds the link.
  context: string;
}

export interface NoteView extends NoteItem {
  body: string;
  links: NoteLink[];
  backlinks: NoteBacklink[];
}

export interface NoteHit {
  path: string;
  title: string;
  line: number;
  snippet: string;
}

// One level of the tree: the folders below it and the notes in it.
export interface NoteFolder {
  name: string;
  path: string;
  folders: NoteFolder[];
  notes: NoteItem[];
}

// buildTree groups the notes (sorted by path, as the gateway sends them) into folders.
export function buildTree(notes: NoteItem[]): NoteFolder {
  const root: NoteFolder = { name: "", path: "", folders: [], notes: [] };
  for (const n of notes) {
    const parts = n.path.split("/");
    parts.pop();
    let at = root;
    for (const part of parts) {
      const path = at.path ? at.path + "/" + part : part;
      let next = at.folders.find((f) => f.path === path);
      if (!next) {
        next = { name: part, path, folders: [], notes: [] };
        at.folders.push(next);
      }
      at = next;
    }
    at.notes.push(n);
  }
  return root;
}

// folderOf is the folder of a note path, "" at the top.
export function folderOf(path: string): string {
  return path.slice(0, Math.max(0, path.lastIndexOf("/")));
}

// noteFileName makes the vault path of a title typed by the person: no characters a file name
// cannot hold, one ".md", inside folder (when there is one). A title may name sub-folders with "/".
export function noteFileName(folder: string, title: string): string {
  const cleaned = title
    .split("/")
    .map((p) => p.replace(/[\\:*?"<>|#^[\]]/g, "").trim())
    .filter((p) => p !== "" && p !== "." && p !== ".." && !p.startsWith("."))
    .join("/")
    .replace(/\.md$/i, "");
  if (cleaned === "") {
    return "";
  }
  const dir = folder.replace(/^\/+|\/+$/g, "");
  return (dir ? dir + "/" : "") + cleaned + ".md";
}

// WIKI matches a code span or fence (left alone) or a [[wiki link]].
const WIKI = /(```[\s\S]*?```|`[^`\n]*`)|!?\[\[([^[\]\n]+)\]\]/g;

// splitWiki takes "target#heading|alias" apart.
function splitWiki(inner: string): { target: string; text: string } {
  const bar = inner.indexOf("|");
  const left = bar < 0 ? inner : inner.slice(0, bar);
  const alias = bar < 0 ? "" : inner.slice(bar + 1).trim();
  const target = left.split("#")[0].trim().replace(/\.md$/i, "");
  return { target, text: alias || left.trim() };
}

// wikiToMarkdown turns the [[wiki links]] of a note into Markdown links: "#note:<path>" for a
// note that exists and "#new:<target>" for one that does not yet. links is what the gateway
// resolved. Links inside code stay as written.
export function wikiToMarkdown(body: string, links: NoteLink[]): string {
  const resolved = new Map<string, string>();
  for (const l of links) {
    if (l.path) {
      resolved.set(l.target.toLowerCase(), l.path);
    }
  }
  return body.replace(WIKI, (all, code, inner) => {
    if (code) {
      return all;
    }
    const { target, text } = splitWiki(inner);
    if (target === "") {
      return all;
    }
    const path = resolved.get(target.toLowerCase());
    const href = path
      ? "#note:" + encodeURIComponent(path)
      : "#new:" + encodeURIComponent(target);
    return "[" + text.replace(/[[\]]/g, "") + "](" + href + ")";
  });
}

// NoteTarget is where a link of the preview leads.
export type NoteTarget =
  | { kind: "note"; path: string }
  | { kind: "new"; target: string };

// parseNoteHref reads the href wikiToMarkdown wrote, or null for any other link.
export function parseNoteHref(
  href: string | null | undefined,
): NoteTarget | null {
  if (href?.startsWith("#note:")) {
    return { kind: "note", path: decodeURIComponent(href.slice(6)) };
  }
  if (href?.startsWith("#new:")) {
    return { kind: "new", target: decodeURIComponent(href.slice(5)) };
  }
  return null;
}

// One note of the link graph, and how many notes link to it (in) and how many it links to (out).
export interface GraphNode {
  path: string;
  title: string;
  in: number;
  out: number;
}

export interface GraphEdge {
  from: string;
  to: string;
}

export interface NoteGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  // Links to notes that do not exist yet: counted, not drawn.
  missing: number;
}

export interface PlacedNode extends GraphNode {
  x: number;
  y: number;
}

const LAYOUT_STEPS = 140;
const MARGIN = 28;
// The layout: spring length against the area, pull to the middle, and the longest step per round.
const SPRING = 0.6;
const GRAVITY = 0.05;
const MAX_SHIFT = 12;

// layoutGraph places the notes in a width x height box with a small force simulation: every pair
// pushes apart, every link pulls together, and the whole drifts to the middle. It starts from a
// circle and cools in a fixed number of steps, so the same vault always draws the same picture.
export function layoutGraph(
  graph: NoteGraph,
  width: number,
  height: number,
): PlacedNode[] {
  const n = graph.nodes.length;
  const cx = width / 2;
  const cy = height / 2;
  const spread = Math.min(width, height) / 2 - MARGIN;
  const placed: PlacedNode[] = graph.nodes.map((node, i) => ({
    ...node,
    x: cx + spread * Math.cos((2 * Math.PI * i) / n),
    y: cy + spread * Math.sin((2 * Math.PI * i) / n),
  }));
  const at = new Map(placed.map((p) => [p.path, p]));
  const k = Math.sqrt((width * height) / n) * SPRING;
  for (let step = 0; step < LAYOUT_STEPS; step++) {
    const cool = 1 - step / LAYOUT_STEPS;
    const move = new Map(placed.map((p) => [p, { x: 0, y: 0 }]));
    for (const a of placed) {
      const da = move.get(a) as { x: number; y: number };
      for (const b of placed) {
        const dx = a.x - b.x;
        const dy = a.y - b.y;
        const d2 = Math.max(dx * dx + dy * dy, 0.01);
        const push = (k * k) / d2;
        da.x += dx * push;
        da.y += dy * push;
      }
      da.x += (cx - a.x) * GRAVITY;
      da.y += (cy - a.y) * GRAVITY;
    }
    for (const e of graph.edges) {
      const a = at.get(e.from) as PlacedNode;
      const b = at.get(e.to) as PlacedNode;
      const dx = a.x - b.x;
      const dy = a.y - b.y;
      const pull = Math.sqrt(dx * dx + dy * dy) / k;
      const da = move.get(a) as { x: number; y: number };
      const db = move.get(b) as { x: number; y: number };
      da.x -= dx * pull;
      da.y -= dy * pull;
      db.x += dx * pull;
      db.y += dy * pull;
    }
    for (const p of placed) {
      const m = move.get(p) as { x: number; y: number };
      const len = Math.max(Math.sqrt(m.x * m.x + m.y * m.y), 0.01);
      const shift = Math.min(len, MAX_SHIFT * cool + 0.5);
      p.x = Math.min(
        width - MARGIN,
        Math.max(MARGIN, p.x + (m.x / len) * shift),
      );
      p.y = Math.min(
        height - MARGIN,
        Math.max(MARGIN, p.y + (m.y / len) * shift),
      );
    }
  }
  return placed;
}

export interface PlacedNode3D extends GraphNode {
  x: number;
  y: number;
  z: number;
}

// The 3D layout: as many rounds as keep a big vault quick, and the pull to the middle.
const LAYOUT_3D_WORK = 60000;
const LAYOUT_3D_STEPS = 120;
const GRAVITY_3D = 0.04;
// SPREAD fits the layout to the radius: the share of notes that lands inside it, so a few far
// notes do not squeeze the rest into the middle, and how far past it the others may stay.
const SPREAD = { share: 0.85, overshoot: 1.15 };

// GOLDEN_ANGLE, in radians, spreads points evenly round a sphere: π·(3 − √5).
export const GOLDEN_ANGLE = 2.399963229728653;

// fitToRadius centres the notes and scales them so most sit within radius, however the forces
// settled; the few farther out are brought in to just past it.
function fitToRadius(placed: PlacedNode3D[], radius: number): void {
  const mid = { x: 0, y: 0, z: 0 };
  for (const p of placed) {
    mid.x += p.x / placed.length;
    mid.y += p.y / placed.length;
    mid.z += p.z / placed.length;
  }
  const dists = placed
    .map((p) => Math.hypot(p.x - mid.x, p.y - mid.y, p.z - mid.z))
    .sort((a, b) => a - b);
  const far = Math.max(
    dists[Math.floor((placed.length - 1) * SPREAD.share)],
    1e-6,
  );
  for (const p of placed) {
    p.x = ((p.x - mid.x) / far) * radius;
    p.y = ((p.y - mid.y) / far) * radius;
    p.z = ((p.z - mid.z) / far) * radius;
    const out = Math.hypot(p.x, p.y, p.z) / (radius * SPREAD.overshoot);
    if (out > 1) {
      p.x /= out;
      p.y /= out;
      p.z /= out;
    }
  }
}

// linkCounts is how many links each note has, in and out.
function linkCounts(edges: GraphEdge[]): Map<string, number> {
  const degree = new Map<string, number>();
  for (const e of edges) {
    degree.set(e.from, (degree.get(e.from) ?? 0) + 1);
    degree.set(e.to, (degree.get(e.to) ?? 0) + 1);
  }
  return degree;
}

// layoutGraph3D places the notes in space around the origin, within about radius: they start
// spread evenly over a sphere, then every pair pushes apart, every link pulls together and the
// whole drifts to the middle. A link to a much-linked note pulls less, so a guide that links every
// chapter does not gather them all round it. Big vaults get fewer rounds; the same vault always
// lands the same.
export function layoutGraph3D(
  graph: NoteGraph,
  radius: number,
): PlacedNode3D[] {
  const n = graph.nodes.length;
  const placed: PlacedNode3D[] = graph.nodes.map((node, i) => {
    const y = 1 - ((i + 0.5) / n) * 2;
    const r = Math.sqrt(1 - y * y);
    return {
      ...node,
      x: Math.cos(GOLDEN_ANGLE * i) * r * radius,
      y: y * radius,
      z: Math.sin(GOLDEN_ANGLE * i) * r * radius,
    };
  });
  const at = new Map(placed.map((p) => [p.path, p]));
  const degree = linkCounts(graph.edges);
  const k = (radius / Math.cbrt(n)) * SPRING * 2;
  const steps = Math.min(LAYOUT_3D_STEPS, Math.ceil(LAYOUT_3D_WORK / (n * n)));
  for (let step = 0; step < steps; step++) {
    const cool = 1 - step / steps;
    const move = new Map(placed.map((p) => [p, { x: 0, y: 0, z: 0 }]));
    for (const a of placed) {
      const da = move.get(a) as { x: number; y: number; z: number };
      for (const b of placed) {
        const dx = a.x - b.x;
        const dy = a.y - b.y;
        const dz = a.z - b.z;
        const push = (k * k) / Math.max(dx * dx + dy * dy + dz * dz, 0.01);
        da.x += dx * push;
        da.y += dy * push;
        da.z += dz * push;
      }
      da.x -= a.x * GRAVITY_3D;
      da.y -= a.y * GRAVITY_3D;
      da.z -= a.z * GRAVITY_3D;
    }
    for (const e of graph.edges) {
      const a = at.get(e.from) as PlacedNode3D;
      const b = at.get(e.to) as PlacedNode3D;
      const dx = a.x - b.x;
      const dy = a.y - b.y;
      const dz = a.z - b.z;
      const hub = Math.max(
        degree.get(e.from) as number,
        degree.get(e.to) as number,
      );
      const pull = Math.hypot(dx, dy, dz) / k / Math.sqrt(hub);
      const da = move.get(a) as { x: number; y: number; z: number };
      const db = move.get(b) as { x: number; y: number; z: number };
      da.x -= dx * pull;
      da.y -= dy * pull;
      da.z -= dz * pull;
      db.x += dx * pull;
      db.y += dy * pull;
      db.z += dz * pull;
    }
    for (const p of placed) {
      const m = move.get(p) as { x: number; y: number; z: number };
      const len = Math.max(Math.hypot(m.x, m.y, m.z), 0.01);
      const shift = Math.min(len, (radius / 10) * cool + 0.5);
      p.x += (m.x / len) * shift;
      p.y += (m.y / len) * shift;
      p.z += (m.z / len) * shift;
    }
  }
  fitToRadius(placed, radius);
  return placed;
}

// plainWiki reads a line of a note as text: the marks of a list, quote, table or heading at its
// start go, a [[link]] becomes the words it shows (its alias, or the name of the note it leads to)
// and the marks of emphasis and code go.
export function plainWiki(line: string): string {
  const [head, ...links] = line.replace(/^[\s>|#-]+/, "").split("[[");
  const words = links.map((part) => {
    const end = part.indexOf("]]");
    if (end < 0) {
      return "[[" + part;
    }
    const [target, alias] = part.slice(0, end).split("|", 2);
    const shown = alias ?? (target.split("/").pop() as string);
    return shown.trim() + part.slice(end + 2);
  });
  return [head, ...words].join("").replace(/[*_`]/g, "").trim();
}

// noteTrail is the folders a note sits in, outermost first: "a/b/c.md" is ["a", "b"].
export function noteTrail(path: string): string[] {
  return path.split("/").slice(0, -1);
}

// groupKey names the part of the vault a note belongs to: in a manual, the top folder of the
// project a chapter documents ("internal › gateway" goes with "internal"); elsewhere the folder
// the note is in. Notes at the top have "".
export function groupKey(path: string): string {
  const title = path.slice(path.lastIndexOf("/") + 1);
  const key = noteTrail(path).join("/");
  if (key.endsWith(" - Manual")) {
    return title.split(" › ")[0].replace(/\.md$/, "");
  }
  return key;
}

// MapGroup is one part of the vault on the map's legend, with how many notes it has.
export interface MapGroup {
  key: string;
  count: number;
}

// mapGroups lists the parts of the vault the notes belong to, the biggest first.
export function mapGroups(nodes: GraphNode[]): MapGroup[] {
  const count = new Map<string, number>();
  for (const n of nodes) {
    const key = groupKey(n.path);
    count.set(key, (count.get(key) ?? 0) + 1);
  }
  return [...count]
    .map(([key, c]) => ({ key, count: c }))
    .sort((a, b) => b.count - a.count || a.key.localeCompare(b.key));
}

// noteColours gives every note the colour of its part of the vault, as an index into a palette of
// colours: the biggest parts get the first ones, so the parts the legend names never share one.
export function noteColours(
  nodes: GraphNode[],
  colours: number,
): Map<string, number> {
  const rank = new Map(mapGroups(nodes).map((g, i) => [g.key, i % colours]));
  return new Map(
    nodes.map((n) => [n.path, rank.get(groupKey(n.path)) as number]),
  );
}

// START_NOTE is the first note of a guide the scan writes; a folder lists it before the rest.
export const START_NOTE = "Start here";

// byReadingOrder sorts the notes of a folder for reading: "Start here" first, then by title.
export function byReadingOrder(a: NoteItem, b: NoteItem): number {
  const first = Number(b.title === START_NOTE) - Number(a.title === START_NOTE);
  return first || a.title.localeCompare(b.title);
}

// NoteScan is what scanning the sessions and projects into notes reports.
export interface NoteScan {
  // false when only a first-use scan was asked for and the vault did not need one.
  ran: boolean;
  // Sessions the model summarised in this scan.
  summarized: number;
  // Projects whose architecture note was written, and those the model explained in this scan.
  mapped: number;
  // Notes of the projects' manuals: one per folder.
  documented: number;
  explained: number;
  projects: number;
  sessions: number;
  created: number;
  updated: number;
  unchanged: number;
  skipped: number;
}

// ScanOptions: first only fills a vault that was never filled; summarize also asks the model for a
// summary of each long session, which sends its text to the provider.
export interface ScanOptions {
  first?: boolean;
  summarize?: boolean;
  // How many folders of the manuals the model may explain in this scan.
  modules?: number;
}

// ScanProgress is how far the scan running in the background got, and how it ended.
export interface ScanProgress {
  running: boolean;
  // "sessions" while the session notes are written, then "projects".
  phase?: string;
  project?: string;
  done: number;
  total: number;
  chapters: number;
  chapter_total: number;
  result?: NoteScan;
  error?: string;
}

// SCAN_STARTED_EVENT is dispatched on window when a background scan starts, so the sidebar
// follows it.
export const SCAN_STARTED_EVENT = "motita-scan-started";

// scanProgress asks how far the background scan got.
export async function scanProgress(api: Api): Promise<ScanProgress> {
  const res = await api("/v1/notes/scan");
  if (!res.ok) {
    throw new Error("HTTP " + res.status);
  }
  return (await res.json()) as ScanProgress;
}

// postScan asks the gateway for a scan; in the background it answers the progress at once.
async function postScan(
  api: Api,
  opts: ScanOptions,
  background: boolean,
): Promise<unknown> {
  const params = new URLSearchParams();
  if (opts.first) {
    params.set("first", "1");
  }
  if (opts.summarize) {
    params.set("summarize", "1");
  }
  if (opts.modules) {
    params.set("modules", String(opts.modules));
  }
  if (background) {
    params.set("background", "1");
  }
  let path = "/v1/notes/scan";
  if (params.size > 0) {
    path += "?" + params.toString();
  }
  const res = await api(path, { method: "POST" });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return res.json();
}

// scanNotes writes a note for every project and every session that has been used, or updates the
// ones it wrote before, and answers what it did.
export async function scanNotes(
  api: Api,
  opts: ScanOptions = {},
): Promise<NoteScan> {
  return (await postScan(api, opts, false)) as NoteScan;
}

// startScan runs the same scan in the background and answers its progress; scanProgress follows
// it. A scan already running is followed instead of started again.
export async function startScan(
  api: Api,
  opts: ScanOptions = {},
): Promise<ScanProgress> {
  return (await postScan(api, opts, true)) as ScanProgress;
}
