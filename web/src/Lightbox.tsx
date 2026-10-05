import { useEffect, useRef, useState } from 'preact/hooks'
import { useDialog } from './useDialog'
import { t } from './i18n'

export interface LightboxImage { src: string; label: string }

const MIN = 0.1
const MAX = 16
const clamp = (z: number) => Math.min(MAX, Math.max(MIN, z))

// Lightbox is the full-window preview of an image: wheel or +/- to zoom around the pointer, drag
// to pan, 0 to fit, 1 for actual size, and the arrow keys to move between the images of a set
// (the "before" and the "after" of a report). Zoom 1 means "fit the window"; the real pixel size
// is reached by the 100% button, which needs the image's natural size.
export function Lightbox({ images, start, onClose }: { images: LightboxImage[]; start: number; onClose: () => void }) {
  const root = useRef<HTMLDivElement>(null)
  const img = useRef<HTMLImageElement>(null)
  const [index, setIndex] = useState(Math.min(Math.max(start, 0), images.length - 1))
  const [view, setView] = useState({ zoom: 1, x: 0, y: 0 })
  // `live` is the latest view, so two events before a re-render (key repeat, a fast wheel) each
  // build on the previous one instead of on the stale value of the render they came from.
  const live = useRef(view)
  const apply = (v: { zoom: number; x: number; y: number }) => { live.current = v; setView(v) }
  const { zoom, x: panX, y: panY } = view
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null)
  useDialog(root, onClose)

  const reset = () => apply({ zoom: 1, x: 0, y: 0 })
  const go = (i: number) => { setIndex((i + images.length) % images.length); reset() }
  // zoomAt keeps the point under the pointer fixed while the scale changes.
  const zoomAt = (factor: number, cx = 0, cy = 0) => {
    const v = live.current
    const z = clamp(v.zoom * factor)
    const k = z / v.zoom
    apply({ zoom: z, x: cx - (cx - v.x) * k, y: cy - (cy - v.y) * k })
  }
  const actual = () => {
    const el = img.current
    if (!el || !el.naturalWidth || !el.clientWidth) return reset()
    apply({ zoom: clamp(el.naturalWidth / el.clientWidth), x: 0, y: 0 })
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const all = document.querySelectorAll('[data-dialog-root]')
      if (all.length && all[all.length - 1] !== root.current) return
      if (e.key === '+' || e.key === '=') zoomAt(1.25)
      else if (e.key === '-') zoomAt(0.8)
      else if (e.key === '0') reset()
      else if (e.key === '1') actual()
      else if (e.key === 'ArrowRight' && images.length > 1) go(index + 1)
      else if (e.key === 'ArrowLeft' && images.length > 1) go(index - 1)
      else return
      e.preventDefault()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  })

  // The stage's centre is the origin of the transform, so pointer offsets are taken from it.
  const fromCentre = (e: { clientX: number; clientY: number }) => {
    const r = root.current!.getBoundingClientRect()
    return { x: e.clientX - r.left - r.width / 2, y: e.clientY - r.top - r.height / 2 }
  }
  const cur = images[index]
  return (
    <div ref={root} class="lb" data-dialog-root role="dialog" aria-modal="true" aria-label={cur.label} data-testid="lightbox">
      <div class="lb-bar" onClick={(e) => e.stopPropagation()}>
        <span class="lb-title">{cur.label}{images.length > 1 ? ` (${index + 1}/${images.length})` : ''}</span>
        <button type="button" class="btn-outline" aria-label={t('Zoom out')} onClick={() => zoomAt(0.8)}>−</button>
        <span class="lb-zoom" data-testid="lightbox-zoom">{Math.round(zoom * 100)}%</span>
        <button type="button" class="btn-outline" aria-label={t('Zoom in')} onClick={() => zoomAt(1.25)}>+</button>
        <button type="button" class="btn-outline" onClick={reset}>{t('Fit')}</button>
        <button type="button" class="btn-outline" onClick={actual}>1:1</button>
        {images.length > 1 && <button type="button" class="btn-outline" aria-label={t('Previous image')} onClick={() => go(index - 1)}>‹</button>}
        {images.length > 1 && <button type="button" class="btn-outline" aria-label={t('Next image')} onClick={() => go(index + 1)}>›</button>}
        <button type="button" class="btn-outline" data-autofocus aria-label={t('Close')} onClick={onClose}>✕</button>
      </div>
      <div
        class="lb-stage"
        onClick={(e) => { if (e.target === e.currentTarget) onClose() }}
        onWheel={(e) => { e.preventDefault(); const c = fromCentre(e); zoomAt(e.deltaY < 0 ? 1.15 : 1 / 1.15, c.x, c.y) }}
        onDblClick={(e) => { if (zoom !== 1) reset(); else { const c = fromCentre(e); zoomAt(2.5, c.x, c.y) } }}
        onPointerDown={(e) => { drag.current = { x: e.clientX, y: e.clientY, px: live.current.x, py: live.current.y }; (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId) }}
        onPointerMove={(e) => { const d = drag.current; if (d) apply({ zoom: live.current.zoom, x: d.px + e.clientX - d.x, y: d.py + e.clientY - d.y }) }}
        onPointerUp={() => { drag.current = null }}
        onPointerCancel={() => { drag.current = null }}
      >
        <img
          ref={img}
          class="lb-img"
          src={cur.src}
          alt={cur.label}
          draggable={false}
          style={{ transform: `translate(${panX}px, ${panY}px) scale(${zoom})` }}
        />
      </div>
    </div>
  )
}
