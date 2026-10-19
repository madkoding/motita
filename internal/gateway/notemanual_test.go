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
	"README.md": "# The project\n",
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

// writtenManual writes the manual of manualTree and answers a reader of its notes.
func writtenManual(t *testing.T) (func(rel string) string, string, noteScan) {
	t.Helper()
	dir := t.TempDir()
	v := notes.Open(dir)
	var res noteScan
	link := writeManual(context.Background(), v, Project{Title: "M"}, "Projects/M.md",
		manualFacts(t, manualTree), nil, &res)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, "Projects", "M manual", filepath.FromSlash(rel)))
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

func TestManualIndexListsTheWholeTree(t *testing.T) {
	read, link, res := writtenManual(t)
	if link != "[[Projects/M manual/_index]]" || res.Documented == 0 || res.Skipped != 1 {
		t.Fatalf("link %q res %+v", link, res)
	}
	index := read("_index.md")
	wantAll(t, "index", index,
		"# M manual", "## M (project root)", "- Architecture: [[Projects/M - Architecture]]",
		"### Contents of the manual",
		"- [[Projects/M manual/lib]] (0 files)\n  - [[Projects/M manual/lib/api]] (3 files)",
		"| `README.md` | 1 |  | The project |", "| `run.sh` | 3 | Shell | Runs it. |")
	if strings.Contains(index, "odd") {
		t.Errorf("a folder with no usable note name is left out:\n%s", index)
	}
}

func TestManualDocumentsTheGoAPI(t *testing.T) {
	read, _, _ := writtenManual(t)
	api := read("lib/api.md")
	wantAll(t, "api note", api,
		"# M lib/api/", "- Inside: [[Projects/M manual/lib]]", "### Go package `api`",
		"> Package api is the public face.",
		"- `type Server struct` (api.go): Server serves.",
		"  - `func (*Server) Start() error` (api.go): Start starts the server.",
		"  - `func (Server) Stop()` (api.go): Stop stops it.",
		"- `type Handler interface` (api.go): Handler handles.", "- `type ID string` (api.go): ID is a name.",
		"  - `func (*Box[T]) Get() T` (api.go): Get gives it.",
		"#### Functions\n- `func New(addr string) (*Server, error)` (api.go): New makes a server.",
		"- `const Max` (api.go): Max is the most.", "- `var Default` (api.go): the default",
		"### Uses\n- [[Projects/M manual/lib/store]]")
	for _, unwanted := range []string{"hidden", "Visible", "helper", "min"} {
		if strings.Contains(api, "`"+unwanted) || strings.Contains(api, " "+unwanted+"(") {
			t.Errorf("unexported %q got in:\n%s", unwanted, api)
		}
	}
	wantAll(t, "store note", read("lib/store.md"), "### Used by\n- [[Projects/M manual/lib/api]]")
}

func TestManualDocumentsScriptsAndDocs(t *testing.T) {
	read, _, _ := writtenManual(t)
	wantAll(t, "web note", read("web.md"),
		"- `function start` (app.ts): The app. It runs.", "- `const y` (app.ts)\n",
		"- `class Main` (app.ts): Big doc.", "### Folders\n- [[Projects/M manual/web/util]]")
	wantAll(t, "python note", read("py.md"), "- `def run` (tool.py): Does it.", "- `class Thing` (tool.py)\n")
	wantAll(t, "guide note", read("docs/guide.md"), "| `intro.md` | 1 |  |  |")
}

func TestManualIsCapped(t *testing.T) {
	defer func(m, f, s int) { manualMaxModules, manualMaxFiles, manualMaxSymbols = m, f, s }(
		manualMaxModules, manualMaxFiles, manualMaxSymbols)
	manualMaxModules, manualMaxFiles, manualMaxSymbols = 3, 1, 1
	f := manualFacts(t, map[string]string{
		"a/b/c/x.go": "package c\n\nfunc A() {}\nfunc B() {}\ntype T struct{}\nfunc (T) M() {}\nvar V = 1\n",
		"a/y.ts":     "export const a = 1;\nexport const b = 2;\n",
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
	b, _ := os.ReadFile(filepath.Join(dir, "Projects", "M manual", "a.md"))
	if strings.Count(string(b), "| `") != 1 || strings.Count(string(b), "- `const") != 1 {
		t.Errorf("files and symbols capped:\n%s", b)
	}
	sub, _ := os.ReadFile(filepath.Join(dir, "Projects", "M manual", "a", "b.md"))
	if !strings.Contains(string(sub), "### Folders\n- `a/b/c/`") {
		t.Errorf("a folder left out is named, not linked:\n%s", sub)
	}
	src := "package x\n\nfunc A() {}\nfunc B() {}\ntype T struct{}\nfunc (T) M() {}\n"
	c := buildManual(manualFacts(t, map[string]string{"x.go": src}))
	if len(c[0].funcs)+len(c[0].types)+len(c[0].methods["T"]) != 1 {
		t.Errorf("symbols capped: %+v", c[0])
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
	svc := &describeService{fakeService: &fakeService{}, reply: "## Purpose\nIt stores."}
	mm := &archModel{svc: svc, left: 1, module: true}
	dir := t.TempDir()
	v := notes.Open(dir)
	var res noteScan
	writeManual(context.Background(), v, Project{Title: "M"}, "Projects/M.md",
		manualFacts(t, map[string]string{"store/s.go": "package s\n// sk-abcdefghijklmnopqrstuvwxyz0123456789\n"}), mm, &res)
	if svc.modules != 1 || mm.done != 1 || svc.calls != 0 {
		t.Fatalf("one module explained within the cap: %d modules, %d done", svc.modules, mm.done)
	}
	if !strings.Contains(svc.askedModule, "### Source excerpts") || strings.Contains(svc.askedModule, "sk-abcdefghij") {
		t.Errorf("the model was asked:\n%s", svc.askedModule)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "Projects", "M manual", "_index.md"))
	if !strings.Contains(string(b), "### What this module does\n<!-- motita:arch:") {
		t.Errorf("index:\n%s", b)
	}
}

func TestScanModelsDoneWithoutModels(t *testing.T) {
	if (scanModels{}).done() != 0 {
		t.Error("no models, nothing done")
	}
}
