// The review of the notes: a new conversation that asks the agent to check the vault with the
// `reviewing-the-notes` skill. The agent reports; it changes nothing until the person agrees.
import { useCallback, useEffect } from "preact/hooks";

// REVIEW_NOTES_EVENT asks the app to start the review from a place that has no store, such as the
// settings card that has just scanned.
export const REVIEW_NOTES_EVENT = "motita-review-notes";

export const REVIEW_NOTES_PROMPT =
  "Review my notes with the reviewing-the-notes skill. Read the whole vault, then report which notes contradict each other, look out of date, have no source, point to notes that do not exist or are orphans. Do not change anything until I agree.";

// useReviewNotes opens a conversation, closes the settings and the notes window (saving the open
// note) and sends the review request there. When the window refuses to close, or no conversation
// could be opened, nothing is sent. It also answers REVIEW_NOTES_EVENT.
export function useReviewNotes(
  createSession: () => Promise<string | undefined>,
  closeNotes: () => Promise<boolean>,
  submit: (text: string) => unknown,
  closeSettings: () => void,
) {
  const review = useCallback(async () => {
    closeSettings();
    if (!(await closeNotes())) {
      return;
    }
    if (await createSession()) {
      void submit(REVIEW_NOTES_PROMPT);
    }
  }, [createSession, closeNotes, submit, closeSettings]);
  useEffect(() => {
    const on = () => void review();
    window.addEventListener(REVIEW_NOTES_EVENT, on);
    return () => window.removeEventListener(REVIEW_NOTES_EVENT, on);
  }, [review]);
  return review;
}
