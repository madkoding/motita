// The dimmed layer under a dialog. A click on it (and not inside the dialog) closes the dialog,
// and a click inside never reaches the page's own click listeners.
import { useRef } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { useBackdropClose } from "./useBackdropClose";

// The backdrop most dialogs share.
export const BACKDROP_CLASS =
  "fixed inset-0 bg-black/60 backdrop-blur-xs z-50 flex items-center justify-center p-4";

interface Props {
  onClose: () => void;
  class?: string;
  children: ComponentChildren;
}

export function ModalBackdrop({
  onClose,
  class: cls = BACKDROP_CLASS,
  children,
}: Props) {
  const ref = useRef<HTMLDivElement>(null);
  useBackdropClose(ref, onClose);
  return (
    <div ref={ref} class={cls}>
      {children}
    </div>
  );
}
