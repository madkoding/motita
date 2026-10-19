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
