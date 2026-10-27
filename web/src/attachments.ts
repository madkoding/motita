// Files attached to a message: sent to the session's workspace first, then named in the message so
// the agent reads them with the tools it already has.
import type { Api } from "./gitApi";
import { SESSIONS_PATH } from "./appConst";
import { choose } from "./choose";

// Attachment is one stored file, as the gateway answers an upload.
export interface Attachment {
  name: string;
  // path is relative to the session's workspace.
  path: string;
  size: number;
  kind: string;
}

// MAX_ATTACHMENT_BYTES mirrors the gateway's ceiling, so a file that cannot be kept is refused
// before its bytes travel.
const BYTES_PER_MB = 1024 * 1024;
const MAX_ATTACHMENT_MB = 200;
export const MAX_ATTACHMENT_BYTES = MAX_ATTACHMENT_MB * BYTES_PER_MB;

// sizeLabel is a size a person reads.
export function sizeLabel(bytes: number): string {
  if (bytes < 1024) {
    return bytes + " B";
  }
  if (bytes < 1024 * 1024) {
    return Math.round(bytes / 1024) + " KB";
  }
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

// HINTS is what the agent is told about reading each kind of file.
const HINTS: Record<string, string> = {
  image: " - view it with an image tool",
  video: " - sample frames with ffmpeg",
  audio: " - transcribe it",
  document: " - extract its text (pdftotext)",
};

// withAttachments is the message the agent receives: what was typed, then the files by path.
export function withAttachments(text: string, list: Attachment[]): string {
  if (list.length === 0) {
    return text;
  }
  const lines = list.map(
    (a) =>
      `- ${a.path} (${a.kind}, ${sizeLabel(a.size)})${HINTS[a.kind] ?? ""}`,
  );
  const lead = text || "Look at the attached files.";
  return `${lead}\n\n[${NOTE_HEAD}\n${lines.join("\n")}]`;
}

// NOTE_PATTERN picks the files list out of a message that carries one, and FILE_PATTERN each
// line of it: the folder is the session's own, so the line holds the id the file is served by.
const NOTE_HEAD = "The person attached these files, saved in your workspace:";
const NOTE_PATTERN = new RegExp(
  String.raw`^([\s\S]*?)\n\n\[${NOTE_HEAD}\n([\s\S]*)\]$`,
);
const FILE_PATTERN = /^- \.motita\/uploads\/([^/]+)\/(.+?) \((\w+), [^)]*\)/;

// AttachedFile is one file a message names, with where it is served from.
export interface AttachedFile {
  name: string;
  kind: string;
  url: string;
}

// attachmentUrl is where the gateway serves a file attached to a session.
export function attachmentUrl(sid: string, name: string): string {
  return `${SESSIONS_PATH}${encodeURIComponent(sid)}/attachments/${encodeURIComponent(name)}`;
}

// fileOf reads one line of the files list, or nothing for a line that is not a file.
function fileOf(line: string): AttachedFile | null {
  const m = FILE_PATTERN.exec(line);
  if (!m) {
    return null;
  }
  const [, sid, name, kind] = m;
  return { name, kind, url: attachmentUrl(sid, name) };
}

// thumbOf is the picture to draw for a stored file, or "" when it is not a picture.
export function thumbOf(a: Attachment): string {
  const f = fileOf(`- ${a.path} (${a.kind}, 0 B)`);
  return choose(f && a.kind === "image", f?.url, "") as string;
}

// attachedIn splits a message into what was typed and the files it names. A message with no list
// of files comes back whole, with no files.
export function attachedIn(text: string): {
  lead: string;
  files: AttachedFile[];
} {
  const m = NOTE_PATTERN.exec(text);
  if (!m) {
    return { lead: text, files: [] };
  }
  const files = m[2]
    .split("\n")
    .map(fileOf)
    .filter((f): f is AttachedFile => f !== null);
  return { lead: m[1], files };
}

// filesOf lists the files of a drag or a paste, ignoring anything that is not a file.
export function filesOf(
  dt: { files?: ArrayLike<File> | null } | null | undefined,
): File[] {
  return Array.from(dt?.files ?? []);
}

// uploadAttachment stores one file in a session's workspace.
export async function uploadAttachment(
  api: Api,
  sid: string,
  file: File,
): Promise<Attachment> {
  if (file.size > MAX_ATTACHMENT_BYTES) {
    throw new Error(
      `${file.name}: larger than ${sizeLabel(MAX_ATTACHMENT_BYTES)}`,
    );
  }
  const res = await api(
    `${SESSIONS_PATH}${encodeURIComponent(sid)}/attachments?name=${encodeURIComponent(file.name || "file")}`,
    {
      method: "POST",
      headers: { "Content-Type": file.type || "application/octet-stream" },
      body: file,
    },
  );
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(`${file.name}: ${body.error || "HTTP " + res.status}`);
  }
  return body as Attachment;
}
