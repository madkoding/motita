// The report's before/after evidence and the full-window image preview. The components are TSX,
// which node cannot import here, so these check the wiring in the source the way the other UI
// tests check the CSS: the pieces that make it work must be present and connected.
// Run with: node --test web/src/__tests__/
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const src = (f) => readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', f), 'utf8')
const report = src('Report.tsx')
const lightbox = src('Lightbox.tsx')
const css = src('report.css')
const app = src('App.tsx')

test('a report that only has screenshots still draws a card', () => {
  assert.match(report, /r\.evidence\?\.length/)
})

test('before and after sit side by side and fall back to a column on a narrow screen', () => {
  assert.match(css, /\.rpt-shots\s*\{[^}]*grid-template-columns:\s*repeat\(auto-fit,\s*minmax\(min\(100%/)
})

test('clicking a screenshot opens the preview with the pair, after loading it with the token', () => {
  assert.match(report, /<Lightbox images=\{zoom\.images\}/)
  assert.match(report, /urls\.current\.get\(other\)/)
  assert.match(app, /loadEvidenceImage[\s\S]*artifactBase\(\)/)
  assert.match(app, /<ReportCard report=\{m\.report\} loadImage=\{loadEvidenceImage\}/)
})

test('the preview fills the window and has zoom, pan, fit, actual size and keyboard control', () => {
  assert.match(css, /\.lb\s*\{[^}]*position:\s*fixed;\s*inset:\s*0/)
  for (const handler of ['onWheel', 'onPointerMove', 'onDblClick']) assert.ok(lightbox.includes(handler), handler)
  for (const key of ["'+'", "'-'", "'0'", "'1'", "'ArrowRight'", "'ArrowLeft'"]) assert.ok(lightbox.includes(key), key)
  assert.ok(lightbox.includes('useDialog'), 'Escape and focus come from the shared dialog behaviour')
})

test('a picture in an answer opens the same preview, unless it is a link', () => {
  assert.match(app, /target\.tagName === 'IMG' && target\.closest\('\.markdown-body'\) && !target\.closest\('a'\)/)
  assert.match(app, /<Lightbox images=\{\[previewImage\]\}/)
})
