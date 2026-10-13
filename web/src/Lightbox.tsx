import { useEffect, useRef, useState } from "preact/hooks";
import type { RefObject } from "preact";
import { useDialog } from "./useDialog";
import { t } from "./i18n";
import { choose } from "./choose";
import { BodyPortal } from "./BodyPortal";

export interface LightboxImage {
  src: string;
  label: string;
}

const MIN = 0.1;
const MAX = 16;
const clamp = (z: number) => Math.min(MAX, Math.max(MIN, z));
// One step of the zoom buttons and keys, and one notch of the wheel.
const ZOOM_STEP = 1.25;
const WHEEL_STEP = 1.15;
// A double click on a fitted image zooms straight to this scale around the pointer.
const DOUBLE_CLICK_ZOOM = 2.5;

type Listeners = {
  [E in keyof HTMLElementEventMap]?: (e: HTMLElementEventMap[E]) => void;
};

// useNativeEvents listens on an element directly, always with the listeners of the latest render,
// the way Preact attaches an on* prop.
function useNativeEvents(ref: RefObject<HTMLElement | null>, listeners: Listeners) {
  const latest = useRef(listeners);
  latest.current = listeners;
  useEffect(() => {
    const el = ref.current;
    if (!el) {
      return undefined;
    }
    const proxies = Object.keys(latest.current).map((name) => {
      const proxy = (e: Event) => {
        (
          latest.current[name as keyof Listeners] as
            ((e: Event) => void) | undefined
        )?.(e);
      };
      el.addEventListener(name, proxy);
      return () => el.removeEventListener(name, proxy);
    });
    return () => {
      for (const remove of proxies) {
        remove();
      }
    };
  }, []);
}

// stopHere keeps a click inside the element it happened in.
const stopHere = (e: MouseEvent) => e.stopPropagation();

// Lightbox is the full-window preview of an image: wheel or +/- to zoom around the pointer, drag
// to pan, 0 to fit, 1 for actual size, and the arrow keys to move between the images of a set
// (the "before" and the "after" of a report). Zoom 1 means "fit the window"; the real pixel size
// is reached by the 100% button, which needs the image's natural size.
export function Lightbox({
  images,
  start,
  onClose,
}: {
  images: LightboxImage[];
  start: number;
  onClose: () => void;
}) {
  const root = useRef<HTMLDialogElement>(null);
  const bar = useRef<HTMLDivElement>(null);
  const stage = useRef<HTMLDivElement>(null);
  const img = useRef<HTMLImageElement>(null);
  const [index, setIndex] = useState(
    Math.min(Math.max(start, 0), images.length - 1),
  );
  const [view, setView] = useState({ zoom: 1, x: 0, y: 0 });
  // `live` is the latest view, so two events before a re-render (key repeat, a fast wheel) each
  // build on the previous one instead of on the stale value of the render they came from.
  const live = useRef(view);
  const apply = (v: { zoom: number; x: number; y: number }) => {
    live.current = v;
    setView(v);
  };
  const { zoom, x: panX, y: panY } = view;
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(
    null,
  );
  useDialog(root, onClose);

  const reset = () => apply({ zoom: 1, x: 0, y: 0 });
  // step moves from the CURRENT index (a functional update), so two key presses that land before
  // the next render still move twice instead of reading the same stale index.
  const step = (by: number) => {
    setIndex((i) => (i + by + images.length) % images.length);
    reset();
  };
  // zoomAt keeps the point under the pointer fixed while the scale changes.
  const zoomAt = (factor: number, cx = 0, cy = 0) => {
    const v = live.current;
    const z = clamp(v.zoom * factor);
    const k = z / v.zoom;
    apply({ zoom: z, x: cx - (cx - v.x) * k, y: cy - (cy - v.y) * k });
  };
  const actual = () => {
    const el = img.current;
    if (!el?.naturalWidth || !el.clientWidth) {
      reset();
      return;
    }
    apply({ zoom: clamp(el.naturalWidth / el.clientWidth), x: 0, y: 0 });
  };

  useEffect(() => {
    // What each key does; the arrows only when there is another image to go to.
    const keys = new Map<string, () => void>([
      ["+", () => zoomAt(ZOOM_STEP)],
      ["=", () => zoomAt(ZOOM_STEP)],
      ["-", () => zoomAt(1 / ZOOM_STEP)],
      ["0", reset],
      ["1", actual],
    ]);
    if (images.length > 1) {
      keys.set("ArrowRight", () => step(1));
      keys.set("ArrowLeft", () => step(-1));
    }
    const onKey = (e: KeyboardEvent) => {
      const all = document.querySelectorAll("[data-dialog-root]");
      if (all.length && all[all.length - 1] !== root.current) {
        return;
      }
      const run = keys.get(e.key);
      if (!run) {
        return;
      }
      run();
      e.preventDefault();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  });

  // The stage's centre is the origin of the transform, so pointer offsets are taken from it.
  const fromCentre = (e: { clientX: number; clientY: number }) => {
    const r = root.current!.getBoundingClientRect();
    return {
      x: e.clientX - r.left - r.width / 2,
      y: e.clientY - r.top - r.height / 2,
    };
  };

  // The stage is the picture itself, not a control: the pointer zooms and pans it, a click around
  // the image closes the preview, and the keyboard has its own keys for all of it (above, and
  // Escape through useDialog). A click in the bar stays in the bar.
  const onClick = (e: MouseEvent) => {
    if (e.target === e.currentTarget) {
      onClose();
    }
  };
  const onWheel = (e: WheelEvent) => {
    e.preventDefault();
    const c = fromCentre(e);
    zoomAt(choose(e.deltaY < 0, WHEEL_STEP, 1 / WHEEL_STEP), c.x, c.y);
  };
  const onDblClick = (e: MouseEvent) => {
    if (zoom !== 1) {
      reset();
    } else {
      const c = fromCentre(e);
      zoomAt(DOUBLE_CLICK_ZOOM, c.x, c.y);
    }
  };
  const onPointerDown = (e: PointerEvent) => {
    drag.current = {
      x: e.clientX,
      y: e.clientY,
      px: live.current.x,
      py: live.current.y,
    };
    (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
  };
  const onPointerMove = (e: PointerEvent) => {
    const d = drag.current;
    if (d) {
      apply({
        zoom: live.current.zoom,
        x: d.px + e.clientX - d.x,
        y: d.py + e.clientY - d.y,
      });
    }
  };
  const onPointerEnd = () => {
    drag.current = null;
  };
  useNativeEvents(stage, {
    click: onClick,
    wheel: onWheel,
    dblclick: onDblClick,
    pointerdown: onPointerDown,
    pointermove: onPointerMove,
    pointerup: onPointerEnd,
    pointercancel: onPointerEnd,
  });
  useNativeEvents(bar, { click: stopHere });

  const cur = images[index];
  // In a portal: a report draws this from inside its chat bubble (see BodyPortal).
  return (
    <BodyPortal>
      <dialog
        open
        ref={root}
        class="lb"
        data-dialog-root
        aria-modal="true"
        aria-label={cur.label}
        data-testid="lightbox"
      >
        <div ref={bar} class="lb-bar">
          <span class="lb-title">
            {cur.label}
            {choose(images.length > 1, ` (${index + 1}/${images.length})`, "")}
          </span>
          <button
            type="button"
            class="btn-outline"
            aria-label={t("Zoom out")}
            onClick={() => zoomAt(1 / ZOOM_STEP)}
          >
            −
          </button>
          <span class="lb-zoom" data-testid="lightbox-zoom">
            {Math.round(zoom * 100)}%
          </span>
          <button
            type="button"
            class="btn-outline"
            aria-label={t("Zoom in")}
            onClick={() => zoomAt(ZOOM_STEP)}
          >
            +
          </button>
          <button type="button" class="btn-outline" onClick={reset}>
            {t("Fit")}
          </button>
          <button type="button" class="btn-outline" onClick={actual}>
            1:1
          </button>
          {images.length > 1 && (
            <button
              type="button"
              class="btn-outline"
              aria-label={t("Previous image")}
              onClick={() => step(-1)}
            >
              ‹
            </button>
          )}
          {images.length > 1 && (
            <button
              type="button"
              class="btn-outline"
              aria-label={t("Next image")}
              onClick={() => step(1)}
            >
              ›
            </button>
          )}
          <button
            type="button"
            class="btn-outline"
            data-autofocus
            aria-label={t("Close")}
            onClick={onClose}
          >
            ✕
          </button>
        </div>
        <div ref={stage} class="lb-stage">
          <img
            ref={img}
            class="lb-img"
            src={cur.src}
            alt={cur.label}
            draggable={false}
            style={{
              transform: `translate(${panX}px, ${panY}px) scale(${zoom})`,
            }}
          />
        </div>
      </dialog>
    </BodyPortal>
  );
}
