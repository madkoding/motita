// The dimmed layer under a dialog. A click on it (and not inside the dialog) closes the dialog,
// and a click inside never reaches the page's own click listeners. However the dialog is closed,
// it leaves the way it came: the backdrop fades and the dialog shrinks away (motion.ts).
import { useRef } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { useBackdropClose } from "./useBackdropClose";
import { useLeave } from "./motion";

// The backdrop most dialogs share.
export const BACKDROP_CLASS =
  "fixed inset-0 bg-black/60 backdrop-blur-xs z-50 flex items-center justify-center p-4";

interface Props {
  onClose: () => void;
  class?: string;
  // modal is false for a layer that is not a dialog (a context menu's click catcher): only a
  // dialog moves the toast out of its way (see .toast-layer in index.css).
  modal?: boolean;
  children: ComponentChildren;
}

export function ModalBackdrop({
  onClose,
  class: cls = BACKDROP_CLASS,
  modal = true,
  children,
}: Props) {
  const ref = useRef<HTMLDivElement>(null);
  useBackdropClose(ref, onClose);
  useLeave(ref, "fade", "pop");
  return (
    <div ref={ref} class={cls} data-modal={modal || undefined}>
      {children}
    </div>
  );
}
