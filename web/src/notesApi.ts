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

// NoteScan is what scanning the sessions and projects into notes reports.
export interface NoteScan {
  // false when only a first-use scan was asked for and the vault did not need one.
  ran: boolean;
  // Sessions the model summarised in this scan.
  summarized: number;
  // Projects whose architecture note was written, and those the model explained in this scan.
  mapped: number;
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
}

// scanNotes writes a note for every project and every session that has been used, or updates the
// ones it wrote before.
export async function scanNotes(
  api: Api,
  opts: ScanOptions = {},
): Promise<NoteScan> {
  const params = new URLSearchParams();
  if (opts.first) {
    params.set("first", "1");
  }
  if (opts.summarize) {
    params.set("summarize", "1");
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
  return (await res.json()) as NoteScan;
}
