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

func TestManualNotesDocumentTheModules(t *testing.T) {
	dir := t.TempDir()
	v := notes.Open(dir)
	var res noteScan
	link := writeManual(context.Background(), v, Project{Title: "M"}, "Projects/M.md", "[[Projects/M - Architecture]]",
		manualFacts(t, manualTree), nil, &res)
	if link != "[[Projects/M manual/_index]]" || res.Documented == 0 || res.Skipped != 1 {
		t.Fatalf("link %q res %+v", link, res)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	index := read("Projects/M manual/_index.md")
	for _, want := range []string{
		"# M manual", "## M (project root)", "### Contents of the manual",
		"- [[Projects/M manual/lib]] (0 files)\n  - [[Projects/M manual/lib/api]] (3 files)",
		"| `README.md` | 1 |  | The project |", "| `run.sh` | 3 | Shell | Runs it. |",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index lacks %q:\n%s", want, index)
		}
	}
	if strings.Contains(index, "odd") {
		t.Errorf("a folder with no usable note name is left out:\n%s", index)
	}
	api := read("Projects/M manual/lib/api.md")
	for _, want := range []string{
		"# M lib/api/", "- Inside: [[Projects/M manual/lib]]", "### Go package `api`",
		"> Package api is the public face.",
		"- `type Server struct` (api.go): Server serves.",
		"  - `func (*Server) Start() error` (api.go): Start starts the server.",
		"  - `func (Server) Stop()` (api.go): Stop stops it.",
		"- `type Handler interface` (api.go): Handler handles.", "- `type ID string` (api.go): ID is a name.",
		"  - `func (*Box[T]) Get() T` (api.go): Get gives it.",
		"#### Functions\n- `func New(addr string) (*Server, error)` (api.go): New makes a server.",
		"- `const Max` (api.go): Max is the most.", "- `var Default` (api.go): the default",
		"### Uses\n- [[Projects/M manual/lib/store]]",
	} {
		if !strings.Contains(api, want) {
			t.Errorf("api note lacks %q:\n%s", want, api)
		}
	}
	for _, unwanted := range []string{"hidden", "Visible", "helper", "min"} {
		if strings.Contains(api, "`"+unwanted) || strings.Contains(api, " "+unwanted+"(") {
			t.Errorf("unexported %q got in:\n%s", unwanted, api)
		}
	}
	web := read("Projects/M manual/web.md")
	for _, want := range []string{
		"- `function start` (app.ts): The app. It runs.", "- `const y` (app.ts)\n", "- `class Main` (app.ts): Big doc.",
		"### Folders\n- [[Projects/M manual/web/util]]",
	} {
		if !strings.Contains(web, want) {
			t.Errorf("web note lacks %q:\n%s", want, web)
		}
	}
	py := read("Projects/M manual/py.md")
	if !strings.Contains(py, "- `def run` (tool.py): Does it.") ||
		!strings.Contains(py, "- `class Thing` (tool.py)\n") {
		t.Errorf("python note:\n%s", py)
	}
	if guide := read("Projects/M manual/docs/guide.md"); !strings.Contains(guide, "| `intro.md` | 1 |  |  |") {
		t.Errorf("guide note:\n%s", guide)
	}
	store := read("Projects/M manual/lib/store.md")
	if !strings.Contains(store, "### Used by\n- [[Projects/M manual/lib/api]]") {
		t.Errorf("store note:\n%s", store)
	}
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
	writeManual(context.Background(), v, Project{Title: "M"}, "Projects/M.md", "[[A]]", f, nil, &res)
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
	writeManual(context.Background(), v, Project{Title: "M"}, "Projects/M.md", "[[A]]",
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
