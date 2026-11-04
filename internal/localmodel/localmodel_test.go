package localmodel

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProbe plays a platform: commands answer from outs, files from files.
func fakeProbe(goos, goarch string, outs map[string]string, files map[string]string) probe {
	return probe{
		goos: goos, goarch: goarch,
		run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if out, ok := outs[name]; ok {
				return []byte(out), nil
			}
			return nil, errors.New("not found")
		},
		readFile: func(p string) ([]byte, error) {
			if v, ok := files[filepath.ToSlash(p)]; ok {
				return []byte(v), nil
			}
			return nil, os.ErrNotExist
		},
		glob: func(string) ([]string, error) {
			seen := map[string]bool{}
			var dirs []string
			for p := range files {
				d := filepath.Dir(p)
				if strings.HasPrefix(p, "/sys/class/drm/") && !seen[d] {
					seen[d] = true
					dirs = append(dirs, d)
				}
			}
			return dirs, nil
		},
	}
}

func TestDetectNvidiaPicksTheLargestCard(t *testing.T) {
	p := fakeProbe("linux", "amd64", map[string]string{
		"nvidia-smi": "NVIDIA GeForce GTX 1650, 4096, 3900\nNVIDIA RTX 3060, 12288, 11000\nbroken line\n",
	}, nil)
	g := p.detect(context.Background())
	if g == nil || g.Name != "NVIDIA RTX 3060" || g.TotalMB != 12288 || g.FreeMB != 11000 || g.Vendor != "nvidia" {
		t.Fatalf("gpu = %+v", g)
	}
}

func TestDetectAMDOnLinuxReadsSysfs(t *testing.T) {
	p := fakeProbe("linux", "amd64", nil, map[string]string{
		"/sys/class/drm/card0/device/mem_info_vram_total": strconv.Itoa(8 << 30),
		"/sys/class/drm/card0/device/mem_info_vram_used":  strconv.Itoa(1 << 30),
		"/sys/class/drm/card0/device/product_name":        "Radeon RX 6600\n",
	})
	g := p.detect(context.Background())
	if g == nil || g.Vendor != "amd" || g.TotalMB != 8192 || g.FreeMB != 7168 || g.Name != "Radeon RX 6600" {
		t.Fatalf("gpu = %+v", g)
	}
}

func TestDetectWindowsFallsBackToTheRegistry(t *testing.T) {
	p := fakeProbe("windows", "amd64", map[string]string{
		"powershell": "Intel(R) UHD Graphics|\r\nAMD Radeon RX 580|8589934592\r\n",
	}, nil)
	g := p.detect(context.Background())
	if g == nil || g.Vendor != "amd" || g.TotalMB != 8192 || g.Source != "registry" {
		t.Fatalf("gpu = %+v", g)
	}
}

func TestDetectAppleSiliconReportsMetalsShare(t *testing.T) {
	p := fakeProbe("darwin", "arm64", map[string]string{"sysctl": strconv.Itoa(16<<30) + "\n"}, nil)
	g := p.detect(context.Background())
	if g == nil || !g.Unified || g.TotalMB != 16384*2/3 {
		t.Fatalf("gpu = %+v", g)
	}
	if got := metalShareMB(64 << 30); got != 65536*3/4 {
		t.Errorf("64 GB share = %d", got)
	}
}

func TestDetectIntelMacReadsSystemProfiler(t *testing.T) {
	p := fakeProbe("darwin", "amd64", map[string]string{
		"system_profiler": `{"SPDisplaysDataType":[{"sppci_model":"Intel UHD 630","spdisplays_vram_shared":"1536 MB"},` +
			`{"sppci_model":"AMD Radeon Pro 5500M","spdisplays_vram":"8 GB"}]}`,
	}, nil)
	g := p.detect(context.Background())
	if g == nil || g.TotalMB != 8192 || g.Vendor != "amd" {
		t.Fatalf("gpu = %+v", g)
	}
}

func TestDetectFindsNothingWithoutTools(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin", "plan9"} {
		if g := fakeProbe(goos, "arm64", nil, nil).detect(context.Background()); g != nil {
			t.Errorf("%s: gpu = %+v", goos, g)
		}
	}
}

func TestEligibilityNeedsFourGigabytes(t *testing.T) {
	if ok, why := eligibility(nil); ok || why != ReasonNoGPU {
		t.Error("no GPU is not eligible")
	}
	if ok, why := eligibility(&GPU{TotalMB: 2048}); ok || why != ReasonLowVRAM {
		t.Error("2 GB is not eligible")
	}
	if ok, _ := eligibility(&GPU{TotalMB: 4096}); !ok {
		t.Error("4 GB is eligible")
	}
}

func TestPlanFitsTheContextOnTheCard(t *testing.T) {
	ctx, layers := plan(GPU{TotalMB: 4096, FreeMB: 3900})
	if ctx != 8192 || layers < 25 || layers >= 99 {
		t.Errorf("4 GB: ctx %d layers %d", ctx, layers)
	}
	if ctx, layers := plan(GPU{TotalMB: 12288, FreeMB: 11000}); ctx != 32768 || layers != 99 {
		t.Errorf("12 GB: ctx %d layers %d", ctx, layers)
	}
	if ctx, layers := plan(GPU{TotalMB: 8192}); ctx != 16384 || layers != 99 {
		t.Errorf("8 GB, free unknown: ctx %d layers %d", ctx, layers)
	}
	if _, layers := plan(GPU{TotalMB: 10922, Unified: true}); layers != 99 {
		t.Errorf("unified: layers %d", layers)
	}
	if _, layers := plan(GPU{TotalMB: 4096, FreeMB: 300}); layers != 0 {
		t.Errorf("a full card: layers %d", layers)
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// rangeServer serves body with Range support and counts the requests.
func rangeServer(t *testing.T, body []byte, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDownloadResumesAndVerifies(t *testing.T) {
	body := bytes.Repeat([]byte("motita"), 5000)
	var hits atomic.Int32
	srv := rangeServer(t, body, &hits)
	dest := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(dest+".part", body[:1000], 0o600); err != nil {
		t.Fatal(err)
	}
	var last int64
	d := download{client: http.DefaultClient, url: srv.URL, dest: dest, size: int64(len(body)), sha256: sum(body)}
	if err := d.run(context.Background(), func(done, _ int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, body) || last != int64(len(body)) {
		t.Fatalf("file %d bytes, progress %d", len(got), last)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error("the part is renamed into place")
	}
}

func TestDownloadDeletesAMismatch(t *testing.T) {
	body := []byte("not the published file")
	var hits atomic.Int32
	srv := rangeServer(t, body, &hits)
	dest := filepath.Join(t.TempDir(), "m.gguf")
	d := download{client: http.DefaultClient, url: srv.URL, dest: dest, size: int64(len(body)), sha256: sum([]byte("other"))}
	if err := d.run(context.Background(), nil); !errors.Is(err, errDigest) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error("a mismatched part is deleted")
	}
	if err := (download{client: http.DefaultClient, url: srv.URL, dest: dest}).run(context.Background(), nil); err == nil {
		t.Error("no hash, no download")
	}
}

func TestDownloadRestartsWhenTheServerIgnoresRange(t *testing.T) {
	body := []byte("whole body every time")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(dest+".part", []byte("whole"), 0o600)
	if err := (download{client: http.DefaultClient, url: srv.URL, dest: dest, size: int64(len(body)), sha256: sum(body)}).run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, body) {
		t.Fatalf("got %q", got)
	}
}

func TestDownloadReportsAFailedServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	err := (download{client: http.DefaultClient, url: srv.URL, dest: filepath.Join(t.TempDir(), "f"), sha256: sum(nil)}).run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

func TestPickAssetPrefersTheGPUBuild(t *testing.T) {
	r := release{Tag: "b9000", Assets: []asset{
		{Name: "cudart-llama-bin-win-cuda-12.4-x64.zip"},
		{Name: "llama-b9000-bin-ubuntu-x64.tar.gz"},
		{Name: "llama-b9000-bin-ubuntu-vulkan-x64.tar.gz"},
		{Name: "llama-b9000-bin-win-vulkan-x64.zip"},
		{Name: "llama-b9000-bin-macos-arm64.zip"},
	}}
	cases := map[string]string{
		"linux/amd64":   "llama-b9000-bin-ubuntu-vulkan-x64.tar.gz",
		"windows/amd64": "llama-b9000-bin-win-vulkan-x64.zip",
		"darwin/arm64":  "llama-b9000-bin-macos-arm64.zip",
	}
	for sys, want := range cases {
		goos, goarch, _ := strings.Cut(sys, "/")
		a, err := pickAsset(r, goos, goarch)
		if err != nil || a.Name != want {
			t.Errorf("%s: %q %v", sys, a.Name, err)
		}
	}
	if _, err := pickAsset(r, "freebsd", "amd64"); err == nil {
		t.Error("no build for freebsd")
	}
}

func TestDigestOfNeedsSHA256(t *testing.T) {
	h := strings.Repeat("a", 64)
	if digestOf(asset{Digest: "sha256:" + h}) != h || digestOf(asset{Digest: "md5:x"}) != "" || digestOf(asset{}) != "" {
		t.Error("digest")
	}
}

func TestInsideRefusesEscapes(t *testing.T) {
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../x", ".."} {
		if _, err := inside("/base", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if p, err := inside("/base", "build/bin/llama-server"); err != nil || p != filepath.Join("/base", "build", "bin", "llama-server") {
		t.Errorf("%q %v", p, err)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func tgzOf(t *testing.T, files map[string]string, links map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "build/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	for name, target := range links {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target})
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestUnpackZipAndTar(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	_ = os.WriteFile(zipPath, zipOf(t, map[string]string{"build/bin/llama-server": "bin"}), 0o600)
	if err := unpack(zipPath, filepath.Join(dir, "z")); err != nil {
		t.Fatal(err)
	}
	if findServer(filepath.Join(dir, "z"), "linux") == "" {
		t.Error("zip: no server found")
	}
	tgz := filepath.Join(dir, "a.tar.gz")
	_ = os.WriteFile(tgz, tgzOf(t, map[string]string{"build/bin/llama-server": "bin", "build/bin/libggml.so.1": "lib"},
		map[string]string{"build/bin/libggml.so": "libggml.so.1", "build/bin/evil": "../../etc/passwd"}), 0o600)
	if err := unpack(tgz, filepath.Join(dir, "t")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "t", "build", "bin", "libggml.so")); err != nil {
		t.Error("a link beside its target is kept")
	}
	if _, err := os.Lstat(filepath.Join(dir, "t", "build", "bin", "evil")); err == nil {
		t.Error("a link out of the folder is skipped")
	}
	bad := filepath.Join(dir, "bad.zip")
	_ = os.WriteFile(bad, zipOf(t, map[string]string{"../escape": "x"}), 0o600)
	if err := unpack(bad, filepath.Join(dir, "b")); err == nil {
		t.Error("zip slip refused")
	}
}

func TestInstallFetchesRuntimeAndModel(t *testing.T) {
	archive := tgzOf(t, map[string]string{"llama-b9000/llama-server": "#!/bin/sh\n"}, nil)
	model := []byte("gguf model bytes")
	mux := http.NewServeMux()
	var srvURL string
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"b9000","assets":[{"name":"llama-b9000-bin-ubuntu-vulkan-x64.tar.gz",`+
			`"browser_download_url":"%s/asset","size":%d,"digest":"sha256:%s"}]}`, srvURL, len(archive), sum(archive))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a", time.Time{}, bytes.NewReader(archive))
	})
	mux.HandleFunc("/model", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "m", time.Time{}, bytes.NewReader(model))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	oldAPI, oldModel, oldLook := releasesAPI, modelURL, lookPath
	releasesAPI, modelURL = srv.URL+"/releases", srv.URL+"/model"
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	defer func() { releasesAPI, modelURL, lookPath = oldAPI, oldModel, oldLook }()

	m := New(t.TempDir(), nil)
	m.probe = fakeProbe("linux", "amd64", map[string]string{"nvidia-smi": "GPU, 8192, 8000"}, nil)
	if err := m.installRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := m.Status(context.Background())
	if !st.RuntimePresent || st.RuntimeSource != "installed" || st.RuntimeTag != "b9000" || !st.Eligible {
		t.Fatalf("status = %+v", st)
	}
	// The model's own size and hash are pinned: a file of other bytes is refused.
	if err := m.installModel(context.Background()); err == nil {
		t.Fatal("a model that is not the published one is refused")
	}
}

func TestInstallReportsItsError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	old, oldLook := releasesAPI, lookPath
	releasesAPI = srv.URL
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	defer func() { releasesAPI, lookPath = old, oldLook }()
	m := New(t.TempDir(), nil)
	m.probe = fakeProbe("linux", "amd64", nil, nil)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Status(context.Background()).Task.Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st := m.Status(context.Background()); st.Task.Running || !strings.Contains(st.Task.Error, "404") {
		t.Fatalf("task = %+v", st.Task)
	}
}

func TestStartRefusesWithoutGPUOrFiles(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	defer func() { lookPath = old }()
	m := New(t.TempDir(), nil)
	m.probe = fakeProbe("linux", "amd64", nil, nil)
	if err := m.Start(context.Background()); !errors.Is(err, ErrNotEligible) {
		t.Errorf("no GPU: %v", err)
	}
	m = New(t.TempDir(), nil)
	m.probe = fakeProbe("linux", "amd64", map[string]string{"nvidia-smi": "GPU, 8192, 8000"}, nil)
	if err := m.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "install") {
		t.Errorf("nothing installed: %v", err)
	}
}

func TestServerArgsStayOnLoopback(t *testing.T) {
	args := strings.Join(serverArgs("/m.gguf", 8471, "k", 8192, 99), " ")
	for _, want := range []string{"--host 127.0.0.1", "--port 8471", "--api-key k", "-c 8192", "-ngl 99", "--alias " + ModelID} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q lack %q", args, want)
		}
	}
	env := serverEnv([]string{"A=1", "LD_LIBRARY_PATH=/x"}, "/rt", "linux")
	if env[len(env)-1] != "LD_LIBRARY_PATH=/rt"+string(os.PathListSeparator)+"/x" {
		t.Errorf("env = %v", env)
	}
	if got := serverEnv([]string{"A=1"}, "/rt", "darwin"); len(got) != 1 {
		t.Errorf("darwin env = %v", got)
	}
}

func TestTailKeepsTheEnd(t *testing.T) {
	tl := &tail{}
	_, _ = tl.Write(bytes.Repeat([]byte("x"), tailMax+10))
	if len(tl.String()) != tailMax {
		t.Errorf("tail %d", len(tl.String()))
	}
	if lastLines("a\nb\nc\n", 2) != "b\nc" {
		t.Error("lastLines")
	}
	if !strings.Contains(exitText(errors.New("exit 1"), "oom\n"), "oom") {
		t.Error("exitText")
	}
}
