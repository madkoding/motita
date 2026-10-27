// The files attached to the message being written: stored in the session's workspace as they are
// chosen, listed above the field, and cleared once the message is sent.
import { useState } from "preact/hooks";
import type { Api } from "./gitApi";
import { type Attachment, uploadAttachment } from "./attachments";
import { errorMessage } from "./errors";

export function useAttachments(
  api: Api,
  sessionRef: { current: string },
  startSession: () => Promise<string | undefined>,
) {
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [uploading, setUploading] = useState(0);
  const [attachError, setAttachError] = useState("");

  // attachFiles stores files in the conversation's workspace (opening one when there is none) and
  // lists them above the field. A file that fails says why and leaves the others alone.
  const attachFiles = async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    setAttachError("");
    const sid = sessionRef.current || (await startSession());
    if (!sid) {
      return;
    }
    setUploading((n) => n + files.length);
    for (const file of files) {
      try {
        const saved = await uploadAttachment(api, sid, file);
        setAttachments((prev) => [...prev, saved]);
      } catch (e) {
        setAttachError(errorMessage(e));
      }
      setUploading((n) => n - 1);
    }
  };

  const removeAttachment = (path: string) =>
    setAttachments((prev) => prev.filter((a) => a.path !== path));

  const clearAttachments = () => {
    setAttachments([]);
    setAttachError("");
  };

  return {
    attachments,
    uploading,
    attachError,
    attachFiles,
    removeAttachment,
    clearAttachments,
  };
}
