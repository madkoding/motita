// What a git action from a menu is doing, shown in a modal while it runs and once it is done.
// It is a small store of its own, not a hook's state, because two hooks start these actions (the
// project's, for updating a main branch, and the repository's, for the rest) and one modal shows them.
import { useEffect, useState } from "preact/hooks";

export interface GitActivity {
  // title is the action in a few words ("Pulling api"), detail is what git does for it.
  title: string;
  detail: string;
  state: "running" | "done" | "error";
  // message is how it ended, and output what git printed for it (the lines the action
  // returned); both are empty while it runs.
  message: string;
  output: string;
}

let current: GitActivity | null = null;
const listeners = new Set<() => void>();

function set(next: GitActivity | null) {
  current = next;
  listeners.forEach((l) => l());
}

// beginGitActivity opens the modal on an action that has just started.
export function beginGitActivity(title: string, detail: string) {
  set({ title, detail, state: "running", message: "", output: "" });
}

// endGitActivity says how the action ended: the modal stays open until the person closes it.
export function endGitActivity(ok: boolean, message: string, output: string) {
  if (current) {
    set({ ...current, state: ok ? "done" : "error", message, output });
  }
}

export function closeGitActivity() {
  set(null);
}

// useGitActivity is the action on screen, nothing when there is none.
export function useGitActivity(): GitActivity | null {
  const [value, setValue] = useState(current);
  useEffect(() => {
    const on = () => setValue(current);
    listeners.add(on);
    on();
    return () => {
      listeners.delete(on);
    };
  }, []);
  return value;
}
