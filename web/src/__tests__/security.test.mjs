// The browser-side halves of two gateway defences. The components are TSX, which node cannot
// import here, so these check the wiring in the source the way the other UI tests do.
// Run with: node --test web/src/__tests__/
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const src = (f) => readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', f), 'utf8')
const app = src('App.tsx')
const markdown = src('Markdown.tsx')

test('every API request carries the X-Motita header the gateway requires with the cookie', () => {
  assert.match(app, /async function api\([\s\S]*?headers\.set\('X-Motita', '1'\)[\s\S]*?fetch\(path, \{[^}]*headers \}\)/)
})

test('a message is sanitized without free-form styles or form controls', () => {
  assert.match(markdown, /const FORM_TAGS = \['form', 'button', 'textarea', 'select', 'option', 'optgroup', 'input'\]/)
  assert.match(markdown, /data\.attrName === 'style' && !ALIGN_ONLY\.test\(data\.attrValue\)/)
  assert.match(markdown, /sanitizeMessage\(md\.render\(source\)\)/)
  // The fence no longer writes a button into the message; sanitizeMessage adds it afterwards.
  assert.doesNotMatch(markdown, /'"><button class="copy-btn">'/)
})
