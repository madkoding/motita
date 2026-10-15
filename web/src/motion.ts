// Exit animations for elements that unmount. Preact removes a node in the same render that says it
// is gone, so there is nothing left on screen to animate, and holding every dialog, toast and sheet
// mounted for a "closing" state would mean a second state in each of them. Instead, as the element
// unmounts, leaveGhost copies it as it looked, pins the copy where the original stood and plays a
// leave keyframe from motion.css on the copy. The copy is inert and takes no pointer events, so the
// page underneath is usable at once; it removes itself when the animation ends.
import { useLayoutEffect } from "preact/hooks";
import type { RefObject } from "preact";
import { choose } from "./choose";

// How an element leaves. Each name is a class in motion.css.
export type Leave = "fade" | "pop" | "sink" | "lift" | "slide-left";

const LEAVE_CLASS: Record<Leave, string> = {
  fade: "motion-fade-out",
  pop: "motion-pop-out",
  sink: "motion-sink-out",
  lift: "motion-lift-out",
  "slide-left": "motion-slide-out-left",
};

// The longest leave animation, plus a margin: the copy is removed after this even if
// `animationend` never fires (a background tab does not run animations).
const GHOST_MAX_MS = 600;

// copyFieldValues carries what was typed into the copy: cloneNode copies the attributes, not the
// live value, so a filled-in field would visibly empty itself while fading.
function copyFieldValues(from: HTMLElement, to: HTMLElement) {
  const src = from.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input, textarea");
  const dst = to.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input, textarea");
  // The copy is a deep clone made just before, so it holds the same fields in the same order.
  src.forEach((el, i) => {
    const copy = dst[i];
    if (el.type === "checkbox" || el.type === "radio") {
      (copy as HTMLInputElement).checked = (el as HTMLInputElement).checked;
    } else if (el.type !== "file") {
      copy.value = el.value;
    } else {
      // A file input's value cannot be set from script, and the copy shows no file either way.
    }
  });
}

// leaveGhost plays `how` on a copy of `el`, and `child` on the copy's first element (the dialog
// inside a backdrop). It does nothing outside a real browser (no Web Animations API, as in jsdom)
// or for an element that is not laid out.
export function leaveGhost(el: HTMLElement | null, how: Leave, child?: Leave) {
  if (!el || !el.isConnected || typeof el.animate !== "function") {
    return;
  }
  const r = el.getBoundingClientRect();
  if (r.width === 0 || r.height === 0) {
    return;
  }
  const ghost = el.cloneNode(true) as HTMLElement;
  copyFieldValues(el, ghost);
  // Ids would be duplicated while the copy lives, and a test or a label lookup could find it.
  ghost.removeAttribute("id");
  ghost.querySelectorAll("[id]").forEach((n) => n.removeAttribute("id"));
  // Nor may it count as an open dialog (the toast moves for `data-modal`, Escape closes the last
  // `data-dialog-root`) or be found by a test id.
  for (const attr of ["data-modal", "data-dialog-root", "data-testid"]) {
    ghost.removeAttribute(attr);
    ghost.querySelectorAll(`[${attr}]`).forEach((n) => n.removeAttribute(attr));
  }
  ghost.setAttribute("aria-hidden", "true");
  ghost.inert = true;
  ghost.classList.add("motion-ghost", LEAVE_CLASS[how]);
  Object.assign(ghost.style, {
    position: "fixed",
    top: r.top + "px",
    left: r.left + "px",
    right: "auto",
    bottom: "auto",
    width: r.width + "px",
    height: r.height + "px",
    margin: "0",
  });
  const inner = choose(child, ghost.firstElementChild as HTMLElement | null, null);
  inner?.classList.add(LEAVE_CLASS[child!]);
  // A <dialog> copy keeps `open` as an attribute, but drawn non-modally: it stays where it is.
  document.body.appendChild(ghost);
  let done = false;
  const remove = () => {
    if (!done) {
      done = true;
      ghost.remove();
    }
  };
  ghost.addEventListener("animationend", (e) => {
    if (e.target === ghost) {
      remove();
    }
  });
  setTimeout(remove, GHOST_MAX_MS);
}

// useLeave plays `how` on the element behind `ref` when the component unmounts. The hook's cleanup
// runs before Preact detaches the component's nodes, so the element is still laid out to copy.
export function useLeave(
  ref: RefObject<HTMLElement | null>,
  how: Leave,
  child?: Leave,
) {
  useLayoutEffect(
    () => () => leaveGhost(ref.current, how, child),
    // The ref object is stable and `how` is fixed per call site.
    [],
  );
}
