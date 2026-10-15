package voice

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fileOf is a catalog entry for content served at url.
func fileOf(name, url, member, content string) ModelFile {
	sum := sha256.Sum256([]byte(content))
	return ModelFile{Name: name, URL: url, Member: member, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
}

// tgz is an npm-style archive holding these members, in order.
func tgz(t *testing.T, members ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		if err := tw.WriteHeader(&tar.Header{Name: m[0], Mode: 0o644, Size: int64(len(m[1]))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(m[1]))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// testLibrary serves a runtime archive and one voice, and counts the requests.
func testLibrary(t *testing.T) (Library, *int) {
	t.Helper()
	archive := tgz(t, [2]string{"package/README.md", "skip me"}, [2]string{"package/a.wasm", "AAAA"},
		[2]string{"package/b.data", "BBBBBB"})
	hits := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		switch r.URL.Path {
		case "/rt.tgz":
			_, _ = w.Write(archive)
		case "/v.onnx":
			_, _ = w.Write([]byte("ONNX"))
		case "/broken":
			// Promises more than it sends: the copy fails mid-way.
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("ON"))
		case "/v.json":
			_, _ = w.Write([]byte("{}"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	lib := Library{
		Dir:    t.TempDir(),
		Client: srv.Client(),
		Runtime: []ModelFile{
			fileOf("a.wasm", srv.URL+"/rt.tgz", "package/a.wasm", "AAAA"),
			fileOf("b.data", srv.URL+"/rt.tgz", "package/b.data", "BBBBBB"),
		},
		Models: []Model{{ID: "v1", Name: "V", Lang: "es-MX", Quality: "low", Files: []ModelFile{
			fileOf("model.onnx", srv.URL+"/v.onnx", "", "ONNX"),
			fileOf("model.json", srv.URL+"/v.json", "", "{}"),
		}}},
	}
	return lib, hits
}

// A file a shared set takes from the runtime's archive is kept in the set's own directory, and
// two files compiled into one voice are both kept.
func TestInstallKeepsEachSetInItsDirectory(t *testing.T) {
	lib, _ := testLibrary(t)
	lib.Shared = map[string][]ModelFile{"eng": {fileOf("b.data", lib.Runtime[0].URL, "package/b.data", "BBBBBB")}}
	one := fileOf("one.bin", "", "", "1")
	one.Data = []byte("1")
	two := fileOf("two.bin", "", "", "2")
	two.Data = []byte("2")
	lib.Models = append(lib.Models, Model{ID: "e1", Shared: "eng", Files: []ModelFile{one, two}})
	if err := lib.Install(context.Background(), "e1", nil); err != nil {
		t.Fatal(err)
	}
	if !lib.Installed("e1") || !lib.RuntimeInstalled() {
		t.Fatal("every set should be in its place")
	}
}

// Installing a voice brings the runtime too, from one request per address, and reports progress
// up to the bytes it needed. A second install needs nothing.
func TestInstallKeepsTheCatalogFiles(t *testing.T) {
	lib, hits := testLibrary(t)
	if lib.Installed("v1") || lib.RuntimeInstalled() {
		t.Fatal("nothing should be installed yet")
	}
	var last [2]int64
	if err := lib.Install(context.Background(), "v1", func(d, n int64) { last = [2]int64{d, n} }); err != nil {
		t.Fatal(err)
	}
	if last != [2]int64{16, 16} || *hits != 3 {
		t.Fatalf("progress %v, %d requests", last, *hits)
	}
	if !lib.Installed("v1") || !lib.RuntimeInstalled() {
		t.Fatal("the voice should be installed")
	}
	if err := lib.Verify("v1"); err != nil {
		t.Fatal(err)
	}
	p, ok := lib.File("v1", "model.onnx")
	if b, _ := os.ReadFile(p); !ok || string(b) != "ONNX" {
		t.Fatalf("File = %q %v", p, ok)
	}
	if p, ok := lib.File(RuntimeID, "b.data"); !ok || filepath.Base(p) != "b.data" {
		t.Fatalf("runtime File = %q %v", p, ok)
	}
	if err := lib.Install(context.Background(), "v1", nil); err != nil || *hits != 3 {
		t.Fatalf("a second install: %v, %d requests", err, *hits)
	}
	// Removing the voice keeps the runtime.
	if err := lib.Remove("v1"); err != nil || lib.Installed("v1") || !lib.RuntimeInstalled() {
		t.Fatalf("Remove: %v", err)
	}
}

// Only the catalog's ids and names are answered.
func TestUnknownIdsAndNames(t *testing.T) {
	lib, _ := testLibrary(t)
	if err := lib.Install(context.Background(), "nope", nil); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("Install = %v", err)
	}
	if err := lib.Remove("nope"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("Remove = %v", err)
	}
	if err := lib.Verify("nope"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("Verify = %v", err)
	}
	if lib.Installed("nope") {
		t.Fatal("an unknown voice is not installed")
	}
	for _, c := range [][2]string{{"nope", "model.onnx"}, {"v1", "../../etc/passwd"}, {"v1", "model.onnx"}} {
		if _, ok := lib.File(c[0], c[1]); ok {
			t.Fatalf("File(%q, %q) answered", c[0], c[1])
		}
	}
	if m, _ := lib.Find("v1"); m.Size() != 6 || lib.RuntimeSize() != 10 {
		t.Fatalf("sizes %d %d", m.Size(), lib.RuntimeSize())
	}
}

// A file that is not the catalog's is never kept, and every failure names what went wrong.
func TestInstallRefusesWhatDoesNotMatch(t *testing.T) {
	cases := map[string]struct {
		edit func(*Library)
		want string
	}{
		"hash":   {func(l *Library) { l.Models[0].Files[0].SHA256 = strings.Repeat("0", 64) }, "not the file"},
		"size":   {func(l *Library) { l.Models[0].Files[0].Size = 2 }, "not the file"},
		"status": {func(l *Library) { l.Models[0].Files[0].URL += "x" }, "answered 404"},
		"not tgz": {func(l *Library) {
			l.Runtime[0].URL = l.Models[0].Files[1].URL
			l.Runtime[1].URL = l.Runtime[0].URL
		}, "not a .tgz"},
		"member":      {func(l *Library) { l.Runtime[1].Member = "package/c" }, "ended before 1"},
		"url":         {func(l *Library) { l.Models[0].Files[0].URL = "http://\x7f" }, "could not be built"},
		"member hash": {func(l *Library) { l.Runtime[0].SHA256 = strings.Repeat("0", 64) }, "a.wasm is not the file"},
		"default client": {func(l *Library) {
			l.Client = nil
			l.Models[0].Files[0].URL = "http://127.0.0.1:1/x"
		}, "could not be reached"},
		"reach": {func(l *Library) { l.Models[0].Files[0].URL = "http://127.0.0.1:1/x" }, "could not be reached"},
		"name":  {func(l *Library) { l.Models[0].Files[0].Name = "a/b" }, "could not be written"},
		"mkdir": {func(l *Library) {
			_ = os.WriteFile(filepath.Join(l.Dir, "v1"), nil, 0o600)
		}, "could not be created"},
		"rename": {func(l *Library) {
			_ = os.MkdirAll(filepath.Join(l.Dir, "v1", "model.onnx", "x"), 0o700)
		}, "could not be kept"},
		"short": {func(l *Library) { l.Models[0].Files[0].Size = 9 }, "not the file"},
		"truncate": {func(l *Library) {
			l.Models[0].Files[0].URL = strings.Replace(l.Models[0].Files[0].URL, "v.onnx", "broken", 1)
		}, "could not be downloaded"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			lib, _ := testLibrary(t)
			c.edit(&lib)
			err := lib.Install(context.Background(), "v1", nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if lib.Installed("v1") {
				t.Fatal("nothing that failed may be installed")
			}
		})
	}
}

// Verify notices a file that went missing or changed after it was kept.
func TestVerifyNoticesChanges(t *testing.T) {
	lib, _ := testLibrary(t)
	if err := lib.Install(context.Background(), "v1", nil); err != nil {
		t.Fatal(err)
	}
	onnx := filepath.Join(lib.Dir, "v1", "model.onnx")
	_ = os.WriteFile(onnx, []byte("XXXX"), 0o600)
	if err := lib.Verify("v1"); err == nil || !strings.Contains(err.Error(), "model.onnx does not match") {
		t.Fatalf("Verify = %v", err)
	}
	_ = os.Remove(onnx)
	if err := lib.Verify("v1"); err == nil || !strings.Contains(err.Error(), "model.onnx is missing") {
		t.Fatalf("Verify = %v", err)
	}
	// A directory where a file should be cannot be read.
	_ = os.Mkdir(onnx, 0o700)
	if err := lib.Verify("v1"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Verify = %v", err)
	}
}

// The compiled-in catalog is well formed: unique ids, https addresses, real hashes, a sample for
// every Piper voice, and Supertonic's and Chatterbox's voices on their shared models, their ids
// starting as the page reads.
func TestTheCatalog(t *testing.T) {
	seen := map[string]bool{RuntimeID: true, samplesDir: true}
	check := func(f ModelFile) {
		if !goodCatalogFile(f) {
			t.Errorf("bad file %+v", f)
		}
	}
	for _, f := range PiperRuntime {
		check(f)
	}
	for _, m := range PiperVoices {
		if seen[m.ID] || !goodPiperVoice(m) {
			t.Errorf("bad voice %+v", m)
		}
		seen[m.ID] = true
		for _, f := range append(m.Files, *m.Sample) {
			check(f)
		}
	}
	seen[SupertonicID] = true
	for _, f := range SupertonicModel {
		check(f)
	}
	for _, m := range SupertonicVoices {
		if seen[m.ID] || !goodSupertonicVoice(m) {
			t.Errorf("bad voice %+v", m)
		}
		seen[m.ID] = true
		check(m.Files[0])
	}
	seen[ChatterboxID] = true
	for _, f := range ChatterboxModel {
		check(f)
	}
	for _, m := range ChatterboxVoices {
		if seen[m.ID] || !goodChatterboxVoice(m) {
			t.Errorf("bad voice %+v", m)
		}
		seen[m.ID] = true
	}
	if embeddedSample("nope") != nil {
		t.Error("a voice with no sample compiled in has none")
	}
}

// catalogSum is what a SHA-256 in the catalog looks like.
var catalogSum = regexp.MustCompile(`^[0-9a-f]{64}$`)

// goodCatalogFile says whether a file of the catalog is downloaded over https and named, sized
// and hashed.
func goodCatalogFile(f ModelFile) bool {
	located := strings.HasPrefix(f.URL, "https://") && f.Name != ""
	return located && catalogSum.MatchString(f.SHA256) && f.Size > 0
}

// goodPiperVoice says whether a Piper voice stands on its own with a model, its config, a name
// and a sample.
func goodPiperVoice(m Model) bool {
	engine := m.Engine == EnginePiper && m.Shared == ""
	files := len(m.Files) == 2 && m.Sample != nil
	return engine && files && m.Name != ""
}

// goodSharedVoice says whether a voice rides on the shared models of its engine, its id
// starting with theirs, for every language, with one file of its own and a sample.
func goodSharedVoice(m Model, engine, shared string) bool {
	onShared := m.Engine == engine && m.Shared == shared
	named := strings.HasPrefix(m.ID, shared+"-") && m.Lang == "mul"
	return onShared && named && len(m.Files) == 1 && m.Sample != nil
}

// goodSupertonicVoice says whether a Supertonic voice is on the shared model with its sample
// compiled in.
func goodSupertonicVoice(m Model) bool {
	if !goodSharedVoice(m, EngineSupertonic, SupertonicID) {
		return false
	}
	return int64(len(m.Sample.Data)) == m.Sample.Size
}

// goodChatterboxVoice says whether a Chatterbox voice is on the shared model with its own file
// compiled in and hashed.
func goodChatterboxVoice(m Model) bool {
	if !goodSharedVoice(m, EngineChatterbox, ChatterboxID) {
		return false
	}
	f := m.Files[0]
	return f.Size != 0 && int64(len(f.Data)) == f.Size && catalogSum.MatchString(f.SHA256)
}

// A sample compiled in is written where the others are kept, checked like them, and not
// downloaded.
func TestEmbeddedSample(t *testing.T) {
	lib, hits := testLibrary(t)
	m := SupertonicVoices[0]
	lib.Models = append(lib.Models, m)
	p, err := lib.Sample(context.Background(), m.ID)
	if b, _ := os.ReadFile(p); err != nil || len(b) != int(m.Sample.Size) || *hits != 0 {
		t.Fatalf("Sample = %q %v, %d requests", p, err, *hits)
	}
}

// A voice of a shared model brings that model with the first of them, counts it in what it takes
// while it is missing, and the model goes with the last of them.
func TestSharedFiles(t *testing.T) {
	lib, hits := testLibrary(t)
	lib.Shared = map[string][]ModelFile{"k": {lib.Models[0].Files[0]}}
	for _, id := range []string{"k-1", "k-2"} {
		lib.Models = append(lib.Models, Model{ID: id, Shared: "k", Files: []ModelFile{lib.Models[0].Files[1]}})
	}
	k1, _ := lib.Find("k-1")
	if lib.DownloadSize(k1) != 6 || lib.DownloadSize(lib.Models[0]) != 6 {
		t.Fatalf("sizes %d %d", lib.DownloadSize(k1), lib.DownloadSize(lib.Models[0]))
	}
	if err := lib.Install(context.Background(), "k-1", nil); err != nil || !lib.Installed("k-1") || lib.Installed("k-2") {
		t.Fatalf("Install: %v", err)
	}
	if lib.DownloadSize(k1) != 2 || *hits != 3 {
		t.Fatalf("once the model is here: %d, %d requests", lib.DownloadSize(k1), *hits)
	}
	if p, ok := lib.File("k", "model.onnx"); !ok || filepath.Base(filepath.Dir(p)) != "k" {
		t.Fatalf("shared File = %q %v", p, ok)
	}
	if err := lib.Verify("k-1"); err != nil {
		t.Fatal(err)
	}
	_ = lib.Install(context.Background(), "k-2", nil)
	if err := lib.Remove("k-1"); err != nil || !lib.Installed("k-2") {
		t.Fatalf("another voice still uses the model: %v", err)
	}
	if err := lib.Remove("k-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib.Dir, "k")); !os.IsNotExist(err) {
		t.Fatalf("the model should go with the last voice: %v", err)
	}
	// A directory that cannot be removed says so.
	lib.Dir = "no\x00dir"
	if err := lib.Remove("k-1"); err == nil {
		t.Fatal("Remove should fail")
	}
}

// A sample is downloaded the first time it is asked for, checked, and kept apart from the voice.
func TestSample(t *testing.T) {
	lib, hits := testLibrary(t)
	s := lib.Models[0].Files[0]
	s.Name = "sample.mp3"
	lib.Models[0].Sample = &s
	lib.Models = append(lib.Models, Model{ID: "quiet"})
	for i := 0; i < 2; i++ {
		p, err := lib.Sample(context.Background(), "v1")
		if b, _ := os.ReadFile(p); err != nil || string(b) != "ONNX" || filepath.Base(p) != "v1.mp3" {
			t.Fatalf("Sample = %q %v", p, err)
		}
	}
	if *hits != 1 || lib.Installed("v1") {
		t.Fatalf("%d requests, installed %v", *hits, lib.Installed("v1"))
	}
	if _, err := lib.Sample(context.Background(), "nope"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := lib.Sample(context.Background(), "quiet"); !errors.Is(err, ErrNoSample) {
		t.Fatalf("no sample: %v", err)
	}
	s.SHA256 = strings.Repeat("0", 64)
	s.Name = "other.mp3"
	lib.Models[0].Sample = &s
	_ = os.Remove(filepath.Join(lib.Dir, samplesDir, "v1.mp3"))
	if _, err := lib.Sample(context.Background(), "v1"); err == nil || !strings.Contains(err.Error(), "not the file") {
		t.Fatalf("a sample that does not match: %v", err)
	}
}

// Prune removes the directories and samples of voices the catalog dropped, and keeps the rest.
func TestPrune(t *testing.T) {
	lib, _ := testLibrary(t)
	lib.Shared = map[string][]ModelFile{"k": nil}
	s := ModelFile{Name: "sample.mp3"}
	lib.Models[0].Sample = &s
	for _, d := range []string{RuntimeID, "k", "v1", "kokoro", "kokoro-ef_dora", "accel-kokoro", samplesDir} {
		_ = os.MkdirAll(filepath.Join(lib.Dir, d), 0o700)
	}
	for _, f := range []string{"v1.mp3", "kokoro-ef_dora.mp3"} {
		_ = os.WriteFile(filepath.Join(lib.Dir, samplesDir, f), []byte("x"), 0o600)
	}
	if err := lib.Prune(); err != nil {
		t.Fatal(err)
	}
	var left []string
	_ = filepath.WalkDir(lib.Dir, func(p string, _ fs.DirEntry, _ error) error {
		rel, _ := filepath.Rel(lib.Dir, p)
		left = append(left, filepath.ToSlash(rel))
		return nil
	})
	if want := []string{".", "k", RuntimeID, samplesDir, samplesDir + "/v1.mp3", "v1"}; !slices.Equal(left, want) {
		t.Fatalf("left %v, want %v", left, want)
	}
	// Nothing downloaded yet is nothing to prune; a directory that cannot be read says so.
	lib.Dir = filepath.Join(t.TempDir(), "none")
	if err := lib.Prune(); err != nil {
		t.Fatal(err)
	}
	lib.Dir = filepath.Join(lib.Dir, "..", "file")
	_ = os.WriteFile(lib.Dir, nil, 0o600)
	if err := lib.Prune(); err == nil {
		t.Fatal("Prune should fail on a file")
	}
}
