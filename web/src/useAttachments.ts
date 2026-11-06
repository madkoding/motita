// The files attached to the message being written: stored in the session's workspace as they are
// chosen, listed above the field, and cleared once the message is sent.
import { useRef, useState } from "preact/hooks";
import type { Api } from "./gitApi";
import { type Attachment, uploadAttachment } from "./attachments";
import { errorMessage } from "./errors";

export function useAttachments(
  api: Api,
  sessionRef: { current: string },
  startSession: () => Promise<string | undefined>,
  sessionId: string,
) {
  // Each file is kept with the conversation whose workspace holds it: the message names a path
  // inside that workspace, so only the files of the conversation on screen are listed and sent.
  const [stored, setStored] = useState<{ sid: string; file: Attachment }[]>([]);
  const attachments = stored
    .filter((a) => a.sid === sessionId)
    .map((a) => a.file);
  // starting is the session being opened for the files chosen first: files chosen while it opens
  // wait for it instead of opening a second one.
  const starting = useRef<Promise<string | undefined> | null>(null);
  const [uploading, setUploading] = useState(0);
  const [attachError, setAttachError] = useState("");

  const openSession = async () => {
    starting.current ??= startSession().finally(() => {
      starting.current = null;
    });
    return starting.current;
  };

  // attachFiles stores files in the conversation's workspace (opening one when there is none) and
  // lists them above the field. A file that fails says why and leaves the others alone.
  const attachFiles = async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    setAttachError("");
    const sid = sessionRef.current || (await openSession());
    if (!sid) {
      return;
    }
    setUploading((n) => n + files.length);
    for (const file of files) {
      try {
        const saved = await uploadAttachment(api, sid, file);
        setStored((prev) => [...prev, { sid, file: saved }]);
      } catch (e) {
        setAttachError(errorMessage(e));
      }
      setUploading((n) => n - 1);
    }
  };

  const removeAttachment = (path: string) =>
    setStored((prev) =>
      prev.filter((a) => !(a.sid === sessionId && a.file.path === path)),
    );

  const clearAttachments = () => {
    setStored((prev) => prev.filter((a) => a.sid !== sessionId));
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
