// BodyPortal draws its children at the end of <body>, out of the tree that rendered them. A
// full-window layer (`position: fixed; inset: 0`) needs it when it is opened from inside a chat
// bubble: the bubble's backdrop-filter makes it the containing block of any `fixed` descendant,
// and the layer was drawn the size of the bubble instead of the window.
import { createPortal } from "preact/compat";
import type { ComponentChildren } from "preact";

export function BodyPortal({ children }: { children: ComponentChildren }) {
  return createPortal(children, document.body);
}
