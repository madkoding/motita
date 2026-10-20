// FloatingMenu draws a menu at the end of <body>, anchored under (or, when it does not fit, over)
// the element that opens it, and always inside the window. It is `fixed` in viewport coordinates,
// so no ancestor's overflow clips it, and it is portaled because a transform or backdrop-filter
// ancestor would otherwise become the containing block of that `fixed`.
import { useLayoutEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { BodyPortal } from "./BodyPortal";
import { MENU_GAP } from "./appConst";

// floatingPlace puts a box of `w` by `h` against `anchor`'s bottom-right corner (flush with its
// right edge), flipped above it when there is no room below, and clamped inside the window.
export function floatingPlace(
  anchor: DOMRect,
  w: number,
  h: number,
  vw: number,
  vh: number,
): { top: number; left: number } {
  const below = anchor.bottom + MENU_GAP;
  let top = below;
  if (below + h > vh) {
    top = anchor.top - MENU_GAP - h;
  }
  top = Math.max(MENU_GAP, Math.min(top, vh - h - MENU_GAP));
  const left = Math.max(
    MENU_GAP,
    Math.min(anchor.right - w, vw - w - MENU_GAP),
  );
  return { top, left };
}

export function FloatingMenu({
  anchor,
  class: cls,
  children,
}: {
  anchor: HTMLElement | null;
  class?: string;
  children: ComponentChildren;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  // Measured after render and before paint, so the menu is never seen at a wrong place.
  useLayoutEffect(() => {
    const place = () => {
      const el = ref.current;
      if (!el || !anchor?.isConnected) {
        return;
      }
      setPos(
        floatingPlace(
          anchor.getBoundingClientRect(),
          el.offsetWidth,
          el.offsetHeight,
          window.innerWidth,
          window.innerHeight,
        ),
      );
    };
    place();
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [anchor]);
  return (
    <BodyPortal>
      <div
        ref={ref}
        class={`floating-menu ${cls ?? ""}`}
        role="menu"
        style={{
          position: "fixed",
          top: `${pos?.top ?? 0}px`,
          left: `${pos?.left ?? 0}px`,
          visibility: pos ? "visible" : "hidden",
        }}
      >
        {children}
      </div>
    </BodyPortal>
  );
}
