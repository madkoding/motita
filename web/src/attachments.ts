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

// hintOf is what the agent is told about reading one kind of file.
function hintOf(kind: string): string {
  switch (kind) {
    case "image":
      return " - look at it with an image-reading tool if you have one";
    case "video":
      return " - sample frames with ffmpeg (for example -vf fps=1) and read its audio track if it has one";
    case "audio":
      return " - transcribe it if you can";
    case "document":
      return " - extract its text with a tool such as pdftotext or pandoc";
    default:
      return "";
  }
}

// withAttachments is the message the agent receives: what was typed, then the files by path.
export function withAttachments(text: string, list: Attachment[]): string {
  if (list.length === 0) {
    return text;
  }
  const lines = list.map(
    (a) => `- ${a.path} (${a.kind}, ${sizeLabel(a.size)})${hintOf(a.kind)}`,
  );
  const lead = text || "Look at the attached files and tell me what you see.";
  return `${lead}\n\n[The person attached these files, saved in your workspace:\n${lines.join("\n")}]`;
}

// shownWith is what the conversation draws for that message: the typed text and the file names.
export function shownWith(text: string, list: Attachment[]): string {
  const names = "📎 " + list.map((a) => a.name).join(", ");
  return choose(text, text + "\n\n" + names, names);
}

// outgoing is a message with its files: what the agent receives names them, and what the
// conversation draws (undefined for a message with none) is the typed text and their names.
export function outgoing(
  typed: string,
  list: Attachment[],
): { text: string; shown?: string } {
  return {
    text: withAttachments(typed, list),
    shown: choose(list.length, shownWith(typed, list), undefined),
  };
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
