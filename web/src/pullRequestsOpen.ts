// Whether the pull request overview is open. It is a small store of its own, like the git activity
// modal, so the sidebar can open it without the page's store knowing about it.
import { useEffect, useState } from "preact/hooks";

let open = false;
let reloads = 0;
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

// reloadPullRequests asks the open overview to read the list again from the hosts, as after a branch
// was updated.
export function reloadPullRequests() {
  reloads += 1;
  listeners.forEach((l) => l());
}

// usePullRequestsReloads counts the reloads asked for, so the overview reads again when it grows.
export function usePullRequestsReloads(): number {
  const [value, setValue] = useState(reloads);
  useEffect(() => {
    const on = () => setValue(reloads);
    listeners.add(on);
    on();
    return () => {
      listeners.delete(on);
    };
  }, []);
  return value;
}
