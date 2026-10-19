package gateway

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/madkoding/motita/internal/notes"
	"github.com/madkoding/motita/internal/redact"
)

// The manual is the project written down as a guide: "Start here" says what the project is and how
// it is organised, and every folder, recursively, has a chapter that says what it is for, what is
// inside, how it connects to the rest and, last, the API it offers. Everything comes from the files;
// with the model on, each chapter also gets an explanation in plain words, kept until the folder
// changes.
const (
	manualSuffix       = " - Manual"
	manualOldSuffix    = " manual"
	manualStart        = "Start here"
	manualSep          = " › "
	manualSummaryBytes = 220
	manualDocBytes     = 1500
	manualSymbolDoc    = 300
	manualTypeBytes    = 80
	manualReadBytes    = 1 << 20
	manualSourceBytes  = 1500
	manualSourcesBytes = 12000
	manualModelBytes   = 16000
	manualExplainMax   = 30
	manualExplainLimit = 2000
	manualHeadLines    = 30
	manualHint         = "_No description of this folder yet. Turn on the model in Settings, Indexing, " +
		"and the next scan explains it in plain words._"
)

// The caps of one manual: folders, file rows per note and API entries per note. Variables so a test
// can lower them.
var (
	manualMaxModules = 400
	manualMaxFiles   = 300
	manualMaxSymbols = 200
	// openFile opens a file of the project; a test can make it fail.
	openFile = os.Open
)

var (
	jsExtensions = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true}
	jsExport     = regexp.MustCompile(
		`^export\s+(?:default\s+)?(?:async\s+)?` +
			`(function\*?|class|const|let|var|interface|type|enum)\s+([A-Za-z0-9_$]+)`)
	jsImport = regexp.MustCompile(`^(?:import|export)\b.*["'](\.{1,2}/[^"']+)["']`)
	pyDef    = regexp.MustCompile(`^(def|class)\s+([A-Za-z0-9_]+)`)
)

type fileInfo struct {
	name, lang, doc string
	lines           int
}

type symbol struct {
	sig, doc, file string
}

type goType struct {
	symbol
	name string
}

// moduleInfo is one folder of the project, as the manual describes it.
type moduleInfo struct {
	rel      string
	files    []fileInfo
	children []string
	pkgName  string
	pkgDoc   string
	readme   string
	types    []goType
	methods  map[string][]symbol
	funcs    []symbol
	values   []symbol
	exports  []symbol
	uses     map[string]bool
	usedBy   []string
	symbols  int
	sources  strings.Builder
}

func parentRel(rel string) string {
	return strings.TrimPrefix(path.Dir(rel), ".")
}

func depth(rel string) int {
	if rel == "" {
		return 0
	}
	return strings.Count(rel, "/") + 1
}

// room says whether one more symbol fits in the module's note.
func (m *moduleInfo) room() bool {
	if m.symbols >= manualMaxSymbols {
		return false
	}
	m.symbols++
	return true
}

// readCapped reads the start of a file, or nothing when it cannot.
func readCapped(p string) []byte {
	f, err := openFile(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	raw, _ := io.ReadAll(io.LimitReader(f, manualReadBytes))
	return raw
}

// fileDoc is what a file says it is: the heading of a Markdown file, or the first comment of a
// source file before any code.
func fileDoc(ext, text string) string {
	lines := strings.SplitN(text, "\n", manualHeadLines+1)
	for _, line := range lines[:min(len(lines), manualHeadLines)] {
		line = strings.TrimSpace(line)
		if doc, ok := docLine(ext, line); ok {
			return clip(doc, archDocBytes)
		}
		if line != "" && !strings.HasPrefix(line, "#!") && !strings.HasPrefix(line, "//go:") {
			return ""
		}
	}
	return ""
}

// docLine answers the text of a line that documents a file, if it is one.
func docLine(ext, line string) (string, bool) {
	if ext == ".md" {
		rest, ok := strings.CutPrefix(line, "# ")
		return strings.TrimSpace(rest), ok
	}
	for _, mark := range []string{"// ", "# ", "/** ", "/* "} {
		if rest, ok := strings.CutPrefix(line, mark); ok {
			return strings.TrimSpace(strings.TrimSuffix(rest, "*/")), true
		}
	}
	return "", false
}

func nodeText(fset *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, fset, n)
	return b.String()
}

func docText(groups ...*ast.CommentGroup) string {
	for _, g := range groups {
		if g != nil {
			return clip(strings.Join(strings.Fields(g.Text()), " "), manualSymbolDoc)
		}
	}
	return ""
}

func typeKind(fset *token.FileSet, t ast.Expr) string {
	switch t.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return clip(strings.Join(strings.Fields(nodeText(fset, t)), " "), manualTypeBytes)
	}
}

// receiverName is the type a method belongs to, without pointer or type parameters.
func receiverName(fset *token.FileSet, recv *ast.FieldList) string {
	name := strings.TrimPrefix(nodeText(fset, recv.List[0].Type), "*")
	name, _, _ = strings.Cut(name, "[")
	return name
}

func (m *moduleInfo) goFunc(fset *token.FileSet, file string, d *ast.FuncDecl) {
	if !d.Name.IsExported() {
		return
	}
	sig := strings.TrimPrefix(nodeText(fset, d.Type), "func")
	if d.Recv == nil {
		if m.room() {
			m.funcs = append(m.funcs, symbol{"func " + d.Name.Name + sig, docText(d.Doc), file})
		}
		return
	}
	owner := receiverName(fset, d.Recv)
	if ast.IsExported(owner) && m.room() {
		recv := nodeText(fset, d.Recv.List[0].Type)
		meth := symbol{"func (" + recv + ") " + d.Name.Name + sig, docText(d.Doc), file}
		m.methods[owner] = append(m.methods[owner], meth)
	}
}

func (m *moduleInfo) goSpec(fset *token.FileSet, file string, d *ast.GenDecl, spec ast.Spec) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		if s.Name.IsExported() && m.room() {
			sig := "type " + s.Name.Name + " " + typeKind(fset, s.Type)
			m.types = append(m.types, goType{symbol{sig, docText(s.Doc, d.Doc), file}, s.Name.Name})
		}
	case *ast.ValueSpec:
		m.goValues(file, d, s)
	default:
	}
}

// goValues adds the exported constants or variables of one declaration.
func (m *moduleInfo) goValues(file string, d *ast.GenDecl, s *ast.ValueSpec) {
	for _, n := range s.Names {
		if n.IsExported() && m.room() {
			doc := docText(s.Doc, s.Comment, d.Doc)
			m.values = append(m.values, symbol{d.Tok.String() + " " + n.Name, doc, file})
		}
	}
}

// addGo reads a Go file for its package, its doc, the packages of the module it imports and the API
// it exports. A file that does not parse adds nothing.
func (m *moduleInfo) addGo(module, file string, raw []byte) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, file, raw, parser.ParseComments)
	if err != nil {
		return
	}
	m.pkgName = af.Name.Name
	if m.pkgDoc == "" && af.Doc != nil {
		m.pkgDoc = clip(strings.TrimSpace(af.Doc.Text()), manualDocBytes)
	}
	for _, im := range af.Imports {
		if rest, ok := strings.CutPrefix(strings.Trim(im.Path.Value, `"`), module+"/"); ok && module != "" {
			m.uses[rest] = true
		}
	}
	for _, decl := range af.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			m.goFunc(fset, file, d)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				m.goSpec(fset, file, d, spec)
			}
		default:
		}
	}
}

// addScript reads a TypeScript, JavaScript or Python file line by line: what it exports or defines
// at the top level, with the comment right above, and the files of the project it imports.
func (m *moduleInfo) addScript(file, ext, text string) {
	def, comment := jsExport, "// "
	if ext == ".py" {
		def, comment = pyDef, "# "
	}
	var doc commentBlock
	for _, line := range strings.Split(text, "\n") {
		if im := jsImport.FindStringSubmatch(line); im != nil && ext != ".py" {
			m.uses[path.Clean(path.Join(m.rel, im[1]))] = true
		}
		if sm := def.FindStringSubmatch(line); sm != nil && m.room() {
			m.exports = append(m.exports, symbol{sm[1] + " " + sm[2], clip(doc.text, manualSymbolDoc), file})
		}
		doc.read(strings.TrimSpace(line), comment)
	}
}

// commentBlock is the comment right above a line: consecutive comment lines join, a blank line or
// a comment fence keeps it and anything else forgets it.
type commentBlock struct {
	text string
	open bool
}

func (c *commentBlock) read(line, mark string) {
	rest, ok := strings.CutPrefix(line, mark)
	if !ok {
		rest, ok = strings.CutPrefix(line, "* ")
	}
	switch {
	case ok && c.open:
		c.text += " " + rest
	case ok:
		c.text, c.open = rest, true
	case line == "" || line == "/**" || line == "*/":
		c.open = false
	default:
		c.text, c.open = "", false
	}
}

// addFile reads one file of the module.
func (m *moduleInfo) addFile(module, p string) {
	raw := readCapped(p)
	name := filepath.Base(p)
	ext := strings.ToLower(filepath.Ext(name))
	text := string(raw)
	lang := archLanguages[ext]
	lines := strings.Count(text, "\n")
	m.files = append(m.files, fileInfo{name: name, lang: lang, doc: fileDoc(ext, text), lines: lines})
	if strings.EqualFold(name, "README.md") {
		m.readme = clip(text, manualDocBytes*4)
	}
	if lang != "" && m.sources.Len() < manualSourcesBytes {
		m.sources.WriteString("\n#### " + name + "\n" + clip(text, manualSourceBytes) + "\n")
	}
	switch {
	case ext == ".go" && !strings.HasSuffix(name, "_test.go"):
		m.addGo(module, name, raw)
	case jsExtensions[ext] || ext == ".py":
		m.addScript(name, ext, text)
	default:
	}
}

// buildManual reads every folder of the project into a module, the shallowest first, at most
// manualMaxModules of them.
func buildManual(f projectFacts) []*moduleInfo {
	mods := map[string]*moduleInfo{}
	var get func(rel string) *moduleInfo
	get = func(rel string) *moduleInfo {
		if m, ok := mods[rel]; ok {
			return m
		}
		m := &moduleInfo{rel: rel, uses: map[string]bool{}, methods: map[string][]symbol{}}
		mods[rel] = m
		if rel != "" {
			parent := get(parentRel(rel))
			parent.children = append(parent.children, rel)
		}
		return m
	}
	get("")
	for rel, files := range f.all {
		m := get(rel)
		for _, p := range files {
			m.addFile(f.module, p)
		}
	}
	linkModules(mods)
	out := make([]*moduleInfo, 0, len(mods))
	for _, m := range mods {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if depth(out[i].rel) != depth(out[j].rel) {
			return depth(out[i].rel) < depth(out[j].rel)
		}
		return out[i].rel < out[j].rel
	})
	return out[:min(len(out), manualMaxModules)]
}

// linkModules turns what each module uses into the modules it points at, and fills "used by".
func linkModules(mods map[string]*moduleInfo) {
	for _, m := range mods {
		resolved := map[string]bool{}
		for u := range m.uses {
			to, ok := mods[u]
			if !ok {
				to, ok = mods[parentRel(u)]
			}
			if ok && to != m {
				resolved[to.rel] = true
			}
		}
		m.uses = resolved
	}
	for _, m := range mods {
		for u := range m.uses {
			mods[u].usedBy = append(mods[u].usedBy, m.rel)
		}
	}
}

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
		return aliasLink(ch.name, c.title+" manual")
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

func (m *moduleInfo) fileList() string {
	if len(m.files) == 0 {
		return ""
	}
	files := m.files
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
	return "\n## For developers\nThe names this folder offers to the rest of the code, with what their comments say.\n" + text
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

// byline opens a chapter: which manual it belongs to and where the folder sits.
func (c manualCtx) byline(m *moduleInfo) string {
	if m.rel == "" {
		return fmt.Sprintf("*The guide to %s, written by motita from its code. Each chapter explains one "+
			"folder: what it is for, what is inside and how it connects to the rest. See also the project "+
			"note %s and the architecture overview %s.*\n", cell(c.title), c.project, c.arch)
	}
	return fmt.Sprintf("*A chapter of the %s. Folder `%s/`, inside %s.*\n",
		aliasLink(c.start, c.title+" manual"), cell(m.rel), c.link(parentRel(m.rel)))
}

// startBody is the first note of the manual: what the project is and how it is organised.
func (c manualCtx) startBody(ch *chapter) string {
	var b strings.Builder
	b.WriteString(c.byline(ch.m))
	b.WriteString("\n## What this project is\n" + ch.purpose())
	if len(ch.m.children) > 0 {
		b.WriteString("\n## How it is organised\nEvery folder of the project has its chapter. " +
			"Start from the top; each line says what the folder is for.\n\n")
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
	b.WriteString(c.byline(ch.m))
	b.WriteString("\n## What it is for\n" + ch.purpose())
	b.WriteString(c.inside(ch.m))
	b.WriteString(c.connections(ch.m))
	b.WriteString(ch.m.reference())
	return b.String()
}

// manualRoot is the folder of a project's manual in the vault.
func manualRoot(projectNote, suffix string) string {
	return strings.TrimSuffix(projectNote, ".md") + suffix
}

// writeManual writes one chapter per folder of the project, with "Start here" for the root, drops
// the chapters of folders that are gone, and answers the link to the start.
func writeManual(
	ctx context.Context, v *notes.Vault, p Project, projectNote string, f projectFacts, mm *archModel, res *noteScan,
) string {
	root := manualRoot(projectNote, manualSuffix)
	c := manualCtx{title: p.Title, project: wikiLink(projectNote), arch: wikiLink(archNoteName(projectNote)),
		start: notes.FileName(root, manualStart), chapters: map[string]*chapter{}}
	taken := map[string]bool{}
	var order []*chapter
	for _, m := range buildManual(f) {
		if m.rel == "" && p.Description != "" {
			m.readme = p.Description
		}
		name := notes.FileName(root, chapterName(m.rel))
		if name == "" || taken[strings.ToLower(name)] {
			res.Skipped++
			continue
		}
		taken[strings.ToLower(name)] = true
		facts := redact.String(m.modelFacts())
		sources := redact.String(m.sources.String())
		ch := &chapter{m: m, name: name, fp: archFingerprint(facts, sources)}
		ch.explanation = mm.of(ctx, v, name, ch.fp, facts+"\n### Source excerpts\n"+sources)
		c.chapters[m.rel] = ch
		order = append(order, ch)
	}
	for _, ch := range order {
		title := c.title + " manual"
		if ch.m.rel != "" {
			title = humanName(ch.m.rel)
		}
		outcome, err := v.Upsert(ch.name, title, c.chapterBody(ch))
		res.count(outcome, err)
		if err == nil {
			res.Documented++
		}
	}
	pruneManual(v, root, taken)
	pruneManual(v, manualRoot(projectNote, manualOldSuffix), taken)
	return aliasLink(c.start, c.title+" manual")
}

// pruneManual deletes the notes of a manual's folder that this scan did not write and that hold
// nothing but what an earlier scan wrote: folders that are gone, or a manual laid out the old way.
func pruneManual(v *notes.Vault, root string, written map[string]bool) {
	for _, n := range v.List() {
		if !strings.HasPrefix(n.Path, root+"/") || written[strings.ToLower(n.Path)] {
			continue
		}
		if body, _, err := v.Read(n.Path); err == nil && onlyScanned(body) {
			_ = v.Delete(n.Path)
		}
	}
}

// onlyScanned says whether a note holds nothing the person wrote: its heading and the scan block.
func onlyScanned(body string) bool {
	i := strings.Index(body, notes.BlockStart)
	j := strings.Index(body, notes.BlockEnd)
	if i < 0 || j < i {
		return false
	}
	rest := strings.TrimSpace(body[:i] + body[j+len(notes.BlockEnd):])
	return rest == "" || (strings.HasPrefix(rest, "# ") && !strings.Contains(rest, "\n"))
}

// keptExplanation is the model's explanation, marked so a later scan keeps it.
func keptExplanation(text, fp string) string {
	return fmt.Sprintf("<!-- motita:arch:%s -->\n%s\n<!-- motita:arch:end -->\n", fp, text)
}

// explanationSection is the model's explanation under a heading of its own.
func explanationSection(title, text, fp string) string {
	if text == "" {
		return ""
	}
	return "\n### " + title + "\n" + keptExplanation(text, fp)
}
