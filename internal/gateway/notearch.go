package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/notes"
	"github.com/madkoding/motita/internal/redact"
)

// The scan also reads a project's folder and writes how it fits together: its folders, languages,
// entry points and, for Go, every package with what it imports. With the model on, it adds an
// explanation of the modules, how they connect and the main flows. The facts are read from the
// files, never invented; the explanation is kept until the facts change.
const (
	archMaxFiles        = 4000
	archMaxPackages     = 60
	archMaxEdges        = 80
	archMaxDirs         = 20
	archMaxDeps         = 15
	archDocBytes        = 100
	archModelBytes      = 12000
	archDescribeMax     = 10
	archDescribeTimeout = 90 * time.Second
	archFingerprintLen  = 12
	archNoteSuffix      = " - Architecture"
)

// archSkipDirs are folders that hold what a build made or fetched, not what the project is.
var archSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"__pycache__": true, "coverage": true,
}

var archLanguages = map[string]string{
	".go": "Go", ".ts": "TypeScript", ".tsx": "TypeScript", ".js": "JavaScript", ".mjs": "JavaScript",
	".py": "Python", ".rs": "Rust", ".java": "Java", ".kt": "Kotlin", ".rb": "Ruby", ".c": "C",
	".cpp": "C++", ".cs": "C#", ".php": "PHP", ".swift": "Swift", ".sh": "Shell",
}

var archBuildFiles = map[string]bool{
	"Makefile": true, "Dockerfile": true, "go.mod": true, "package.json": true, "pyproject.toml": true,
	"Cargo.toml": true, "pom.xml": true, "build.gradle": true, "docker-compose.yml": true,
}

var archBlock = regexp.MustCompile(`(?s)<!-- motita:arch:([0-9a-f]+) -->\n(.*?)\n<!-- motita:arch:end -->`)

// describer is what a service offers when it can explain a project's structure with the model.
type describer interface {
	DescribeProject(ctx context.Context, text string) (string, error)
	DescribeModule(ctx context.Context, text string) (string, error)
}

type goPackage struct {
	dir, name, doc string
	imports        []string
}

// projectFacts is what the walk found in a project folder.
type projectFacts struct {
	files   int
	topDirs map[string]int
	langs   map[string]int
	build   []string
	goFiles map[string][]string
	// all is every file read, by folder relative to the project ("" for its root).
	all    map[string][]string
	module string
	pkgs   []goPackage
	node   nodeFacts
}

type nodeFacts struct {
	Name    string            `json:"name"`
	Scripts map[string]string `json:"scripts"`
	Deps    map[string]string `json:"dependencies"`
}

func (f *projectFacts) visit(dir, p string, d fs.DirEntry) error {
	name := d.Name()
	if d.IsDir() {
		if strings.HasPrefix(name, ".") || archSkipDirs[name] {
			return filepath.SkipDir
		}
		return nil
	}
	if f.files >= archMaxFiles {
		return filepath.SkipAll
	}
	if !d.Type().IsRegular() {
		return nil
	}
	f.files++
	rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, dir), string(filepath.Separator)))
	top, _, nested := strings.Cut(rel, "/")
	if nested {
		f.topDirs[top]++
	}
	if !nested && archBuildFiles[name] {
		f.build = append(f.build, name)
	}
	if lang, ok := archLanguages[strings.ToLower(filepath.Ext(name))]; ok {
		f.langs[lang]++
	}
	folder := strings.TrimPrefix(path.Dir(rel), ".")
	f.all[folder] = append(f.all[folder], p)
	if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
		f.goFiles[path.Dir(rel)] = append(f.goFiles[path.Dir(rel)], p)
	}
	return nil
}

// goModule is the module path in a go.mod, or "".
func goModule(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// add reads one file of the package: its name, its doc line and the packages of the same module it
// imports. A file that does not parse is left out.
func (g *goPackage) add(module, file string, seen map[string]bool) {
	af, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return
	}
	g.name = af.Name.Name
	if g.doc == "" && af.Doc != nil {
		g.doc = firstSentence(af.Doc.Text())
	}
	for _, im := range af.Imports {
		rest, ok := strings.CutPrefix(strings.Trim(im.Path.Value, `"`), module+"/")
		if ok && !seen[rest] {
			seen[rest] = true
			g.imports = append(g.imports, rest)
		}
	}
}

// goPackages reads the files of each folder for its package.
func goPackages(module string, byDir map[string][]string) []goPackage {
	var out []goPackage
	for dir, files := range byDir {
		pkg := goPackage{dir: dir}
		seen := map[string]bool{}
		for _, file := range files {
			pkg.add(module, file, seen)
		}
		if pkg.name != "" {
			sort.Strings(pkg.imports)
			out = append(out, pkg)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// firstSentence is the first line of a doc comment, cut short.
func firstSentence(doc string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(doc), "\n")
	return clip(strings.TrimSpace(line), archDocBytes)
}

// readNode reads the package.json at the root of a project, if any.
func readNode(dir string) nodeFacts {
	var n nodeFacts
	if raw, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		_ = json.Unmarshal(raw, &n)
	}
	return n
}

// walkProject reads a project folder: it follows no symlink and skips hidden and build folders.
func walkProject(dir string) projectFacts {
	f := projectFacts{
		topDirs: map[string]int{}, langs: map[string]int{}, goFiles: map[string][]string{},
		all: map[string][]string{},
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		return f.visit(dir, p, d)
	})
	sort.Strings(f.build)
	f.module = goModule(dir)
	if f.module != "" {
		f.pkgs = goPackages(f.module, f.goFiles)
	}
	f.node = readNode(dir)
	return f
}

type countedName struct {
	name string
	n    int
}

// ranked lists a count map, the biggest first and then by name, up to max entries.
func ranked(m map[string]int, max int) []countedName {
	out := make([]countedName, 0, len(m))
	for k, v := range m {
		out = append(out, countedName{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].name < out[j].name
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func joinCounts(list []countedName) string {
	parts := make([]string, len(list))
	for i, c := range list {
		parts[i] = fmt.Sprintf("%s %d", c.name, c.n)
	}
	return strings.Join(parts, ", ")
}

// cell makes a text safe inside a Markdown table cell and a note.
func cell(text string) string {
	return strings.NewReplacer("|", "/", "\n", " ", "[[", "[ [", "<!--", "").Replace(text)
}

func (f projectFacts) overview() string {
	var b strings.Builder
	fmt.Fprintf(&b, "- Files read: %d\n", f.files)
	if len(f.langs) > 0 {
		b.WriteString("- Languages (files): " + joinCounts(ranked(f.langs, archMaxDirs)) + "\n")
	}
	if len(f.build) > 0 {
		b.WriteString("- Build files: `" + strings.Join(f.build, "`, `") + "`\n")
	}
	if f.module != "" {
		b.WriteString("- Go module: `" + cell(f.module) + "`\n")
	}
	if f.node.Name != "" {
		b.WriteString("- Node package: `" + cell(f.node.Name) + "`\n")
	}
	return b.String()
}

func (f projectFacts) layout() string {
	if len(f.topDirs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### Folders\n| Folder | Files |\n|---|---|\n")
	for _, c := range ranked(f.topDirs, archMaxDirs) {
		fmt.Fprintf(&b, "| `%s/` | %d |\n", cell(c.name), c.n)
	}
	return b.String()
}

func (f projectFacts) nodeSection() string {
	if f.node.Name == "" && len(f.node.Scripts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### Node scripts and dependencies\n")
	scripts := make([]string, 0, len(f.node.Scripts))
	for k := range f.node.Scripts {
		scripts = append(scripts, k)
	}
	sort.Strings(scripts)
	if len(scripts) > 0 {
		b.WriteString("- Scripts: `" + cell(strings.Join(scripts, "`, `")) + "`\n")
	}
	deps := make([]string, 0, len(f.node.Deps))
	for k := range f.node.Deps {
		deps = append(deps, k)
	}
	sort.Strings(deps)
	if len(deps) > archMaxDeps {
		deps = deps[:archMaxDeps]
	}
	if len(deps) > 0 {
		b.WriteString("- Main dependencies: `" + cell(strings.Join(deps, "`, `")) + "`\n")
	}
	return b.String()
}

func (f projectFacts) goSection() string {
	pkgs := f.pkgs
	if len(pkgs) == 0 {
		return ""
	}
	var b strings.Builder
	var mains []string
	for _, p := range pkgs {
		if p.name == "main" {
			mains = append(mains, "`"+cell(p.dir)+"`")
		}
	}
	if len(mains) > 0 {
		b.WriteString("\n### Entry points\n- " + strings.Join(mains, ", ") + "\n")
	}
	if len(pkgs) > archMaxPackages {
		pkgs = pkgs[:archMaxPackages]
	}
	b.WriteString("\n### Go packages\n| Package | What it is | Imports |\n|---|---|---|\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", cell(p.dir), cell(p.doc), cell(strings.Join(p.imports, ", ")))
	}
	b.WriteString(importGraph(pkgs))
	return b.String()
}

// importGraph draws which package imports which, as a Mermaid diagram, for the packages listed.
func importGraph(pkgs []goPackage) string {
	ids := map[string]string{}
	for i, p := range pkgs {
		ids[p.dir] = fmt.Sprintf("p%d", i)
	}
	var edges []string
	for _, p := range pkgs {
		for _, im := range p.imports {
			if to, ok := ids[im]; ok && len(edges) < archMaxEdges {
				edges = append(edges, fmt.Sprintf("  %s --> %s", ids[p.dir], to))
			}
		}
	}
	if len(edges) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### How the packages connect\n```mermaid\nflowchart LR\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", ids[p.dir], strings.ReplaceAll(cell(p.dir), `"`, ""))
	}
	b.WriteString(strings.Join(edges, "\n") + "\n```\n")
	return b.String()
}

// facts is the deterministic description of the project, as Markdown.
func (f projectFacts) text() string {
	return f.overview() + f.layout() + f.goSection() + f.nodeSection()
}

// archFingerprint changes when the facts or the README do, so an explanation is asked for again.
func archFingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:archFingerprintLen]
}

// archModel decides which projects, or which modules of the manual, get the model's explanation.
type archModel struct {
	svc    describer
	left   int
	done   int
	module bool
}

// ask is the model call: the explanation of a whole project, or of one module.
func (m *archModel) ask(ctx context.Context, text string) (string, error) {
	if m.module {
		return m.svc.DescribeModule(ctx, clip(text, manualModelBytes))
	}
	return m.svc.DescribeProject(ctx, clip(text, archModelBytes))
}

// scanModels are the model's budgets for one scan: one for projects, one for modules.
type scanModels struct {
	project, module *archModel
}

// done is how many explanations the model wrote in this scan.
func (ms scanModels) done() int {
	if ms.project == nil {
		return 0
	}
	return ms.project.done + ms.module.done
}

// explanationOf finds the explanation a note already holds for these facts.
func explanationOf(v *notes.Vault, name, fp string) (string, bool) {
	body, _, err := v.Read(name)
	if err != nil {
		return "", false
	}
	m := archBlock.FindStringSubmatch(body)
	if m == nil || m[1] != fp {
		return "", false
	}
	return m[2], true
}

// of answers the explanation of a project, or "" when it gets none: the model is off, the cap was
// reached or the model failed.
func (m *archModel) of(ctx context.Context, v *notes.Vault, name, fp, facts string) string {
	if m == nil || m.svc == nil {
		return ""
	}
	if kept, ok := explanationOf(v, name, fp); ok {
		return kept
	}
	if m.left <= 0 {
		return ""
	}
	m.left--
	cctx, cancel := context.WithTimeout(ctx, archDescribeTimeout)
	defer cancel()
	out, err := m.ask(cctx, redact.String(facts))
	out = strings.TrimSpace(redact.String(out))
	if err != nil || out == "" {
		return ""
	}
	m.done++
	return strings.NewReplacer(notes.BlockStart, "", notes.BlockEnd, "", "[[", "[ [", "<!--", "").Replace(out)
}

func archBody(title, projectLink, manualLink, facts, explanation, fp string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Architecture of %s\n- Project: %s\n", cell(title), projectLink)
	fmt.Fprintf(&b, "- Guide: %s\n%s", manualLink, facts)
	b.WriteString(explanationSection("How it fits together", explanation, fp))
	return b.String()
}

// archNoteName is the vault path of a project's architecture note.
func archNoteName(projectNote string) string {
	return strings.TrimSuffix(projectNote, ".md") + archNoteSuffix + ".md"
}

// scanArchitecture writes the architecture note and the guide of a project and answers their links,
// or nothing when the folder holds nothing to describe.
func scanArchitecture(
	ctx context.Context, v *notes.Vault, p Project, projectNote string, ms scanModels, res *noteScan,
) (archLink, guideLink string) {
	f := walkProject(p.Dir)
	if f.files == 0 {
		return "", ""
	}
	name := archNoteName(projectNote)
	readme := readmeExcerpt(p.Dir)
	facts := f.text()
	fp := archFingerprint(facts, readme)
	input := facts
	if readme != "" {
		input += "\n### README\n" + readme
	}
	manual := writeManual(ctx, v, p, projectNote, f, ms.module, res)
	body := archBody(p.Title, wikiLink(projectNote), manual, facts, ms.project.of(ctx, v, name, fp, input), fp)
	outcome, err := v.Upsert(name, strings.TrimSuffix(filepath.Base(name), ".md"), body)
	res.count(outcome, err)
	if err != nil {
		return "", manual
	}
	res.Mapped++
	return wikiLink(name), manual
}

// archDescriber finds a service that can explain a project with the model: the gateway's own, or
// one of the conversations'.
func (s *Server) archDescriber() describer {
	if d, ok := s.opts.Service.(describer); ok {
		return d
	}
	for _, ss := range s.scanSessions() {
		if d, ok := ss.svc.(describer); ok {
			return d
		}
	}
	return nil
}
