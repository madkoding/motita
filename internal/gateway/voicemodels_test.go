package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/voice"
)

// modelFile is a catalog entry for content served at url.
func modelFile(name, url, content string) voice.ModelFile {
	sum := sha256.Sum256([]byte(content))
	return voice.ModelFile{Name: name, URL: url, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
}

// voiceLibrary is a catalog of two voices served by a test server. The second one's model is
// held back until release is closed, so a download can be caught in progress.
func voiceLibrary(t *testing.T) (*voice.Library, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	files := map[string]string{"/rt.wasm": "WASM", "/a.onnx": "ONNX", "/a.json": `{"a":1}`, "/b.json": "{}"}
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/b.onnx" {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte("BBBB"))
			return
		}
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(src.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	return &voice.Library{
		Dir:     t.TempDir(),
		Client:  src.Client(),
		Runtime: []voice.ModelFile{modelFile("ort.wasm", src.URL+"/rt.wasm", "WASM")},
		Models: []voice.Model{
			{ID: "a", Name: "A", Lang: "es-MX", Quality: "high", Files: []voice.ModelFile{
				modelFile("model.onnx", src.URL+"/a.onnx", "ONNX"), modelFile("model.json", src.URL+"/a.json", `{"a":1}`)}},
			{ID: "b", Name: "B", Lang: "en-US", Quality: "medium", Files: []voice.ModelFile{
				modelFile("model.onnx", src.URL+"/b.onnx", "BBBB"), modelFile("model.json", src.URL+"/b.json", "{}")}},
		},
	}, release
}

// modelsOf reads GET /v1/voice/models.
func modelsOf(t *testing.T, srv *Server) (out struct {
	Runtime struct {
		Size      int64 `json:"size"`
		Installed bool  `json:"installed"`
	} `json:"runtime"`
	Models   []localModelView `json:"models"`
	Download *downloadView    `json:"download"`
}) {
	t.Helper()
	w := get(t, srv, "/v1/voice/models", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/voice/models: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// waitFor polls until cond holds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A voice is listed, downloaded in the background, served from this origin with its type, and
// removed again.
func TestLocalVoiceLifecycle(t *testing.T) {
	lib, _ := voiceLibrary(t)
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.VoiceModels = lib })
	got := modelsOf(t, srv)
	if len(got.Models) != 2 || got.Models[0].ID != "a" || got.Models[0].Size != 11 || got.Models[0].Installed ||
		got.Runtime.Size != 4 || got.Runtime.Installed || got.Download != nil {
		t.Fatalf("catalog = %+v", got)
	}
	if w := post(t, srv, "/v1/voice/models/a", "", testToken); w.Code != http.StatusAccepted {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	waitFor(t, func() bool { return modelsOf(t, srv).Models[0].Installed })
	if got := modelsOf(t, srv); !got.Runtime.Installed || got.Download != nil {
		t.Fatalf("after the install: %+v", got)
	}
	// Installing it again is a no-op.
	if w := post(t, srv, "/v1/voice/models/a", "", testToken); w.Code != http.StatusOK {
		t.Fatalf("second install: %d", w.Code)
	}
	for _, c := range []struct{ path, ctype, body string }{
		{"/v1/voice/models/a/model.json", "application/json", `{"a":1}`},
		{"/v1/voice/models/a/model.onnx", "application/octet-stream", "ONNX"},
		{"/v1/voice/models/runtime/ort.wasm", "application/wasm", "WASM"},
	} {
		w := get(t, srv, c.path, testToken)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != c.ctype || w.Body.String() != c.body ||
			w.Header().Get("Cache-Control") != "private, no-cache" {
			t.Errorf("%s: %d %q %q", c.path, w.Code, w.Header().Get("Content-Type"), w.Body)
		}
	}
	if w := get(t, srv, "/v1/voice/models/b/model.onnx", testToken); w.Code != http.StatusNotFound {
		t.Errorf("a file not downloaded: %d", w.Code)
	}
	if w := del(t, srv, "/v1/voice/models/a", testToken); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if got := modelsOf(t, srv); got.Models[0].Installed || !got.Runtime.Installed {
		t.Fatalf("after the delete: %+v", got)
	}
}

// One download at a time; the voice downloading cannot be removed; a failed download is
// reported with its reason.
func TestLocalVoiceDownloadConflicts(t *testing.T) {
	lib, release := voiceLibrary(t)
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.VoiceModels = lib })
	if w := post(t, srv, "/v1/voice/models/b", "", testToken); w.Code != http.StatusAccepted {
		t.Fatalf("install: %d", w.Code)
	}
	if d := modelsOf(t, srv).Download; d == nil || d.ID != "b" {
		t.Fatalf("download = %+v", d)
	}
	if w := post(t, srv, "/v1/voice/models/a", "", testToken); w.Code != http.StatusConflict {
		t.Errorf("a second download: %d", w.Code)
	}
	if w := del(t, srv, "/v1/voice/models/b", testToken); w.Code != http.StatusConflict {
		t.Errorf("deleting the voice downloading: %d", w.Code)
	}
	// Another voice can be removed meanwhile.
	if w := del(t, srv, "/v1/voice/models/a", testToken); w.Code != http.StatusNoContent {
		t.Errorf("deleting another voice: %d", w.Code)
	}
	close(release)
	waitFor(t, func() bool { return modelsOf(t, srv).Models[1].Installed })

	// A file that is not the catalog's fails the download, and the reason stays to be read.
	lib.Models[0].Files[0].SHA256 = strings.Repeat("0", 64)
	if w := post(t, srv, "/v1/voice/models/a", "", testToken); w.Code != http.StatusAccepted {
		t.Fatalf("install: %d", w.Code)
	}
	waitFor(t, func() bool {
		d := modelsOf(t, srv).Download
		return d != nil && d.Error != ""
	})
	if d := modelsOf(t, srv).Download; d.ID != "a" || !strings.Contains(d.Error, "model.onnx is not the file") {
		t.Fatalf("download = %+v", d)
	}
}

// Unknown voices are 404, and a removal that fails is 500.
func TestLocalVoiceErrors(t *testing.T) {
	lib, _ := voiceLibrary(t)
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.VoiceModels = lib })
	if w := post(t, srv, "/v1/voice/models/nope", "", testToken); w.Code != http.StatusNotFound {
		t.Errorf("install unknown: %d", w.Code)
	}
	if w := del(t, srv, "/v1/voice/models/nope", testToken); w.Code != http.StatusNotFound {
		t.Errorf("delete unknown: %d", w.Code)
	}
	if w := get(t, srv, "/v1/voice/models/nope/model.onnx", testToken); w.Code != http.StatusNotFound {
		t.Errorf("file of unknown: %d", w.Code)
	}
	// The library's directory under a file: nothing under it can be removed.
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	lib.Dir = filepath.Join(blocker, "voices")
	if w := del(t, srv, "/v1/voice/models/a", testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("a removal that fails: %d", w.Code)
	}
}

// Without a library every endpoint says so, and GET /v1/voice says there are no local voices.
func TestLocalVoicesOff(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for _, w := range []*httptest.ResponseRecorder{
		get(t, srv, "/v1/voice/models", testToken),
		post(t, srv, "/v1/voice/models/a", "", testToken),
		del(t, srv, "/v1/voice/models/a", testToken),
		get(t, srv, "/v1/voice/models/a/model.onnx", testToken),
	} {
		if w.Code != http.StatusNotImplemented {
			t.Errorf("status %d, want 501", w.Code)
		}
	}
	lib, _ := voiceLibrary(t)
	on := newTestServer(t, &fakeService{}, func(o *Options) { o.VoiceModels = lib })
	for s, want := range map[*Server]bool{srv: false, on: true} {
		var v voiceView
		_ = json.Unmarshal(get(t, s, "/v1/voice", testToken).Body.Bytes(), &v)
		if v.Local != want {
			t.Errorf("local = %v, want %v", v.Local, want)
		}
	}
}

// A voice's sample plays before the voice is downloaded; a voice with none, or whose sample does
// not match the catalog, says so.
func TestLocalVoiceSample(t *testing.T) {
	lib, _ := voiceLibrary(t)
	sample := lib.Models[0].Files[0]
	sample.Name = "sample.mp3"
	lib.Models[0].Sample = &sample
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.VoiceModels = lib })
	got := modelsOf(t, srv)
	if !got.Models[0].Sample || got.Models[1].Sample {
		t.Fatalf("catalog = %+v", got.Models)
	}
	w := get(t, srv, "/v1/voice/models/a/sample", testToken)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "audio/mpeg" || w.Body.String() != "ONNX" ||
		modelsOf(t, srv).Models[0].Installed {
		t.Fatalf("sample: %d %q %q", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	if w := get(t, srv, "/v1/voice/models/b/sample", testToken); w.Code != http.StatusNotFound {
		t.Errorf("no sample: %d", w.Code)
	}
	if w := get(t, srv, "/v1/voice/models/nope/sample", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown voice: %d", w.Code)
	}
	sample.Name = "other.mp3"
	sample.SHA256 = strings.Repeat("0", 64)
	lib.Models[1].Sample = &sample
	if w := get(t, srv, "/v1/voice/models/b/sample", testToken); w.Code != http.StatusBadGateway ||
		!strings.Contains(w.Body.String(), "not the file") {
		t.Errorf("a sample that does not match: %d %s", w.Code, w.Body)
	}
}
