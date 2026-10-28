// The review of the memory: a new conversation that asks the agent to check it with the
// `reviewing-the-memory` skill. The agent reports; it changes nothing until the person agrees.
import { useCallback } from "preact/hooks";

export const REVIEW_MEMORY_PROMPT =
  "Review my memory with the reviewing-the-memory skill. Read the whole memory, then report which entries contradict each other, look out of date, have no source, point to entries that do not exist or are orphans. Do not change anything until I agree.";

// useReviewMemory opens a conversation, closes the settings and the memory window (saving the open
// entry) and sends the review request there. When the window refuses to close, or no conversation
// could be opened, nothing is sent.
export function useReviewMemory(
  createSession: () => Promise<string | undefined>,
  closeMemory: () => Promise<boolean>,
  submit: (text: string) => unknown,
  closeSettings: () => void,
) {
  return useCallback(async () => {
    closeSettings();
    if (!(await closeMemory())) {
      return;
    }
    if (await createSession()) {
      void submit(REVIEW_MEMORY_PROMPT);
    }
  }, [createSession, closeMemory, submit, closeSettings]);
}
