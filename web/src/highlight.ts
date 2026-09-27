// Syntax highlighting, loaded on demand.
//
// This module and its grammars are imported only when an answer actually contains a
// code block: hljs with ten languages is ~180 KB, which is a fifth of what this page
// may weigh in total, and most answers have no code in them at all. The Markdown
// renderer leaves a placeholder and this fills it in after the message is mounted.
import hljs from 'highlight.js/lib/core'
import go from 'highlight.js/lib/languages/go'
import python from 'highlight.js/lib/languages/python'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import bash from 'highlight.js/lib/languages/bash'
import yaml from 'highlight.js/lib/languages/yaml'
import json from 'highlight.js/lib/languages/json'
import sql from 'highlight.js/lib/languages/sql'
import css from 'highlight.js/lib/languages/css'
import xml from 'highlight.js/lib/languages/xml'

// Registered per language rather than by importing the full package (5.5 MB of
// grammars): these are the languages the agent writes in. Adding one is one import
// and one line here.
const LANGUAGES: Record<string, Parameters<typeof hljs.registerLanguage>[1]> = {
  go,
  python,
  js: javascript,
  javascript,
  ts: typescript,
  typescript,
  bash,
  sh: bash,
  shell: bash,
  yaml,
  yml: yaml,
  json,
  sql,
  css,
  html: xml,
  xml,
}
for (const [name, lang] of Object.entries(LANGUAGES)) hljs.registerLanguage(name, lang)

/**
 * Highlights one code block, returning HTML.
 *
 * `code` is the RAW source, not escaped, and this function is what escapes it. That is
 * the whole contract, and getting it wrong is a displayed defect rather than a cosmetic
 * one: highlighting already-escaped input makes highlight.js escape it a SECOND time, so
 * a snippet containing `-->` is shown to the reader as `--&gt;`. Measured in the gate —
 * a three-word JavaScript sample came out with its arrow spelled in entities.
 *
 * An unknown language must still render as code, so the escape happens on every path;
 * only the colouring is skipped. A grammar that throws falls back the same way.
 */
export function highlight(code: string, lang: string): string {
  if (!lang || !hljs.getLanguage(lang)) return escapeHtml(code)
  try {
    return hljs.highlight(code, { language: lang, ignoreIllegals: true }).value
  } catch {
    return escapeHtml(code)
  }
}

/** Escapes text for a code element. Kept here so both paths agree on it. */
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}
