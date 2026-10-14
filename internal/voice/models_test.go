package voice

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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

// The compiled-in catalog is well formed: unique ids, https addresses, real hashes.
func TestThePiperCatalog(t *testing.T) {
	hexSum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := map[string]bool{}
	check := func(f ModelFile) {
		if !strings.HasPrefix(f.URL, "https://") || !hexSum.MatchString(f.SHA256) || f.Size <= 0 || f.Name == "" {
			t.Errorf("bad file %+v", f)
		}
	}
	for _, f := range PiperRuntime {
		check(f)
	}
	for _, m := range PiperVoices {
		if seen[m.ID] || m.ID == RuntimeID || len(m.Files) != 2 || m.Name == "" {
			t.Errorf("bad voice %+v", m)
		}
		seen[m.ID] = true
		for _, f := range m.Files {
			check(f)
		}
	}
}
