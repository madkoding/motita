package gateway

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/madkoding/motita/internal/notes"
	"github.com/madkoding/motita/internal/redact"
)

// The chapters of the manual as text: what each folder says about itself, its files and its links.

// chapter is the note of one folder: the module, where its note goes and what the model wrote.
type chapter struct {
	m               *moduleInfo
	name            string
	explanation, fp string
}

// manualCtx is what every chapter of one manual needs: where each chapter is, and the links up.
type manualCtx struct {
	title, project, arch, start string
	chapters                    map[string]*chapter
}

// chapterName is the file name of a folder's chapter: its path, flat, read as a trail.
func chapterName(rel string) string {
	if rel == "" {
		return manualStart
	}
	return strings.ReplaceAll(rel, "/", manualSep)
}

// humanName is how the manual calls a folder: its last part, in words.
func humanName(rel string) string {
	name := strings.Trim(strings.NewReplacer("_", " ", "-", " ").Replace(path.Base(rel)), " ")
	if name == "" || name == "." {
		return path.Base(rel)
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// aliasLink links a note under a label of its own.
func aliasLink(name, label string) string {
	label = strings.NewReplacer("|", " ", "[", "", "]", "", "\n", " ").Replace(label)
	return "[[" + strings.TrimSuffix(name, ".md") + "|" + label + "]]"
}

func (c manualCtx) link(rel string) string {
	if ch, ok := c.chapters[rel]; ok && rel == "" {
		return aliasLink(ch.name, c.title+manualTitleSuffix)
	}
	if ch, ok := c.chapters[rel]; ok {
		return aliasLink(ch.name, humanName(rel))
	}
	return "`" + cell(rel) + "/`"
}

// plain is a text of the project made safe to sit in a note: no secret, no marker, no link.
func plain(text string) string {
	return strings.NewReplacer(notes.BlockStart, "", notes.BlockEnd, "", "[[", "[ [", "<!--", "").
		Replace(redact.String(text))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// mainLanguage is the language most of the folder's files are written in, or "".
func (m *moduleInfo) mainLanguage() string {
	count := map[string]int{}
	best := ""
	for _, fi := range m.files {
		if fi.lang == "" {
			continue
		}
		count[fi.lang]++
		if count[fi.lang] > count[best] || (count[fi.lang] == count[best] && fi.lang < best) {
			best = fi.lang
		}
	}
	return best
}

// contents says in one sentence what the folder holds.
func (m *moduleInfo) contents() string {
	var parts []string
	if n := len(m.children); n > 0 {
		parts = append(parts, plural(n, "folder"))
	}
	if n := len(m.files); n > 0 {
		parts = append(parts, plural(n, "file"))
	}
	if len(parts) == 0 {
		return "An empty folder."
	}
	line := "It holds " + strings.Join(parts, " and ")
	if lang := m.mainLanguage(); lang != "" {
		line += ", mostly " + lang
	}
	return line + "."
}

// leadParagraph is the first paragraph of a text that is prose: no heading, badge, markup, rule
// or code.
func leadParagraph(text string) string {
	var lines []string
	fenced := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "-*>"))
		if strings.HasPrefix(strings.TrimSpace(raw), "```") {
			fenced = !fenced
		}
		switch {
		case fenced || !prose(line):
			if len(lines) > 0 && line == "" {
				return strings.Join(lines, " ")
			}
		default:
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " ")
}

func prose(line string) bool {
	if line == "" {
		return false
	}
	for _, mark := range []string{"#", "<", "[", "![", "`", "|"} {
		if strings.HasPrefix(line, mark) {
			return false
		}
	}
	return true
}

// leadSentence is the first sentence of a text's first paragraph, or "".
func leadSentence(text string) string {
	line := leadParagraph(text)
	if i := strings.Index(line, ". "); i >= 0 {
		line = line[:i+1]
	}
	return clip(line, manualSummaryBytes)
}

// summary is the one line other chapters show about this one.
func (ch *chapter) summary() string {
	for _, text := range []string{ch.explanation, ch.m.pkgDoc, ch.m.readme} {
		if line := leadSentence(text); line != "" {
			return plain(line)
		}
	}
	return ch.m.contents()
}

// purpose is what the chapter says the folder is for: the model's explanation, or what the code
// says of itself.
func (ch *chapter) purpose() string {
	if ch.explanation != "" {
		return keptExplanation(ch.explanation, ch.fp)
	}
	var b strings.Builder
	switch {
	case ch.m.pkgDoc != "":
		b.WriteString(plain(ch.m.pkgDoc) + "\n\n")
	case ch.m.readme != "":
		b.WriteString(plain(clip(leadParagraph(ch.m.readme), manualDocBytes)) + "\n\n")
	default:
		b.WriteString(manualHint + "\n")
	}
	return b.String()
}

// isTest says whether a file holds tests, which the manual counts instead of listing.
func isTest(name string) bool {
	lower := strings.ToLower(name)
	for _, mark := range []string{"_test.", ".test.", ".spec.", "test_"} {
		if strings.Contains(lower, mark) {
			return true
		}
	}
	return false
}

// fileList is the files of the folder, each with what it is; the tests are counted on one line.
func (m *moduleInfo) fileList() string {
	var files []fileInfo
	tests := 0
	for _, fi := range m.files {
		if isTest(fi.name) {
			tests++
			continue
		}
		files = append(files, fi)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	var b strings.Builder
	for _, fi := range files[:min(len(files), manualMaxFiles)] {
		var about []string
		if fi.lang != "" {
			about = append(about, fi.lang)
		}
		about = append(about, plural(fi.lines, "line"))
		line := "- **" + cell(fi.name) + "** (" + strings.Join(about, ", ") + ")"
		if fi.doc != "" {
			line += ": " + cell(fi.doc)
		}
		b.WriteString(line + "\n")
	}
	if len(files) > manualMaxFiles {
		fmt.Fprintf(&b, "- and %d more files.\n", len(files)-manualMaxFiles)
	}
	if tests > 0 {
		fmt.Fprintf(&b, "- and %s checking them.\n", plural(tests, "test file"))
	}
	return b.String()
}

// folderList is the subfolders of a folder with what each one is for.
func (c manualCtx) folderList(rels []string, indent string, deep bool, b *strings.Builder) {
	rels = append([]string{}, rels...)
	sort.Strings(rels)
	for _, rel := range rels {
		ch, ok := c.chapters[rel]
		if !ok {
			continue
		}
		fmt.Fprintf(b, "%s- %s: %s\n", indent, c.link(rel), ch.summary())
		if deep {
			c.folderList(ch.m.children, indent+"  ", true, b)
		}
	}
}

func (c manualCtx) inside(m *moduleInfo) string {
	var b strings.Builder
	if len(m.children) > 0 {
		b.WriteString("\n### Folders\n")
		c.folderList(m.children, "", false, &b)
	}
	if files := m.fileList(); files != "" {
		b.WriteString("\n### Files\n" + files)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n## What is inside\n" + m.contents() + "\n" + b.String()
}

func (c manualCtx) linkLine(lead string, rels []string) string {
	if len(rels) == 0 {
		return ""
	}
	sort.Strings(rels)
	links := make([]string, 0, len(rels))
	for _, rel := range rels {
		links = append(links, c.link(rel))
	}
	return lead + " " + strings.Join(links, ", ") + ".\n\n"
}

func (c manualCtx) connections(m *moduleInfo) string {
	text := c.linkLine("It relies on", keys(m.uses)) + c.linkLine("It is used by", m.usedBy)
	if text == "" {
		return ""
	}
	return "\n## How it connects\n" + text
}

func sortedSymbols(list []symbol) []symbol {
	sort.Slice(list, func(i, j int) bool { return list[i].sig < list[j].sig })
	return list
}

func symbolLine(indent string, s symbol) string {
	line := indent + "- `" + strings.ReplaceAll(s.sig, "`", "'") + "` in " + cell(s.file)
	if s.doc != "" {
		line += ": " + cell(s.doc)
	}
	return line + "\n"
}

func symbolList(title string, list []symbol) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n#### " + title + "\n")
	for _, s := range sortedSymbols(list) {
		b.WriteString(symbolLine("", s))
	}
	return b.String()
}

func (m *moduleInfo) goSection() string {
	if m.pkgName == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### Go package `" + cell(m.pkgName) + "`\n")
	if len(m.types) > 0 {
		sort.Slice(m.types, func(i, j int) bool { return m.types[i].name < m.types[j].name })
		b.WriteString("\n#### Types\n")
		for _, t := range m.types {
			b.WriteString(symbolLine("", t.symbol))
			for _, meth := range sortedSymbols(m.methods[t.name]) {
				b.WriteString(symbolLine("  ", meth))
			}
		}
	}
	b.WriteString(symbolList("Functions", m.funcs))
	b.WriteString(symbolList("Constants and variables", m.values))
	return b.String()
}

// reference is the API of the folder, for whoever changes its code.
func (m *moduleInfo) reference() string {
	text := m.goSection()
	if len(m.exports) > 0 {
		text += "\n### Exports\n" + strings.TrimPrefix(symbolList("", m.exports), "\n#### \n")
	}
	if text == "" {
		return ""
	}
	return "\n## For developers\n" +
		"The names this folder offers to the rest of the code, with what their comments say.\n" + text
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// modelFacts is what the model and the fingerprint see of a folder: nothing another chapter wrote.
func (m *moduleInfo) modelFacts() string {
	var b strings.Builder
	b.WriteString("## Folder `" + cell(m.rel) + "/`\n" + m.contents() + "\n")
	if m.pkgDoc != "" {
		b.WriteString("\nPackage doc: " + m.pkgDoc + "\n")
	}
	b.WriteString("\n### Folders\n" + strings.Join(sorted(m.children), "\n") + "\n\n### Files\n" + m.fileList())
	b.WriteString(m.reference())
	b.WriteString("\n### Uses\n" + strings.Join(sorted(keys(m.uses)), "\n") +
		"\n\n### Used by\n" + strings.Join(sorted(m.usedBy), "\n") + "\n")
	return b.String()
}

func sorted(list []string) []string {
	sort.Strings(list)
	return list
}

// trail is where a chapter sits in the guide, from its start down to the chapter itself; every part
// but the last leads to its own chapter.
func (c manualCtx) trail(rel string) string {
	parts := []string{"**" + cell(humanName(rel)) + "**"}
	for r := parentRel(rel); ; r = parentRel(r) {
		parts = append([]string{c.link(r)}, parts...)
		if r == "" {
			return strings.Join(parts, manualSep)
		}
	}
}

// neighbours ends a chapter with the way on: the chapters before and after it in the folder above,
// and the way back up.
func (c manualCtx) neighbours(rel string) string {
	var siblings []string
	for _, r := range sorted(append([]string{}, c.chapters[parentRel(rel)].m.children...)) {
		if _, ok := c.chapters[r]; ok {
			siblings = append(siblings, r)
		}
	}
	at := slices.Index(siblings, rel)
	var steps []string
	if at > 0 {
		steps = append(steps, "← Before: "+c.link(siblings[at-1]))
	}
	steps = append(steps, "Up: "+c.link(parentRel(rel)))
	if at < len(siblings)-1 {
		steps = append(steps, "Next: "+c.link(siblings[at+1])+" →")
	}
	return "\n## Keep reading\n" + strings.Join(steps, " · ") + "\n"
}

// startBody is the first note of the guide: what the project is and the contents of the guide.
func (c manualCtx) startBody(ch *chapter) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*A guide to %s, written by motita from its files. Read it from the top, or jump to the "+
		"part you need from the contents below. See also the project page %s and the technical "+
		"overview %s.*\n", cell(c.title), c.project, c.arch)
	b.WriteString("\n## What this project is\n" + ch.purpose())
	if len(ch.m.children) > 0 {
		b.WriteString("\n## Contents\nEvery part of the project has its own chapter. " +
			"Pick one to read what it is for.\n\n")
		c.folderList(ch.m.children, "", true, &b)
	}
	if files := ch.m.fileList(); files != "" {
		b.WriteString("\n## Files at the top\n" + files)
	}
	b.WriteString(c.connections(ch.m))
	b.WriteString(ch.m.reference())
	return b.String()
}

func (c manualCtx) chapterBody(ch *chapter) string {
	if ch.m.rel == "" {
		return c.startBody(ch)
	}
	var b strings.Builder
	b.WriteString("*" + c.trail(ch.m.rel) + "*\n")
	b.WriteString("\n## What it is for\n" + ch.purpose())
	b.WriteString(c.inside(ch.m))
	b.WriteString(c.connections(ch.m))
	b.WriteString(c.neighbours(ch.m.rel))
	b.WriteString(ch.m.reference())
	return b.String()
}
