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

// The manual is the project written down folder by folder: one note per folder, recursively, with
// its files, what each one is, the API it exports, what it uses and what uses it, linked to its
// parent and its subfolders, and an index of the whole tree. Everything comes from the files; with
// the model on, each module also gets an explanation, kept until the module changes.
const (
	manualSuffix       = " manual"
	manualIndex        = "_index"
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

// manualCtx is what every note of one manual needs: where each module's note is, and the links up.
type manualCtx struct {
	title, project, arch string
	names                map[string]string
	mods                 map[string]*moduleInfo
}

func (c manualCtx) link(rel string) string {
	if name, ok := c.names[rel]; ok {
		return wikiLink(name)
	}
	return "`" + cell(rel) + "/`"
}

func (c manualCtx) heading(rel string) string {
	if rel == "" {
		return cell(c.title) + " (project root)"
	}
	return "`" + cell(rel) + "/`"
}

func sortedSymbols(list []symbol) []symbol {
	sort.Slice(list, func(i, j int) bool { return list[i].sig < list[j].sig })
	return list
}

func symbolLine(indent string, s symbol) string {
	line := indent + "- `" + strings.ReplaceAll(s.sig, "`", "'") + "` (" + cell(s.file) + ")"
	if s.doc != "" {
		line += ": " + cell(s.doc)
	}
	return line + "\n"
}

func (m *moduleInfo) filesSection() string {
	if len(m.files) == 0 {
		return ""
	}
	files := m.files
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	var b strings.Builder
	fmt.Fprintf(&b, "\n### Files (%d)\n| File | Lines | Language | What it is |\n|---|---|---|---|\n", len(files))
	for _, fi := range files[:min(len(files), manualMaxFiles)] {
		fmt.Fprintf(&b, "| `%s` | %d | %s | %s |\n", cell(fi.name), fi.lines, fi.lang, cell(fi.doc))
	}
	return b.String()
}

func (m *moduleInfo) goSection() string {
	if m.pkgName == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### Go package `" + cell(m.pkgName) + "`\n")
	if m.pkgDoc != "" {
		b.WriteString(quote(m.pkgDoc, manualDocBytes) + "\n")
	}
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

func (c manualCtx) linkList(title string, rels []string) string {
	if len(rels) == 0 {
		return ""
	}
	sort.Strings(rels)
	var b strings.Builder
	b.WriteString("\n### " + title + "\n")
	for _, rel := range rels {
		b.WriteString("- " + c.link(rel) + "\n")
	}
	return b.String()
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// tree is the index of the manual: every folder under rel, indented by depth.
func (c manualCtx) tree(rel, indent string, b *strings.Builder) {
	m := c.mods[rel]
	children := append([]string{}, m.children...)
	sort.Strings(children)
	for _, child := range children {
		if _, ok := c.mods[child]; !ok {
			continue
		}
		fmt.Fprintf(b, "%s- %s (%d files)\n", indent, c.link(child), len(c.mods[child].files))
		c.tree(child, indent+"  ", b)
	}
}

// moduleFacts is the note of a module, without the model's explanation.
func (c manualCtx) moduleFacts(m *moduleInfo) string {
	var b strings.Builder
	b.WriteString("## " + c.heading(m.rel) + "\n- Project: " + c.project + "\n- Architecture: " + c.arch + "\n")
	if m.rel == "" {
		b.WriteString("\n### Contents of the manual\n")
		c.tree("", "", &b)
	} else {
		b.WriteString("- Inside: " + c.link(parentRel(m.rel)) + "\n")
		b.WriteString(c.linkList("Folders", m.children))
	}
	b.WriteString(m.filesSection())
	b.WriteString(m.goSection())
	b.WriteString(symbolList("Exports", m.exports))
	b.WriteString(c.linkList("Uses", keys(m.uses)))
	b.WriteString(c.linkList("Used by", m.usedBy))
	return b.String()
}

// moduleNoteName is where a module's note goes in the manual's folder.
func moduleNoteName(root, rel string) string {
	if rel == "" {
		return notes.FileName(root, manualIndex)
	}
	return notes.FileName(root, rel)
}

// writeManual writes one note per folder of the project and answers the link to its index.
func writeManual(
	ctx context.Context, v *notes.Vault, p Project, projectNote string, f projectFacts, mm *archModel, res *noteScan,
) string {
	archLink := wikiLink(archNoteName(projectNote))
	mods := buildManual(f)
	root := strings.TrimSuffix(projectNote, ".md") + manualSuffix
	c := manualCtx{title: p.Title, project: wikiLink(projectNote), arch: archLink,
		names: map[string]string{}, mods: map[string]*moduleInfo{}}
	for _, m := range mods {
		if name := moduleNoteName(root, m.rel); name != "" {
			c.names[m.rel] = name
			c.mods[m.rel] = m
		}
	}
	for _, m := range mods {
		name, ok := c.names[m.rel]
		if !ok {
			res.Skipped++
			continue
		}
		facts := redact.String(c.moduleFacts(m))
		sources := redact.String(m.sources.String())
		fp := archFingerprint(facts, sources)
		explanation := mm.of(ctx, v, name, fp, facts+"\n### Source excerpts\n"+sources)
		body := facts + explanationSection("What this module does", explanation, fp)
		outcome, err := v.Upsert(name, c.title+" "+manualTitle(m.rel), body)
		res.count(outcome, err)
		if err == nil {
			res.Documented++
		}
	}
	return c.link("")
}

// manualTitle is the heading of a module's note: its folder, or "manual" for the index.
func manualTitle(rel string) string {
	if rel == "" {
		return "manual"
	}
	return rel + "/"
}

// explanationSection is how the model's explanation sits in a note, marked so a later scan keeps it.
func explanationSection(title, text, fp string) string {
	if text == "" {
		return ""
	}
	return fmt.Sprintf("\n### %s\n<!-- motita:arch:%s -->\n%s\n<!-- motita:arch:end -->\n", title, fp, text)
}
