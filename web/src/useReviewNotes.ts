// The review of the notes: a new conversation that asks the agent to check the vault with the
// `reviewing-the-notes` skill. The agent reports; it changes nothing until the person agrees.
import { useCallback } from "preact/hooks";

export const REVIEW_NOTES_PROMPT =
  "Review my notes with the reviewing-the-notes skill. Read the whole vault, then report which notes contradict each other, look out of date, have no source, point to notes that do not exist or are orphans. Do not change anything until I agree.";

// useReviewNotes opens a conversation, closes the notes window (saving what is open) and sends
// the review request there. When no conversation could be opened, or the window refuses to
// close, nothing is sent.
export function useReviewNotes(
  createSession: () => Promise<string | undefined>,
  closeNotes: () => Promise<boolean>,
  submit: (text: string) => unknown,
) {
  return useCallback(async () => {
    if (!(await closeNotes())) {
      return;
    }
    if (await createSession()) {
      void submit(REVIEW_NOTES_PROMPT);
    }
  }, [createSession, closeNotes, submit]);
}
