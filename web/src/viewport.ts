// The phone layout: what the page needs to know about a narrow, touch screen that CSS alone
// cannot say.
//
// The breakpoint is the one the markup already uses (Tailwind's `md`, 768px): below it the
// sidebar is a sheet over the conversation instead of a column beside it.
import { useEffect } from "preact/hooks";

export const PHONE_QUERY = "(max-width: 767px)";

// The visual viewport is this much shorter than the layout viewport (the root element's client
// height, which the keyboard does not change) when the on-screen keyboard is up. Browser chrome
// (a URL bar sliding away) moves it by less than this.
const KEYBOARD_MIN_PX = 120;

// isPhone says whether the page is laid out for a phone right now.
export function isPhone(): boolean {
  try {
    return window.matchMedia(PHONE_QUERY).matches;
  } catch {
    return false;
  }
}

// initialSidebarOpen is whether the sidebar starts open. On a phone it is a sheet that covers the
// whole conversation, so it starts closed whatever the desktop preference says; on a wider screen
// it follows the remembered choice, open unless it was closed.
export function initialSidebarOpen(
  stored: string | null,
  phone: boolean,
): boolean {
  return !phone && stored !== "false";
}

// closesOnPhone says whether a click in the sidebar picked a conversation, which on a phone is
// the reader leaving the list for it. Picking a DIFFERENT session already closes the sheet (see
// useSessionList); this covers tapping the one that is already open.
export function closesOnPhone(target: EventTarget | null): boolean {
  return target instanceof Element && !!target.closest(".session-row .row-hit");
}

// keyboardOpen says whether the on-screen keyboard is covering part of the page, from the two
// heights the browser reports.
export function keyboardOpen(layoutH: number, visualH: number): boolean {
  return layoutH - visualH > KEYBOARD_MIN_PX;
}

// useVisualViewport keeps `--app-h` on the root element at the height the reader can actually
// see, and `data-keyboard` set while the on-screen keyboard is up.
//
// iOS Safari does not shrink `100dvh` for the keyboard: the composer, pinned to the bottom of a
// full-height column, ended up behind it. The visual viewport is the one height every mobile
// browser reports correctly, so the shell is sized from it. While the reader is pinch-zoomed the
// visual viewport is a magnified window, not a smaller screen, so the variable is dropped and the
// layout falls back to `100dvh`.
export function useVisualViewport(): void {
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) {
      return undefined;
    }
    const root = document.documentElement;
    const update = () => {
      if (Math.abs(vv.scale - 1) > 0.01) {
        root.style.removeProperty("--app-h");
        delete root.dataset.keyboard;
        return;
      }
      root.style.setProperty("--app-h", `${Math.round(vv.height)}px`);
      if (keyboardOpen(root.clientHeight, vv.height)) {
        root.dataset.keyboard = "open";
        // iOS scrolls the layout viewport to bring the field into view; the shell is already
        // the visible height, so put it back where it belongs.
        window.scrollTo(0, 0);
      } else {
        delete root.dataset.keyboard;
      }
    };
    update();
    vv.addEventListener("resize", update);
    return () => vv.removeEventListener("resize", update);
  }, []);
}
