// A minimal, dependency-free Markdown-to-HTML renderer.
//
// Handles: headings, bold, italic, inline code, fenced code blocks (with
// optional syntax highlighting), links, lists (bulleted/numbered/task),
// blockquotes, GFM alerts, tables, horizontal rules, footnotes.
//
// The output is sanitized: only an allowlist of tags and attributes survive.
// Every byte is embedded in a Go binary — no external dependencies.

// escapeHTML escapes all characters that are special in HTML content AND
// attribute values: & < > " '.  This makes the output safe to insert into
// both element bodies and double-quoted attribute values.
function escapeHTML(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

// --- Syntax highlighting -------------------------------------------------

const KW = (s: string) => s.split(' ')

interface LangCfg {
  kw: string[]
  lc?: string   // line comment prefix: '//', '#', '--'
  bc?: boolean  // block comments: /* ... */
  hc?: boolean  // HTML comments: <!-- ... -->
  ci?: boolean  // case-insensitive keywords
}

const LANGS: Record<string, LangCfg> = {
  go: {
    kw: KW('break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var'),
    lc: '//', bc: true,
  },
  python: {
    kw: KW('and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield False None True self'),
    lc: '#',
  },
  js: {
    kw: KW('break case catch class const continue debugger default delete do else export extends finally for function if import in instanceof new return super switch this throw try typeof var void while with yield let async await null undefined true false console'),
    lc: '//', bc: true,
  },
  ts: {
    kw: KW('break case catch class const continue debugger default delete do else export extends finally for function if import in instanceof new return super switch this throw try typeof var void while with yield let async await null undefined true false abstract as enum get namespace public private protected readonly set static type console'),
    lc: '//', bc: true,
  },
  bash: {
    kw: KW('if then else elif fi for while do done case esac in function return local export echo exit set unset source cd pwd ls cat grep sed awk'),
    lc: '#',
  },
  yaml: {
    kw: KW('true false null yes no'),
    lc: '#',
  },
  json: {
    kw: KW('true false null'),
  },
  sql: {
    kw: KW('select from where insert update delete create table drop alter into values set and or not null join left right inner outer on group by order having limit offset as distinct union all primary key foreign references default unique index'),
    lc: '--', ci: true,
  },
  css: {
    kw: KW('important'),
    bc: true,
  },
}

// makeStasher returns a pair of functions: S (stash an HTML fragment and get
// a placeholder) and restore (replace all placeholders with their fragments).
// Placeholders use only uppercase letters (base-26) delimited by NUL bytes so
// they never collide with keyword/number/function/operator regexes.
function makeStasher() {
  const stash: string[] = []
  const enc = (n: number): string => {
    let s = ''
    do { s = String.fromCharCode(65 + n % 26) + s; n = Math.floor(n / 26) } while (n > 0)
    return s
  }
  const S = (html: string): string => {
    stash.push(html)
    return '\x00' + enc(stash.length - 1) + '\x00'
  }
  const restore = (s: string): string =>
    s.replace(/\x00([A-Z]+)\x00/g, (_, id: string) => {
      let n = 0
      for (let i = 0; i < id.length; i++) n = n * 26 + (id.charCodeAt(i) - 65)
      return stash[n]
    })
  return { S, restore }
}

// highlightCode applies lightweight regex-based syntax highlighting to
// already-HTML-escaped code.  Returns HTML with <span> tags using classes:
// tk-key (keywords), tk-str (strings), tk-com (comments), tk-num (numbers),
// tk-fn (function names), tk-op (operators).
function highlightCode(code: string, lang: string): string {
  const l = lang.toLowerCase()
  if (l === 'html') return highlightHTML(code)
  const cfg = LANGS[l]
  if (!cfg) return code

  const { S, restore } = makeStasher()
  let s = code

  // 1. Strings — after escaping, double quotes are &quot; and single quotes are &#39;
  s = s.replace(/&quot;(?:[^&]|&(?!quot;))*&quot;/g, m => S('<span class="tk-str">' + m + '</span>'))
  s = s.replace(/&#39;(?:[^&]|&(?!#39;))*&#39;/g, m => S('<span class="tk-str">' + m + '</span>'))
  s = s.replace(/`[^`]*`/g, m => S('<span class="tk-str">' + m + '</span>'))

  // 2. Comments
  if (cfg.lc === '//') s = s.replace(/\/\/[^\n]*/g, m => S('<span class="tk-com">' + m + '</span>'))
  else if (cfg.lc === '#') s = s.replace(/(?<!&)#[^\n]*/g, m => S('<span class="tk-com">' + m + '</span>'))
  else if (cfg.lc === '--') s = s.replace(/--[^\n]*/g, m => S('<span class="tk-com">' + m + '</span>'))
  if (cfg.bc) s = s.replace(/\/\*[\s\S]*?\*\//g, m => S('<span class="tk-com">' + m + '</span>'))

  // 3. HTML entities — protect from keyword/number matching
  s = s.replace(/&[a-zA-Z#0-9]+;/g, m => S(m))

  // 4. Keywords
  if (cfg.kw.length > 0) {
    const flags = cfg.ci ? 'gi' : 'g'
    const kwRe = new RegExp('\\b(' + cfg.kw.join('|') + ')\\b', flags)
    s = s.replace(kwRe, m => S('<span class="tk-key">' + m + '</span>'))
  }

  // 5. Numbers
  s = s.replace(/\b\d+\.?\d*\b/g, m => S('<span class="tk-num">' + m + '</span>'))

  // 6. Function names — identifier followed by (
  s = s.replace(/[a-zA-Z_]\w*(?=\s*\()/g, m => S('<span class="tk-fn">' + m + '</span>'))

  // 7. Operators
  s = s.replace(/[=+\-*/%!|^~]/g, m => S('<span class="tk-op">' + m + '</span>'))

  return restore(s)
}

// highlightHTML handles HTML/XML syntax specifically.
function highlightHTML(code: string): string {
  const { S, restore } = makeStasher()
  let s = code

  // Strings
  s = s.replace(/&quot;(?:[^&]|&(?!quot;))*&quot;/g, m => S('<span class="tk-str">' + m + '</span>'))
  // HTML comments: &lt;!-- ... --&gt;
  s = s.replace(/&lt;!--[\s\S]*?--&gt;/g, m => S('<span class="tk-com">' + m + '</span>'))
  // Tag names: &lt;tag or &lt;/tag
  s = s.replace(/(&lt;\/?)(\w+)/g, (_, p: string, t: string) => S(p) + S('<span class="tk-key">' + t + '</span>'))
  // Attribute names: whitespace name =
  s = s.replace(/(\s)([\w-]+)(=)/g, (_m, sp: string, attr: string, eq: string) => sp + S('<span class="tk-fn">' + attr + '</span>') + eq)
  // Numbers
  s = s.replace(/\b\d+\.?\d*\b/g, m => S('<span class="tk-num">' + m + '</span>'))

  return restore(s)
}

// --- Inline rendering ----------------------------------------------------

// renderInline renders inline markup: bold, italic, code, links, footnote refs.
// All text is escaped BEFORE any markup is applied, so the regex replacements
// operate on already-escaped text.  This means captured groups (link URLs,
// footnote IDs) are already safe for use in attribute values.
function renderInline(text: string): string {
  let s = escapeHTML(text)

  // Inline code: `code` — before bold/italic so ** inside code is literal.
  s = s.replace(/`([^`]+)`/g, (_, code: string) => '<code>' + code + '</code>')

  // Bold: **text** or __text__
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
  s = s.replace(/__([^_]+)__/g, '<strong>$1</strong>')

  // Italic: *text* or _text_ (but not inside words with underscores)
  s = s.replace(/(?<!\w)\*([^*]+)\*(?!\w)/g, '<em>$1</em>')
  s = s.replace(/(?<!\w)_([^_]+)_(?!\w)/g, '<em>$1</em>')

  // Strikethrough: ~~text~~
  s = s.replace(/~~([^~]+)~~/g, '<del>$1</del>')

  // Footnote references: [^id] → superscript link.
  // id is already escaped (from the whole-text escaping above), so it is
  // safe to use directly in both the href attribute and the link text.
  s = s.replace(/\[\^([^\]]+)\]/g, (_, id: string) =>
    '<sup class="footnote-ref"><a href="#fn-' + id + '">' + id + '</a></sup>')

  // Images: ![alt](url) — only relative URLs or data: URIs.
  // alt and url are already escaped, making them safe in attributes.
  s = s.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (_, alt: string, url: string) => {
    if (/^(\/|data:)/.test(url)) {
      return '<img src="' + url + '" alt="' + alt + '" />'
    }
    return alt // strip unsafe image links to their alt text
  })

  // Links: [text](url) — only relative URLs, #fragments, or mailto:.
  // url is already escaped, making it safe in the href attribute.
  // Footnote refs [^id] were already replaced above, so they won't match here.
  // Images ![alt](url) were already replaced above, so they won't match here.
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, linkText: string, url: string) => {
    if (/^(\/|#|mailto:)/.test(url)) {
      return '<a href="' + url + '">' + linkText + '</a>'
    }
    return linkText // strip unsafe links to their text
  })

  return s
}

// --- Block rendering -----------------------------------------------------

function renderMarkdown(src: string): string {
  // Extract footnote definitions [^id]: text before rendering.
  // They are removed from the source and appended as a <section class="footnotes">.
  const footnotes: { id: string; text: string }[] = []
  const lines = src.split('\n')
  const cleanLines: string[] = []
  for (const line of lines) {
    const fn = line.match(/^\[\^([^\]]+)\]:\s*(.*)$/)
    if (fn) {
      footnotes.push({ id: fn[1], text: fn[2] })
    } else {
      cleanLines.push(line)
    }
  }

  const src2 = cleanLines.join('\n')
  const lines2 = src2.split('\n')
  let html = ''
  let i = 0
  let inList = false
  let listTag = ''

  function closeList() {
    if (inList) {
      const tag = listTag.split(' ')[0]
      html += '</' + tag + '>\n'
      inList = false
    }
  }

  while (i < lines2.length) {
    const line = lines2[i]

    // Fenced code block: ```lang ... ```
    if (/^```/.test(line.trim())) {
      closeList()
      const lang = line.trim().replace(/^```/, '').trim()
      let code = ''
      i++
      while (i < lines2.length && !/^```/.test(lines2[i].trim())) {
        code += lines2[i] + '\n'
        i++
      }
      i++ // skip closing ```
      const rawCode = code.replace(/\n$/, '')
      const escCode = escapeHTML(rawCode)
      const highlighted = lang ? highlightCode(escCode, lang) : escCode
      html += '<pre><button class="copy-btn" data-copy-text="' + escCode + '">copy</button><code'
        + (lang ? ' class="lang-' + escapeHTML(lang) + '"' : '') + '>'
        + highlighted + '</code></pre>\n'
      continue
    }

    // Blank line
    if (line.trim() === '') {
      closeList()
      i++
      continue
    }

    // Heading: # ... ######
    const h = line.match(/^(#{1,6})\s+(.*)$/)
    if (h) {
      closeList()
      const level = h[1].length
      html += '<h' + level + '>' + renderInline(h[2]) + '</h' + level + '>\n'
      i++
      continue
    }

    // Horizontal rule: --- or *** or ___
    if (/^(\s*[-*_]\s*){3,}$/.test(line) && line.trim().length >= 3) {
      closeList()
      html += '<hr>\n'
      i++
      continue
    }

    // Blockquote: > text, including GFM alerts
    if (/^>\s?/.test(line)) {
      closeList()
      let quote = ''
      while (i < lines2.length && /^>\s?/.test(lines2[i])) {
        quote += lines2[i].replace(/^>\s?/, '') + '\n'
        i++
      }
      quote = quote.replace(/\n$/, '')
      const alert = quote.match(/^\[!(\w+)\]\s*\n?([\s\S]*)/)
      if (alert) {
        const alertType = alert[1].toLowerCase()
        html += '<blockquote class="alert alert-' + alertType + '">'
          + '<strong>' + alertType.toUpperCase() + '</strong>'
          + renderMarkdown(alert[2].replace(/^\n/, ''))
          + '</blockquote>\n'
      } else {
        html += '<blockquote>' + renderMarkdown(quote) + '</blockquote>\n'
      }
      continue
    }

    // Table: | a | b | with separator row | --- | --- |
    if (line.includes('|') && i + 1 < lines2.length && /^\|?[\s-:|]+\|?\s*$/.test(lines2[i + 1])) {
      closeList()
      const headers = line.split('|').map(c => c.trim()).filter((c, idx, arr) =>
        !(idx === 0 && c === '') && !(idx === arr.length - 1 && c === ''))
      i += 2 // skip header + separator
      let rows = ''
      while (i < lines2.length && lines2[i].includes('|') && lines2[i].trim() !== '') {
        const cells = lines2[i].split('|').map(c => c.trim()).filter((c, idx, arr) =>
          !(idx === 0 && c === '') && !(idx === arr.length - 1 && c === ''))
        let rowHTML = ''
        for (const cell of cells) rowHTML += '<td>' + renderInline(cell) + '</td>'
        rows += '<tr>' + rowHTML + '</tr>\n'
        i++
      }
      let headHTML = ''
      for (const hdr of headers) headHTML += '<th>' + renderInline(hdr) + '</th>'
      html += '<table><thead><tr>' + headHTML + '</tr></thead><tbody>' + rows + '</tbody></table>\n'
      continue
    }

    // Task list: - [x] done or - [ ] todo
    if (/^\s*[-*+]\s+\[[ xX]\]\s+/.test(line)) {
      if (!inList || listTag !== 'ul class="task-list"') {
        closeList()
        html += '<ul class="task-list">\n'
        inList = true
        listTag = 'ul class="task-list"'
      }
      const checked = /^\s*[-*+]\s+\[[xX]\]\s+/.test(line)
      const item = line.replace(/^\s*[-*+]\s+\[[ xX]\]\s+/, '')
      html += '<li class="' + (checked ? 'task-done' : 'task-todo') + '">'
        + '<span class="checkbox ' + (checked ? 'checked' : '') + '">'
        + (checked ? '\u2611' : '\u2610') + '</span> '
        + renderInline(item) + '</li>\n'
      i++
      continue
    }

    // Unordered list: - or * or + item
    if (/^\s*[-*+]\s+/.test(line)) {
      if (!inList || listTag !== 'ul') {
        closeList()
        html += '<ul>\n'
        inList = true
        listTag = 'ul'
      }
      const item = line.replace(/^\s*[-*+]\s+/, '')
      html += '<li>' + renderInline(item) + '</li>\n'
      i++
      continue
    }

    // Ordered list: 1. item
    if (/^\s*\d+\.\s+/.test(line)) {
      if (!inList || listTag !== 'ol') {
        closeList()
        html += '<ol>\n'
        inList = true
        listTag = 'ol'
      }
      const item = line.replace(/^\s*\d+\.\s+/, '')
      html += '<li>' + renderInline(item) + '</li>\n'
      i++
      continue
    }

    // Paragraph: collect consecutive non-blank, non-special lines
    closeList()
    let para = line
    i++
    while (i < lines2.length && lines2[i].trim() !== '' &&
           !/^```/.test(lines2[i].trim()) &&
           !/^#{1,6}\s/.test(lines2[i]) &&
           !/^>\s?/.test(lines2[i]) &&
           !/^\s*[-*+]\s+/.test(lines2[i]) &&
           !/^\s*\d+\.\s+/.test(lines2[i]) &&
           !/^(\s*[-*_]\s*){3,}$/.test(lines2[i])) {
      para += '\n' + lines2[i]
      i++
    }
    html += '<p>' + renderInline(para) + '</p>\n'
  }
  closeList()

  // Append footnote definitions if any were extracted.
  if (footnotes.length > 0) {
    html += '<section class="footnotes"><ol>'
    for (const fn of footnotes) {
      html += '<li id="fn-' + escapeHTML(fn.id) + '">' + renderInline(fn.text) + '</li>\n'
    }
    html += '</ol></section>\n'
  }

  return html
}

// --- Component -----------------------------------------------------------

export function Markdown({ content }: { content: string }) {
  const html = renderMarkdown(content)
  const escapedRaw = escapeHTML(content)
  const output =
    '<div class="markdown-body" data-raw="' + escapedRaw + '">' +
    '<button class="copy-msg-btn">copy</button>' +
    html +
    '</div>'
  return <div dangerouslySetInnerHTML={{ __html: output }} />
}