// What a drawer keeps in the page: nothing while it is closed, and the newest entries while open.
//
// The TTY and THINK drawers used to render their whole history all the time, closed or not: up
// to 500 commands with their outputs and every thought of the conversation, re-diffed on every
// streamed event and held in the DOM behind a panel nobody was looking at. A closed drawer now
// mounts nothing, and an open one mounts the newest WINDOW entries, with a button for the older.
import { useEffect, useState } from "preact/hooks";

/** How many entries an open drawer shows, and how many more each "earlier" click adds. */
export const DRAWER_WINDOW = 50;

/**
 * How long a closed drawer keeps its content: the panel slides out in 0.3s, and emptying it at
 * the start of the slide would show a blank screen leaving.
 */
export const DRAWER_LINGER_MS = 350;

/** windowStart is the index of the first entry shown, out of `total`, when `shown` fit. */
export function windowStart(total: number, shown: number): number {
  return Math.max(0, total - shown);
}

/**
 * useDrawerContent says whether the drawer's content is mounted, and how many entries it shows.
 * The content mounts when the drawer opens and leaves once it has finished closing; the window
 * goes back to its size on every close, so a drawer reopened later starts light again.
 */
export function useDrawerContent(open: boolean): {
  mounted: boolean;
  shown: number;
  showEarlier: () => void;
} {
  const [mounted, setMounted] = useState(open);
  const [shown, setShown] = useState(DRAWER_WINDOW);
  useEffect(() => {
    if (open) {
      setMounted(true);
      return undefined;
    }
    const timer = setTimeout(() => {
      setMounted(false);
      setShown(DRAWER_WINDOW);
    }, DRAWER_LINGER_MS);
    return () => clearTimeout(timer);
  }, [open]);
  const showEarlier = () => setShown((n) => n + DRAWER_WINDOW);
  // `open` counts too: the render that opens the drawer already draws its content, so the panel
  // never slides in empty.
  return { mounted: mounted || open, shown, showEarlier };
}
