// Whether the pull request overview is open. It is a small store of its own, like the git activity
// modal, so the sidebar can open it without the page's store knowing about it.
import { useEffect, useState } from "preact/hooks";

let open = false;
const listeners = new Set<() => void>();

function set(next: boolean) {
  open = next;
  listeners.forEach((l) => l());
}

export function openPullRequests() {
  set(true);
}

export function closePullRequests() {
  set(false);
}

// usePullRequestsOpen is whether the overview is on screen.
export function usePullRequestsOpen(): boolean {
  const [value, setValue] = useState(open);
  useEffect(() => {
    const on = () => setValue(open);
    listeners.add(on);
    on();
    return () => {
      listeners.delete(on);
    };
  }, []);
  return value;
}
