package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var archTree = map[string]string{
	"go.mod":   "module example.com/app\n\ngo 1.22\n",
	"Makefile": "all:\n",
	"cmd/app/main.go": "package main\n\nimport \"example.com/app/internal/core\"\n\n" +
		"func main() { core.Run() }\n",
	"internal/core/core.go": "// Package core runs the app. More text.\npackage core\n\n" +
		"import (\n\t\"fmt\"\n\t\"example.com/app/internal/store\"\n)\n\nfunc Run() { fmt.Println(store.X) }\n",
	"internal/core/core2.go":     "package core\n\nimport \"example.com/app/internal/store\"\n\nvar _ = store.X\n",
	"internal/core/core_test.go": "package core\n",
	"internal/store/store.go":    "package store\n\nvar X = 1\n",
	"internal/bad/bad.go":        "this is not go",
	"web/package.json":           "{}",
	"package.json":               `{"name":"web-app","scripts":{"build":"x","test":"y"},"dependencies":{"preact":"1"}}`,
	"node_modules/x/index.js":    "ignored",
	".hidden/secret.go":          "package secret",
	"web/src/app.ts":             "export {}",
	"docs/a.md":                  "# a",
}

func TestWalkProjectReadsTheStructure(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, archTree)
	if err := os.Symlink(filepath.Join(dir, "docs"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	f := walkProject(dir)
	if f.module != "example.com/app" || f.node.Name != "web-app" {
		t.Fatalf("module %q node %q", f.module, f.node.Name)
	}
	if f.langs["Go"] != 6 || f.langs["TypeScript"] != 1 || f.topDirs["node_modules"] != 0 || f.topDirs[".hidden"] != 0 {
		t.Errorf("langs %v dirs %v", f.langs, f.topDirs)
	}
	text := f.text()
	for _, want := range []string{
		"Files read:", "Languages (files): Go 6", "`Makefile`", "Go module: `example.com/app`", "Node package: `web-app`",
		"| `internal/` |", "### Entry points\n- `cmd/app`",
		"| `internal/core` | Package core runs the app. More text. | internal/store |",
		"```mermaid", "p0 --> p1", "p1 --> p2", "Scripts: `build`, `test`", "Main dependencies: `preact`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("facts lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "internal/bad") || strings.Contains(text, "secret") {
		t.Errorf("a file that does not parse or a hidden folder got in:\n%s", text)
	}
}

func TestWalkProjectHandlesOddFolders(t *testing.T) {
	if got := walkProject(filepath.Join(t.TempDir(), "missing")); got.files != 0 || got.text() == "" {
		t.Errorf("a missing folder has no files: %+v", got)
	}
	// Plain files only: no module, no packages, no node.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.py": "x", "notes.txt": "y", "package.json": "not json"})
	text := walkProject(dir).text()
	if !strings.Contains(text, "Python 1") || strings.Contains(text, "Go packages") || strings.Contains(text, "Node") {
		t.Errorf("plain folder:\n%s", text)
	}
	// A package.json with only a name still says what it is, without lists.
	writeTree(t, dir, map[string]string{"package.json": `{"name":"n"}`})
	if got := walkProject(dir).nodeSection(); !strings.Contains(got, "### Node") || strings.Contains(got, "Scripts") {
		t.Errorf("node section:\n%s", got)
	}
	// A module whose packages import nothing draws no graph; a go.mod without a module line is no module.
	dir2 := t.TempDir()
	writeTree(t, dir2, map[string]string{"go.mod": "go 1.22\n", "a/a.go": "package a"})
	if walkProject(dir2).module != "" {
		t.Error("go.mod without a module line")
	}
	writeTree(t, dir2, map[string]string{"go.mod": "module m\n"})
	if text := walkProject(dir2).text(); !strings.Contains(text, "| `a` |") || strings.Contains(text, "mermaid") {
		t.Errorf("no imports, no graph:\n%s", text)
	}
}

func TestWalkProjectStopsAtTheFileCap(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for i := 0; i < archMaxFiles+5; i++ {
		files[filepath.Join("d", strings.Repeat("x", 1+i%7), string(rune('a'+i%26))+strings.Repeat("y", i/26)+".txt")] = "x"
	}
	writeTree(t, dir, files)
	if got := walkProject(dir); got.files != archMaxFiles {
		t.Errorf("files = %d, want the cap %d", got.files, archMaxFiles)
	}
}

func TestFactsAreCappedAndSafe(t *testing.T) {
	f := projectFacts{
		topDirs: map[string]int{}, langs: map[string]int{},
		node: nodeFacts{Name: "n", Deps: map[string]string{}},
	}
	for i := 0; i < archMaxDirs+3; i++ {
		f.topDirs[strings.Repeat("d", i+1)+"|[[x]]"] = i
	}
	for i := 0; i < archMaxDeps+3; i++ {
		f.node.Deps[strings.Repeat("p", i+1)] = "1"
	}
	for i := 0; i < archMaxPackages+3; i++ {
		imports := []string{"a", strings.Repeat("a", i+2)}
		f.pkgs = append(f.pkgs, goPackage{dir: strings.Repeat("a", i+1), name: "a", imports: imports})
	}
	text := f.text()
	if strings.Contains(text, "[[") || strings.Count(text, "| `d") != archMaxDirs {
		t.Errorf("folders not capped or not safe:\n%s", text)
	}
	if strings.Count(text, "\n| `a") != archMaxPackages || strings.Count(text, " --> ") > archMaxEdges {
		t.Errorf("packages or edges not capped: %d", strings.Count(text, " --> "))
	}
	if strings.Contains(text, strings.Repeat("p", archMaxDeps+1)) {
		t.Errorf("dependencies not capped:\n%s", text)
	}
}

type describeService struct {
	*fakeService
	reply string
	err   error
	calls int
	asked string
}

func (s *describeService) DescribeProject(_ context.Context, text string) (string, error) {
	s.calls++
	s.asked = text
	return s.reply, s.err
}

func archServer(t *testing.T, svc Service) (*Server, string) {
	t.Helper()
	srv, dir, ws := scanServer(t, nil)
	proj := filepath.Join(ws, "app")
	writeTree(t, proj, archTree)
	writeTree(t, proj, map[string]string{"README.md": "# App\nkey sk-abcdefghijklmnopqrstuvwxyz0123456789"})
	if err := srv.projects.save(Project{ID: "p1aaaaaaaaaa", Title: "App", Dir: proj, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if svc != nil {
		c, _ := srv.lookup(DefaultSession)
		c.svc = svc
	}
	return srv, dir
}

func TestScanMapsAProjectAndLinksIt(t *testing.T) {
	srv, dir := archServer(t, nil)
	got := scanWith(t, srv, "")
	if got.Mapped != 1 || got.Explained != 0 || got.Created != 2 {
		t.Fatalf("scan = %+v", got)
	}
	arch := readNote(t, dir, "Projects/App - Architecture.md")
	for _, want := range []string{"## Architecture of App", "- Project: [[Projects/App]]", "### Go packages"} {
		if !strings.Contains(arch, want) {
			t.Errorf("architecture note lacks %q:\n%s", want, arch)
		}
	}
	if strings.Contains(arch, "How it fits together") {
		t.Errorf("the model is off, so nothing is explained:\n%s", arch)
	}
	project := readNote(t, dir, "Projects/App.md")
	if !strings.Contains(project, "- Architecture: [[Projects/App - Architecture]]") {
		t.Errorf("the project note does not link its architecture:\n%s", project)
	}
	if again := scanWith(t, srv, ""); again.Unchanged != 2 || again.Created != 0 {
		t.Errorf("a second scan changes nothing: %+v", again)
	}
}

func TestScanExplainsAProjectOnlyWhenAskedAndKeepsIt(t *testing.T) {
	svc := &describeService{
		fakeService: &fakeService{},
		reply:       "## Modules\n- core <!-- motita:scan:end --> [[x]] sk-abcdefghijklmnopqrstuvwxyz0123456789",
	}
	srv, dir := archServer(t, svc)
	if got := scanWith(t, srv, ""); got.Explained != 0 || svc.calls != 0 {
		t.Fatalf("the model is off by default: %+v, %d calls", got, svc.calls)
	}
	if got := scanWith(t, srv, "?summarize=1"); got.Explained != 1 || svc.calls != 1 {
		t.Fatalf("explained: %+v, %d calls", got, svc.calls)
	}
	asked := svc.asked
	if !strings.Contains(asked, "### Go packages") || !strings.Contains(asked, "README") ||
		strings.Contains(asked, "sk-abcdefghij") {
		t.Errorf("the model was asked:\n%s", svc.asked)
	}
	body := readNote(t, dir, "Projects/App - Architecture.md")
	for _, want := range []string{"### How it fits together", "<!-- motita:arch:", "- core", "[ [x]]"} {
		if !strings.Contains(body, want) {
			t.Errorf("note lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "sk-abcdefghij") || strings.Count(body, "motita:scan:end") != 1 {
		t.Errorf("a secret or a marker got through:\n%s", body)
	}
	// Kept while nothing changed; asked again when the project did.
	if got := scanWith(t, srv, "?summarize=1"); got.Explained != 0 || svc.calls != 1 {
		t.Errorf("the explanation is kept: %+v, %d calls", got, svc.calls)
	}
	writeTree(t, filepath.Join(filepath.Dir(dir), "app"), map[string]string{"internal/new/n.go": "package n"})
	if got := scanWith(t, srv, "?summarize=1"); got.Explained != 1 || svc.calls != 2 {
		t.Errorf("a changed project is explained again: %+v, %d calls", got, svc.calls)
	}
}

func TestScanExplanationGivesUpQuietly(t *testing.T) {
	svc := &describeService{fakeService: &fakeService{}, err: errors.New("down")}
	srv, dir := archServer(t, svc)
	if got := scanWith(t, srv, "?summarize=1"); got.Explained != 0 || got.Mapped != 1 || svc.calls != 1 {
		t.Errorf("a failing model still maps: %+v", got)
	}
	svc.err, svc.reply = nil, "  "
	scanWith(t, srv, "?summarize=1")
	if body := readNote(t, dir, "Projects/App - Architecture.md"); strings.Contains(body, "How it fits together") {
		t.Errorf("an empty answer adds nothing:\n%s", body)
	}
	// No service can explain: the facts are still written.
	srv2, _ := archServer(t, &fakeService{})
	if got := scanWith(t, srv2, "?summarize=1"); got.Mapped != 1 || got.Explained != 0 {
		t.Errorf("no describer: %+v", got)
	}
}

func TestScanExplanationRespectsTheCap(t *testing.T) {
	m := &archModel{svc: &describeService{fakeService: &fakeService{}, reply: "x"}, left: 0}
	if got := m.of(context.Background(), nil, "n", "fp", "facts"); got != "" {
		t.Errorf("over the cap: %q", got)
	}
	var none *archModel
	if got := none.of(context.Background(), nil, "n", "fp", "facts"); got != "" {
		t.Errorf("no model: %q", got)
	}
}

func TestScanSkipsAnEmptyProjectFolderAndAFailingArchitectureNote(t *testing.T) {
	srv, dir, ws := scanServer(t, []agent.DialogueTurn{})
	empty := Project{ID: "p1aaaaaaaaaa", Title: "Empty", Dir: filepath.Join(ws, "nothing"), Created: time.Now()}
	if err := srv.projects.save(empty); err != nil {
		t.Fatal(err)
	}
	if got := scanWith(t, srv, ""); got.Mapped != 0 || got.Created != 1 {
		t.Errorf("an empty folder has no architecture note: %+v", got)
	}
	// The vault cannot write the note (a folder is in its place): counted as skipped, not mapped.
	proj := filepath.Join(ws, "app")
	writeTree(t, proj, map[string]string{"a.go": "package a"})
	if err := srv.projects.save(Project{ID: "p2bbbbbbbbbb", Title: "Full", Dir: proj, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "Projects", "Full - Architecture.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := scanWith(t, srv, ""); got.Mapped != 0 || got.Skipped != 2 {
		t.Errorf("a refused note: %+v", got)
	}
}

func TestArchDescriberPrefersTheGatewaysOwnService(t *testing.T) {
	srv, _ := archServer(t, nil)
	own := &describeService{fakeService: &fakeService{}}
	srv.opts.Service = own
	if got := srv.archDescriber(); got != describer(own) {
		t.Errorf("describer = %v, want the gateway's service", got)
	}
}
