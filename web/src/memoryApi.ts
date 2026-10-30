// The memory's types and the small pure helpers around them: the folder tree, the file name a
// title makes and the wiki links turned into links the Markdown view can draw. No value imports: the
// node tests load this file as is.

export type Api = (path: string, init?: RequestInit) => Promise<Response>;

export interface MemoryEntry {
  path: string;
  title: string;
  size: number;
  modified: string;
}

// A [[wiki link]] of an entry: path is the entry it leads to, absent while there is none.
export interface EntryLink {
  target: string;
  alias?: string;
  heading?: string;
  path?: string;
}

export interface EntryBacklink extends MemoryEntry {
  // The first line that holds the link.
  context: string;
}

// What an entry says about itself in its front matter: who wrote it, when, and its tags. The date
// is the file's own when the entry says none.
export interface EntryMeta {
  author?: string;
  date: string;
  tags?: string[];
}

export interface EntryView extends MemoryEntry {
  meta: EntryMeta;
  body: string;
  links: EntryLink[];
  backlinks: EntryBacklink[];
}

// A namespace is a collection of the memory: the first folder of an entry, "" for the entries at
// the top.
export interface Namespace {
  name: string;
  entries: number;
  modified: string;
}

// One section of an entry that answers a search. block is the whole section (the parent), snippet
// the passage that matched it; via names the entry a related answer was reached through.
export interface MemoryHit {
  path: string;
  title: string;
  namespace: string;
  heading?: string;
  line: number;
  snippet: string;
  block: string;
  author?: string;
  date: string;
  score: number;
  via?: string;
}

export interface MemoryAnswer {
  hits: MemoryHit[];
  related?: MemoryHit[];
}

// The filters that narrow a search. An empty one is not applied.
export interface MemoryFilters {
  namespace: string;
  author: string;
  since: string;
  until: string;
  graph: boolean;
}

export const NO_FILTERS: MemoryFilters = {
  namespace: "",
  author: "",
  since: "",
  until: "",
  graph: false,
};

// searchPath is the request that answers q under the filters.
export function searchPath(q: string, f: MemoryFilters): string {
  const params = new URLSearchParams({ q });
  for (const key of ["namespace", "author", "since", "until"] as const) {
    if (f[key] !== "") {
      params.set(key, f[key]);
    }
  }
  if (f.graph) {
    params.set("graph", "1");
  }
  return "/v1/memory/search?" + params.toString();
}

// One level of the tree: the folders below it and the entries in it.
export interface EntryFolder {
  name: string;
  path: string;
  folders: EntryFolder[];
  entries: MemoryEntry[];
}

// joinPath puts name below dir, which is empty at the top.
function joinPath(dir: string, name: string): string {
  if (dir === "") {
    return name;
  }
  return dir + "/" + name;
}

// trimSlashes takes the slashes off both ends of a folder name.
function trimSlashes(s: string): string {
  let a = 0;
  let b = s.length;
  while (a < b && s[a] === "/") {
    a++;
  }
  while (b > a && s[b - 1] === "/") {
    b--;
  }
  return s.slice(a, b);
}

// buildTree groups the entries (sorted by path, as the gateway sends them) into folders.
export function buildTree(entries: MemoryEntry[]): EntryFolder {
  const root: EntryFolder = { name: "", path: "", folders: [], entries: [] };
  for (const n of entries) {
    const parts = n.path.split("/");
    parts.pop();
    let at = root;
    for (const part of parts) {
      const path = joinPath(at.path, part);
      let next = at.folders.find((f) => f.path === path);
      if (!next) {
        next = { name: part, path, folders: [], entries: [] };
        at.folders.push(next);
      }
      at = next;
    }
    at.entries.push(n);
  }
  return root;
}

// folderOf is the folder of an entry path, "" at the top.
export function folderOf(path: string): string {
  return path.slice(0, Math.max(0, path.lastIndexOf("/")));
}

// entryFileName makes the memory path of a title typed by the person: no characters a file name
// cannot hold, one ".md", inside folder (when there is one). A title may name sub-folders with "/".
export function entryFileName(folder: string, title: string): string {
  const cleaned = title
    .split("/")
    .map((p) => p.replace(/[\\:*?"<>|#^[\]]/g, "").trim())
    .filter((p) => p !== "" && p !== "." && p !== ".." && !p.startsWith("."))
    .join("/")
    .replace(/\.md$/i, "");
  if (cleaned === "") {
    return "";
  }
  return joinPath(trimSlashes(folder), cleaned) + ".md";
}

const ENTRY_PREFIX = "#entry:";
const NEW_PREFIX = "#new:";

// WIKI matches a code span or fence (left alone) or a [[wiki link]].
const WIKI = /(```[\s\S]*?```|`[^`\n]*`)|!?\[\[([^[\]\n]+)\]\]/g;

// splitWiki takes "target#heading|alias" apart.
function splitWiki(inner: string): { target: string; text: string } {
  const bar = inner.indexOf("|");
  let left = inner;
  let alias = "";
  if (bar >= 0) {
    left = inner.slice(0, bar);
    alias = inner.slice(bar + 1).trim();
  }
  const target = left.split("#")[0].trim().replace(/\.md$/i, "");
  return { target, text: alias || left.trim() };
}

// wikiToMarkdown turns the [[wiki links]] of an entry into Markdown links: "#entry:<path>" for an
// entry that exists and "#new:<target>" for one that does not yet. links is what the gateway
// resolved. Links inside code stay as written.
export function wikiToMarkdown(body: string, links: EntryLink[]): string {
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
      ? ENTRY_PREFIX + encodeURIComponent(path)
      : NEW_PREFIX + encodeURIComponent(target);
    return "[" + text.replace(/[[\]]/g, "") + "](" + href + ")";
  });
}

// EntryTarget is where a link of the preview leads.
export type EntryTarget =
  { kind: "entry"; path: string } | { kind: "new"; target: string };

// parseEntryHref reads the href wikiToMarkdown wrote, or null for any other link.
export function parseEntryHref(
  href: string | null | undefined,
): EntryTarget | null {
  if (href?.startsWith(ENTRY_PREFIX)) {
    return {
      kind: "entry",
      path: decodeURIComponent(href.slice(ENTRY_PREFIX.length)),
    };
  }
  if (href?.startsWith(NEW_PREFIX)) {
    return {
      kind: "new",
      target: decodeURIComponent(href.slice(NEW_PREFIX.length)),
    };
  }
  return null;
}

// One entry of the link graph, and how many entries link to it (in) and how many it links to (out).
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

export interface MemoryGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  // Links to entries that do not exist yet: counted, not drawn.
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

// layoutGraph places the entries in a width x height box with a small force simulation: every pair
// pushes apart, every link pulls together, and the whole drifts to the middle. It starts from a
// circle and cools in a fixed number of steps, so the same vault always draws the same picture.
export function layoutGraph(
  graph: MemoryGraph,
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
// SPREAD fits the layout to the radius: the share of entries that lands inside it, so a few far
// entries do not squeeze the rest into the middle, and how far past it the others may stay.
const SPREAD = { share: 0.85, overshoot: 1.15 };

// GOLDEN_ANGLE, in radians, spreads points evenly round a sphere: π·(3 − √5).
export const GOLDEN_ANGLE = 2.399963229728653;

// fitToRadius centres the entries and scales them so most sit within radius, however the forces
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

// linkCounts is how many links each entry has, in and out.
function linkCounts(edges: GraphEdge[]): Map<string, number> {
  const degree = new Map<string, number>();
  for (const e of edges) {
    degree.set(e.from, (degree.get(e.from) ?? 0) + 1);
    degree.set(e.to, (degree.get(e.to) ?? 0) + 1);
  }
  return degree;
}

// layoutGraph3D places the entries in space around the origin, within about radius: they start
// spread evenly over a sphere, then every pair pushes apart, every link pulls together and the
// whole drifts to the middle. A link to a much-linked entry pulls less, so an index that links every
// chapter does not gather them all round it. Big vaults get fewer rounds; the same vault always
// lands the same.
export function layoutGraph3D(
  graph: MemoryGraph,
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

// plainWiki reads a line of an entry as text: the marks of a list, quote, table or heading at its
// start go, a [[link]] becomes the words it shows (its alias, or the name of the entry it leads to)
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

// entryTrail is the folders an entry sits in, outermost first: "a/b/c.md" is ["a", "b"].
export function entryTrail(path: string): string[] {
  return path.split("/").slice(0, -1);
}

// placeOf says where an entry is, as the folders it sits in: "HR › Hiring".
export function placeOf(path: string): string {
  return entryTrail(path).join(" › ");
}

// groupKey names the collection an entry belongs to: its first folder, "" for an entry at the top.
export function groupKey(path: string): string {
  return entryTrail(path)[0] ?? "";
}

// MapGroup is one collection on the map's legend, with how many entries it has.
export interface MapGroup {
  key: string;
  count: number;
}

// mapGroups lists the collections the entries belong to, the biggest first.
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

// entryColours gives every entry the colour of its collection, as an index into a palette of
// colours: the biggest collections get the first ones, so the ones the legend names never share one.
export function entryColours(
  nodes: GraphNode[],
  colours: number,
): Map<string, number> {
  const rank = new Map(mapGroups(nodes).map((g, i) => [g.key, i % colours]));
  return new Map(
    nodes.map((n) => [n.path, rank.get(groupKey(n.path)) as number]),
  );
}

// countEntries is how many entries a folder holds, its sub-folders included.
export function countEntries(folder: EntryFolder): number {
  return folder.folders.reduce(
    (n, f) => n + countEntries(f),
    folder.entries.length,
  );
}

// RECENT_ENTRIES is how many of the newest entries the library shows on top.
export const RECENT_ENTRIES = 5;

// recentEntries is the newest entries first, the ones whose date cannot be read left out: what was
// just saved, by the person or by the agent, without hunting for it in the folders.
export function recentEntries(
  entries: MemoryEntry[],
  n = RECENT_ENTRIES,
): MemoryEntry[] {
  return entries
    .filter((e) => !Number.isNaN(Date.parse(e.modified)))
    .sort((a, b) => Date.parse(b.modified) - Date.parse(a.modified))
    .slice(0, n);
}

// The steps of an age, largest first: the unit's length in seconds and its key.
const AGE_STEPS: [number, string][] = [
  [365 * 86400, "{n}y ago"],
  [30 * 86400, "{n}mo ago"],
  [86400, "{n}d ago"],
  [3600, "{n}h ago"],
  [60, "{n}m ago"],
];

// ageKey is how long ago iso was, as the key to translate and its number. Under a minute, or in
// the future (a clock ahead of the gateway's), it is "just now".
export function ageKey(iso: string, now: number): { key: string; n: number } {
  const secs = (now - Date.parse(iso)) / 1000;
  for (const [size, key] of AGE_STEPS) {
    if (secs >= size) {
      return { key, n: Math.floor(secs / size) };
    }
  }
  return { key: "just now", n: 0 };
}
