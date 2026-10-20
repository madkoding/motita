package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/notes"
)

var manualTree = map[string]string{
	"go.mod":    "module example.com/m\n",
	"README.md": "# The project\n\nThe project, in short.\n",
	"run.sh":    "#!/bin/sh\n# Runs it.\necho hi\n",
	"lib/api/api.go": `// Package api is the public face.
//
// It says more here.
package api

import (
	"fmt"
	"example.com/m/lib/store"
)

// Server serves.
type Server struct{}

// Handler handles.
type Handler interface{ Handle() }

// ID is a name.
type ID string

type hidden struct{}

// Start starts the server.
func (s *Server) Start() error { return nil }

// Stop stops it.
func (s Server) Stop() {}

func (h hidden) Visible() {}

// Box holds one thing.
type Box[T any] struct{ v T }

// Get gives it.
func (b *Box[T]) Get() T { return b.v }

// New makes a server.
func New(addr string) (*Server, error) { fmt.Println(store.X); return &Server{}, nil }

func helper() {}

// Limits.
const (
	// Max is the most.
	Max = 3
	min = 1
)

var Default = New // the default
`,
	"lib/api/api_test.go": "package api\n",
	"lib/api/broken.go":   "this is not go",
	"lib/store/store.go":  "package store\n\n// X is a value.\nvar X = 1\n",
	"web/app.ts": "import { a } from \"./util/a\";\nimport \"../lib/api/x\";\n\n// The app.\n// It runs.\n\n" +
		"export function start() {}\nconst x = 1;\nexport const y = 2;\n/**\n * Big doc.\n */\nexport class Main {}\n",
	"web/util/a.ts":        "export default function a() {}\n",
	"py/tool.py":           "# A tool.\nimport os\n\n# Does it.\ndef run():\n    pass\n\nclass Thing:\n    pass\n",
	"docs/guide/intro.md":  "Intro without heading\n",
	"docs/guide/notes.txt": "\n\n",
	"[]/odd.go":            "package odd\n",
}

func manualFacts(t *testing.T, files map[string]string) projectFacts {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, files)
	return walkProject(dir)
}

func moduleByRel(mods []*moduleInfo, rel string) *moduleInfo {
	for _, m := range mods {
		if m.rel == rel {
			return m
		}
	}
	return nil
}

func TestBuildManualReadsEveryFolder(t *testing.T) {
	mods := buildManual(manualFacts(t, manualTree))
	if mods[0].rel != "" || moduleByRel(mods, "docs") == nil || moduleByRel(mods, "docs/guide") == nil {
		t.Fatalf("root first, and folders with only subfolders too: %v", mods)
	}
	api := moduleByRel(mods, "lib/api")
	if api.pkgName != "api" || !strings.Contains(api.pkgDoc, "It says more here.") {
		t.Errorf("package %q doc %q", api.pkgName, api.pkgDoc)
	}
	if !api.uses["lib/store"] || len(moduleByRel(mods, "lib/store").usedBy) != 1 {
		t.Errorf("uses %v", api.uses)
	}
	web := moduleByRel(mods, "web")
	if !web.uses["web/util"] || !web.uses["lib/api"] {
		t.Errorf("web uses %v", web.uses)
	}
	if moduleByRel(mods, "").uses["web"] {
		t.Error("a module never uses itself")
	}
}

// manualDir is where the manual of project M is written in a vault at dir.
func manualDir(dir string) string { return filepath.Join(dir, "Projects", "M - Manual") }

// writtenManual writes the manual of manualTree and answers a reader of its chapters.
func writtenManual(t *testing.T) (func(name string) string, string, noteScan) {
	t.Helper()
	dir := t.TempDir()
	v := notes.Open(dir)
	var res noteScan
	link := writeManual(context.Background(), v, Project{Title: "M"}, "Projects/M.md",
		manualFacts(t, manualTree), nil, &res)
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(manualDir(dir), name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return read, link, res
}

// wantAll fails for each text the note lacks.
func wantAll(t *testing.T, name, note string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(note, want) {
			t.Errorf("%s lacks %q:\n%s", name, want, note)
		}
	}
}

func TestManualStartsWithAGuideToTheProject(t *testing.T) {
	read, link, res := writtenManual(t)
	if link != "[[Projects/M - Manual/Start here|M guide]]" || res.Documented == 0 || res.Skipped != 1 {
		t.Fatalf("link %q res %+v", link, res)
	}
	start := read("Start here")
	wantAll(t, "start", start,
		"# M guide", "*A guide to M, written by motita from its files.",
		"the technical overview [[Projects/M - Architecture]]",
		"## What this project is\nThe project, in short.\n",
		"## Contents\n",
		"- [[Projects/M - Manual/lib|Lib]]: It holds 2 folders.\n"+
			"  - [[Projects/M - Manual/lib › api|Api]]: Package api is the public face.\n",
		"## Files at the top\n", "- **README.md** (3 lines): The project", "- **run.sh** (Shell, 3 lines): Runs it.")
	if strings.Contains(start, "odd") {
		t.Errorf("a folder with no usable note name is left out:\n%s", start)
	}
}

func TestManualChaptersExplainEachFolder(t *testing.T) {
	read, _, _ := writtenManual(t)
	api := read("lib › api")
	wantAll(t, "api chapter", api,
		"# Api", "*[[Projects/M - Manual/Start here|M guide]] › [[Projects/M - Manual/lib|Lib]] › **Api***",
		"## What it is for\nPackage api is the public face.\n\nIt says more here.\n",
		"## What is inside\nIt holds 3 files, mostly Go.\n", "- and 1 test file checking them.\n",
		"## How it connects\nIt relies on [[Projects/M - Manual/lib › store|Store]].",
		"## For developers\n", "### Go package `api`",
		"- `type Server struct` in api.go: Server serves.",
		"  - `func (*Server) Start() error` in api.go: Start starts the server.",
		"  - `func (Server) Stop()` in api.go: Stop stops it.",
		"- `type Handler interface` in api.go: Handler handles.", "- `type ID string` in api.go: ID is a name.",
		"  - `func (*Box[T]) Get() T` in api.go: Get gives it.",
		"#### Functions\n- `func New(addr string) (*Server, error)` in api.go: New makes a server.",
		"- `const Max` in api.go: Max is the most.", "- `var Default` in api.go: the default")
	for _, unwanted := range []string{"hidden", "Visible", "helper", "min"} {
		if strings.Contains(api, "`"+unwanted) || strings.Contains(api, " "+unwanted+"(") {
			t.Errorf("unexported %q got in:\n%s", unwanted, api)
		}
	}
	wantAll(t, "store chapter", read("lib › store"),
		"It is used by [[Projects/M - Manual/lib › api|Api]].")
}

func TestManualDocumentsScriptsAndDocs(t *testing.T) {
	read, _, _ := writtenManual(t)
	wantAll(t, "web chapter", read("web"),
		"### Exports\n- `class Main` in app.ts: Big doc.\n",
		"- `function start` in app.ts: The app. It runs.",
		"- `const y` in app.ts\n",
		"- `class Main` in app.ts: Big doc.", "### Folders\n- [[Projects/M - Manual/web › util|Util]]: ")
	wantAll(t, "python chapter", read("py"), "- `def run` in tool.py: Does it.", "- `class Thing` in tool.py\n")
	wantAll(t, "guide chapter", read("docs › guide"), "- **intro.md** (1 line)\n", manualHint)
}

func TestManualIsCapped(t *testing.T) {
	defer func(m, f, s int) { manualMaxModules, manualMaxFiles, manualMaxSymbols = m, f, s }(
		manualMaxModules, manualMaxFiles, manualMaxSymbols)
	manualMaxModules, manualMaxFiles, manualMaxSymbols = 3, 1, 1
	f := manualFacts(t, map[string]string{
		"a/b/c/x.go": "package c\n\nfunc A() {}\nfunc B() {}\ntype T struct{}\nfunc (T) M() {}\nvar V = 1\n",
		"a/y.ts":     "import \"./b/c/x\";\nexport const a = 1;\nexport const b = 2;\n",
		"a/z.txt":    "z",
	})
	mods := buildManual(f)
	if len(mods) != 3 || moduleByRel(mods, "a/b/c") != nil {
		t.Fatalf("the deepest folders go first: %d", len(mods))
	}
	dir := t.TempDir()
	v := notes.Open(dir)
	var res noteScan
	writeManual(context.Background(), v, Project{Title: "M"}, "Projects/M.md", f, nil, &res)
	b, _ := os.ReadFile(filepath.Join(manualDir(dir), "a.md"))
	if strings.Count(string(b), "- **") != 1 || !strings.Contains(string(b), "- and 1 more files.") ||
		strings.Count(string(b), "- `const") != 1 || !strings.Contains(string(b), "It relies on `a/b/c/`.") {
		t.Errorf("files and symbols capped:\n%s", b)
	}
	sub, _ := os.ReadFile(filepath.Join(manualDir(dir), "a › b.md"))
	if strings.Contains(string(sub), "a/b/c") {
		t.Errorf("a folder left out has no line:\n%s", sub)
	}
	src := "package x\n\nfunc A() {}\nfunc B() {}\ntype T struct{}\nfunc (T) M() {}\n"
	c := buildManual(manualFacts(t, map[string]string{"x.go": src}))
	if len(c[0].funcs)+len(c[0].types)+len(c[0].methods["T"]) != 1 {
		t.Errorf("symbols capped: %+v", c[0])
	}
}

func TestManualRemovesWhatIsNoLongerThere(t *testing.T) {
	dir := t.TempDir()
	v := notes.Open(dir)
	for name, body := range map[string]string{
		"Projects/M manual/_index.md":       "# M manual\n\n" + notes.BlockStart + "\nold\n" + notes.BlockEnd + "\n",
		"Projects/M manual/mine.md":         "# Mine\n\nwritten by hand\n",
		"Projects/M manual/kept.md":         "# Kept\n\n" + notes.BlockStart + "\nold\n" + notes.BlockEnd + "\nmy note\n",
		"Projects/M - Manual/gone.md":       notes.BlockStart + "\nold\n" + notes.BlockEnd,
		"Projects/M - Manual/Start here.md": "# M manual\n",
	} {
		if _, err := v.Write(name, body, true); err != nil {
			t.Fatal(err)
		}
	}
	var res noteScan
	writeManual(context.Background(), v, Project{Title: "M"}, "Projects/M.md",
		manualFacts(t, map[string]string{"A/x.py": "x = 1", "a/y.py": "y = 2"}), nil, &res)
	var left []string
	for _, n := range v.List() {
		left = append(left, n.Path)
	}
	want := "Projects/M - Manual/A.md Projects/M - Manual/Start here.md " +
		"Projects/M manual/kept.md Projects/M manual/mine.md"
	if strings.Join(left, " ") != want || res.Skipped != 1 {
		t.Errorf("left %v, skipped %d", left, res.Skipped)
	}
}

func TestManualWordingHelpers(t *testing.T) {
	if humanName("__") != "__" || humanName("my_tool-kit") != "My tool kit" {
		t.Error("humanName")
	}
	if aliasLink("a.md", "x|[y]") != "[[a|x y]]" {
		t.Error("aliasLink")
	}
	empty := &moduleInfo{}
	if empty.contents() != "An empty folder." || (manualCtx{}).inside(empty) != "" || empty.reference() != "" {
		t.Error("an empty folder")
	}
	tie := &moduleInfo{files: []fileInfo{{lang: "Go"}, {lang: "Bash"}, {}}}
	if tie.mainLanguage() != "Bash" {
		t.Error("a tie goes to the first name")
	}
	for text, want := range map[string]string{
		"# T\n[![b](x)](y)\n```\ncode\n```\n\nFirst one. Second.\nmore\n\nnext": "First one.",
		"- one\n- two": "one two", "<div>\n": "",
	} {
		if got := leadSentence(text); got != want {
			t.Errorf("leadSentence(%q) = %q, want %q", text, got, want)
		}
	}
	ch := &chapter{m: &moduleInfo{readme: "# R\nRead me.\n"}}
	if ch.summary() != "Read me." || !strings.HasPrefix(ch.purpose(), "Read me.") {
		t.Errorf("the README speaks for a folder: %q", ch.purpose())
	}
	if onlyScanned("text") || onlyScanned(notes.BlockEnd+notes.BlockStart) || explanationSection("x", "", "f") != "" {
		t.Error("onlyScanned or explanationSection")
	}
}

func TestManualSurvivesUnreadableFiles(t *testing.T) {
	defer func(o func(string) (*os.File, error)) { openFile = o }(openFile)
	openFile = func(string) (*os.File, error) { return nil, errors.New("denied") }
	mods := buildManual(manualFacts(t, map[string]string{"a.go": "package a\n"}))
	if len(mods[0].files) != 1 || mods[0].files[0].lines != 0 || mods[0].pkgName != "" {
		t.Errorf("an unreadable file is listed, empty: %+v", mods[0].files)
	}
}

func TestFileDocReadsTheFirstComment(t *testing.T) {
	for _, c := range []struct{ ext, text, want string }{
		{".go", "//go:build linux\n\n// Package x does.\npackage x", "Package x does."},
		{".go", "package x\n// late", ""},
		{".md", "# Title\n", "Title"},
		{".md", "text\n# Title", ""},
		{".ts", "/** Doc. */\n", "Doc."},
		{".txt", strings.Repeat("\n", manualHeadLines+2), ""},
	} {
		if got := fileDoc(c.ext, c.text); got != c.want {
			t.Errorf("fileDoc(%q, %q) = %q, want %q", c.ext, c.text, got, c.want)
		}
	}
}

func TestManualModulesAreExplainedWhenAsked(t *testing.T) {
	svc := &describeService{fakeService: &fakeService{}, reply: "It stores things. More words."}
	mm := &archModel{svc: svc, left: 1, module: true}
	dir := t.TempDir()
	v := notes.Open(dir)
	var res noteScan
	writeManual(context.Background(), v, Project{Title: "M", Description: "About M."}, "Projects/M.md",
		manualFacts(t, map[string]string{"store/s.go": "package s\n// sk-abcdefghijklmnopqrstuvwxyz0123456789\n"}), mm, &res)
	if svc.modules != 1 || mm.done != 1 || svc.calls != 0 {
		t.Fatalf("one module explained within the cap: %d modules, %d done", svc.modules, mm.done)
	}
	if !strings.Contains(svc.askedModule, "### Source excerpts") || strings.Contains(svc.askedModule, "sk-abcdefghij") {
		t.Errorf("the model was asked:\n%s", svc.askedModule)
	}
	b, _ := os.ReadFile(filepath.Join(manualDir(dir), "Start here.md"))
	if !strings.Contains(string(b), "## What this project is\n<!-- motita:arch:") ||
		!strings.Contains(string(b), "|Store]]: It holds 1 file, mostly Go.") {
		t.Errorf("start:\n%s", b)
	}
}

func TestScanModelsDoneWithoutModels(t *testing.T) {
	if (scanModels{}).done() != 0 {
		t.Error("no models, nothing done")
	}
}

func TestExplainBudgetFollowsTheChoice(t *testing.T) {
	for raw, want := range map[string]int{
		"": manualExplainMax, "x": manualExplainMax, "0": manualExplainMax, "-3": manualExplainMax,
		"100": 100, "999999": manualExplainLimit,
	} {
		if got := explainBudget(raw); got != want {
			t.Errorf("explainBudget(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestScanExplainsAsManyFoldersAsChosen(t *testing.T) {
	svc := &describeService{fakeService: &fakeService{}, reply: "x"}
	srv, _ := archServer(t, svc)
	if got := scanWith(t, srv, "?summarize=1&modules=2"); svc.modules != 2 || got.Explained != 3 {
		t.Errorf("two folders and the project: %+v, %d modules", got, svc.modules)
	}
}

func TestManualLeavesOutFoldersThatHaveNothingToRead(t *testing.T) {
	facts := manualFacts(t, map[string]string{
		"app/main.py":             "x = 1",
		"app/pictures/one.png":    "png",
		"app/pictures/deep/a.png": "png",
		"docs/intro.md":           "# Intro",
		"notes/README.md":         "# Notes\nWhat they are.",
		"empty/data.bin":          "bin",
	})
	var rels []string
	for _, m := range buildManual(facts) {
		rels = append(rels, m.rel)
	}
	if got, want := strings.Join(rels, " "), " app docs notes"; got != want {
		t.Errorf("chapters = %q, want %q", got, want)
	}
}
