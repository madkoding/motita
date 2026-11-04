package localmodel

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The edges: every refusal and failure the happy paths in the other files do not reach.

func TestDetectGPUOnThisMachineDoesNotFail(t *testing.T) {
	_ = DetectGPU(context.Background()) // whatever this machine has, it answers
}

func TestParsersSkipWhatTheyCannotRead(t *testing.T) {
	if g := parseNvidiaSMI("GPU, abc, 1\nGPU, 4096, [N/A]\n"); len(g) != 1 || g[0].FreeMB != 0 {
		t.Errorf("nvidia = %+v", g)
	}
	if g := parseRegistry("no bar\nGPU|x\n"); len(g) != 0 {
		t.Errorf("registry = %+v", g)
	}
	for in, want := range map[string]int{"8 GB": 8192, "512 MB": 512, "8GB": 0, "x GB": 0, "8 TB": 0} {
		if got := parseSizeMB(in); got != want {
			t.Errorf("parseSizeMB(%q) = %d", in, got)
		}
	}
	if parseSystemProfiler([]byte("{")) != nil {
		t.Error("bad json")
	}
	if g := parseSystemProfiler([]byte(`{"SPDisplaysDataType":[{"sppci_model":"x"}]}`)); len(g) != 0 {
		t.Errorf("no vram = %+v", g)
	}
	for name, want := range map[string]string{
		"NVIDIA GeForce": "nvidia", "Radeon": "amd", "Intel Arc": "intel", "Apple M2": "apple", "Matrox": "",
	} {
		if got := vendorOf(name); got != want {
			t.Errorf("vendorOf(%q) = %q", name, got)
		}
	}
}

func TestAMDSysfsWithoutUsageOrName(t *testing.T) {
	p := fakeProbe("linux", "amd64", nil, map[string]string{
		"/sys/class/drm/card1/device/mem_info_vram_total": strconv.Itoa(4 << 30),
		"/sys/class/drm/card2/device/mem_info_vram_total": "junk",
	})
	g := p.detect(context.Background())
	if g == nil || g.Name != "AMD GPU" || g.FreeMB != 0 || g.TotalMB != 4096 {
		t.Fatalf("gpu = %+v", g)
	}
}

func TestRehashOfAnUnreadablePart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	if _, err := rehash(filepath.Join(file, "x.part"), sha256.New()); err == nil {
		t.Error("a part under a file")
	}
}

func TestMacProbeFailures(t *testing.T) {
	if g := fakeProbe("darwin", "amd64", nil, nil).detect(context.Background()); g != nil {
		t.Errorf("intel mac without system_profiler: %+v", g)
	}
	for _, outs := range []map[string]string{nil, {"sysctl": "not a number"}} {
		if g := fakeProbe("darwin", "arm64", outs, nil).detect(context.Background()); g != nil {
			t.Errorf("%v: %+v", outs, g)
		}
	}
}

func TestDownloadEdges(t *testing.T) {
	body := []byte("0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	dir := t.TempDir()
	run := func(d download) error {
		d.client = http.DefaultClient
		return d.run(context.Background(), nil)
	}
	// A part longer than the file is thrown away and the file fetched again.
	dest := filepath.Join(dir, "long")
	_ = os.WriteFile(dest+".part", bytes.Repeat([]byte("x"), 50), 0o600)
	if err := run(download{url: srv.URL, dest: dest, size: 10, sha256: sum(body)}); err != nil {
		t.Errorf("long part: %v", err)
	}
	// A part already whole is only checked.
	dest = filepath.Join(dir, "whole")
	_ = os.WriteFile(dest+".part", body, 0o600)
	if err := run(download{url: "http://127.0.0.1:1/", dest: dest, size: 10, sha256: sum(body)}); err != nil {
		t.Errorf("whole part: %v", err)
	}
	// A file shorter than announced says where it stopped.
	dest = filepath.Join(dir, "short")
	if err := run(download{url: srv.URL, dest: dest, size: 20, sha256: sum(body)}); err == nil ||
		!strings.Contains(err.Error(), "stopped at 10 of 20") {
		t.Errorf("short: %v", err)
	}
	// No size known: the answer's length is the total.
	dest = filepath.Join(dir, "unsized")
	if err := run(download{url: srv.URL, dest: dest, sha256: sum(body)}); err != nil {
		t.Errorf("unsized: %v", err)
	}
	// The destination's folder cannot be made.
	blocker := filepath.Join(dir, "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	if err := run(download{url: srv.URL, dest: filepath.Join(blocker, "x", "y"), sha256: sum(body)}); err == nil {
		t.Error("a file in the way of the folder")
	}
	// The part is a folder: it cannot be read back.
	dest = filepath.Join(dir, "dirpart")
	_ = os.MkdirAll(dest+".part", 0o755)
	if err := run(download{url: srv.URL, dest: dest, sha256: sum(body)}); err == nil {
		t.Error("a folder as the part")
	}
	// Nothing listens.
	if err := run(download{url: "http://127.0.0.1:1/", dest: filepath.Join(dir, "down"), sha256: sum(body)}); err == nil {
		t.Error("no server")
	}
	// Not a URL.
	if err := run(download{url: "http://[::1", dest: filepath.Join(dir, "bad"), sha256: sum(body)}); err == nil {
		t.Error("bad url")
	}
}

func TestDownloadFinishedAndAWrongRange(t *testing.T) {
	body := []byte("0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/416":
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		case "/wrong":
			w.Header().Set("Content-Range", "bytes 0-9/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body)
		case "/cut":
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write(body)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	// 416 for a part that is whole by size (a later run checks it).
	part := filepath.Join(dir, "a")
	_ = os.WriteFile(part+".part", body[:5], 0o600)
	d := download{client: http.DefaultClient, url: srv.URL + "/416", dest: part, size: 5, sha256: sum(body[:5])}
	// The part is as long as the file: no request is made at all.
	if err := d.run(context.Background(), nil); err != nil {
		t.Errorf("whole: %v", err)
	}
	_ = os.WriteFile(part+".part", body[:5], 0o600)
	if n, err := d.fetch(context.Background(), part+".part", 5, sha256.New(), nil); err != nil || n != 5 {
		t.Errorf("416: %d %v", n, err)
	}
	// A 206 that starts somewhere else is refused.
	_ = os.WriteFile(part+".part", body[:5], 0o600)
	d = download{client: http.DefaultClient, url: srv.URL + "/wrong", dest: part, size: 10, sha256: sum(body)}
	if err := d.run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "206") {
		t.Errorf("wrong range: %v", err)
	}
	// A body cut short is an error, and what arrived is kept.
	cut := filepath.Join(dir, "cut")
	d = download{client: http.DefaultClient, url: srv.URL + "/cut", dest: cut, size: 100, sha256: sum(body)}
	if err := d.run(context.Background(), nil); err == nil {
		t.Error("cut body")
	}
	if fi, err := os.Stat(cut + ".part"); err != nil || fi.Size() != 10 {
		t.Errorf("kept part: %v", err)
	}
	// The part cannot be written.
	ro := filepath.Join(dir, "ro")
	_ = os.MkdirAll(ro, 0o755)
	d = download{client: http.DefaultClient, url: srv.URL + "/cut", dest: filepath.Join(ro, "f"), sha256: sum(body)}
	_ = os.MkdirAll(filepath.Join(ro, "f.part", "x"), 0o755)
	if _, err := d.fetch(context.Background(), filepath.Join(ro, "f.part"), 0, sha256.New(), nil); err == nil {
		t.Error("a folder as the part")
	}
	if rangeStart(&http.Response{Header: http.Header{"Content-Range": {"junk"}}}) != -1 {
		t.Error("rangeStart")
	}
}

func TestAssetPatternsForEverySystem(t *testing.T) {
	for _, sys := range []string{"linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64", "darwin/arm64", "darwin/amd64"} {
		goos, goarch, _ := strings.Cut(sys, "/")
		if len(assetPatterns(goos, goarch)) == 0 {
			t.Errorf("%s has no build", sys)
		}
	}
	if serverName("windows") != "llama-server.exe" {
		t.Error("windows name")
	}
	if safeName("") || safeName(strings.Repeat("a", 65)) || safeName("b1/../x") || !safeName("b6500") {
		t.Error("safeName")
	}
	if findServer(filepath.Join(t.TempDir(), "missing"), "linux") != "" {
		t.Error("findServer in nothing")
	}
}

// releaseServer answers the release API with body at status.
func releaseServer(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/latest") && !strings.Contains(r.URL.Path, "/tags/") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := releasesAPI
	releasesAPI = srv.URL
	t.Cleanup(func() { releasesAPI = old })
}

func TestFetchReleaseRefusals(t *testing.T) {
	ctx := context.Background()
	releaseServer(t, http.StatusOK, `{"tag_name":"b1"}`)
	if r, err := fetchRelease(ctx, http.DefaultClient, "b1"); err != nil || r.Tag != "b1" {
		t.Errorf("pinned: %+v %v", r, err)
	}
	releaseServer(t, http.StatusOK, `{"tag_name":"../evil"}`)
	if _, err := fetchRelease(ctx, http.DefaultClient, ""); err == nil {
		t.Error("an unsafe tag")
	}
	releaseServer(t, http.StatusOK, `{`)
	if _, err := fetchRelease(ctx, http.DefaultClient, ""); err == nil {
		t.Error("bad json")
	}
	releasesAPI = "http://127.0.0.1:1"
	if _, err := fetchRelease(ctx, http.DefaultClient, ""); err == nil {
		t.Error("no server")
	}
	releasesAPI = "http://[::1"
	if _, err := fetchRelease(ctx, http.DefaultClient, ""); err == nil {
		t.Error("bad url")
	}
}

func TestInstallRuntimeRefusals(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	defer func() { lookPath = old }()
	m := New(t.TempDir(), nil)
	m.probe = fakeProbe("linux", "amd64", nil, nil)
	ctx := context.Background()
	releaseServer(t, http.StatusOK, `{"tag_name":"b1","assets":[]}`)
	if err := m.installRuntime(ctx); err == nil || !strings.Contains(err.Error(), "no build") {
		t.Errorf("no asset: %v", err)
	}
	releaseServer(t, http.StatusOK, `{"tag_name":"b1","assets":[{"name":"llama-b1-bin-ubuntu-x64.zip"}]}`)
	if err := m.installRuntime(ctx); err == nil || !strings.Contains(err.Error(), "no SHA-256") {
		t.Errorf("no digest: %v", err)
	}
}

// assetServer serves a release with one archive.
func assetServer(t *testing.T, name string, archive []byte) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"b1","assets":[{"name":%q,"browser_download_url":"%s/a","size":%d,"digest":"sha256:%s"}]}`,
			name, srv.URL, len(archive), sum(archive))
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a", time.Time{}, bytes.NewReader(archive))
	})
	old := releasesAPI
	releasesAPI = srv.URL
	t.Cleanup(func() { releasesAPI = old })
}

func TestInstallRuntimeArchiveProblems(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	defer func() { lookPath = old }()
	ctx := context.Background()
	newM := func() *Manager {
		m := New(t.TempDir(), nil)
		m.probe = fakeProbe("linux", "amd64", nil, nil)
		return m
	}
	assetServer(t, "llama-b1-bin-ubuntu-x64.zip", []byte("not a zip"))
	if err := newM().installRuntime(ctx); err == nil || !strings.Contains(err.Error(), "unpacking") {
		t.Errorf("bad archive: %v", err)
	}
	assetServer(t, "llama-b1-bin-ubuntu-x64.zip", zipOf(t, map[string]string{"readme.txt": "x"}))
	if err := newM().installRuntime(ctx); err == nil || !strings.Contains(err.Error(), "holds no") {
		t.Errorf("no server: %v", err)
	}
	assetServer(t, "llama-b1-bin-ubuntu-x64.zip", []byte("x"))
	m := newM()
	_ = os.MkdirAll(filepath.Join(m.runtimeDir(), "llama-b1-bin-ubuntu-x64.zip.part", "x"), 0o755)
	if err := m.installRuntime(ctx); err == nil {
		t.Error("a download that cannot be written")
	}
}

func TestServerBinaryIgnoresABrokenCurrent(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	defer func() { lookPath = old }()
	m := New(t.TempDir(), nil)
	m.probe = fakeProbe("linux", "amd64", nil, nil)
	_ = os.MkdirAll(m.runtimeDir(), 0o755)
	for _, current := range []string{"../x", "b404"} {
		_ = os.WriteFile(m.currentFile(), []byte(current), 0o644)
		if p, _, _ := m.serverBinary(); p != "" {
			t.Errorf("%q: %q", current, p)
		}
	}
}

func TestAFullInstallThenCancelAndBusy(t *testing.T) {
	archive := zipOf(t, map[string]string{"bin/llama-server": "x"})
	assetServer(t, "llama-b1-bin-ubuntu-x64.zip", archive)
	model := []byte("small model")
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte("abc"))
			w.(http.Flusher).Flush()
			<-gate
			return
		}
		http.ServeContent(w, r, "m", time.Time{}, bytes.NewReader(model))
	}))
	defer srv.Close()
	defer close(gate)
	oldURL, oldSize, oldSHA, oldLook := modelURL, modelSize, modelSHA, lookPath
	modelURL, modelSize, modelSHA = srv.URL+"/m", int64(len(model)), sum(model)
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	defer func() { modelURL, modelSize, modelSHA, lookPath = oldURL, oldSize, oldSHA, oldLook }()

	m := New(t.TempDir(), nil)
	m.probe = fakeProbe("linux", "amd64", map[string]string{"nvidia-smi": "GPU, 8192, 8000"}, nil)
	waitIdle := func() Status {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if st := m.Status(context.Background()); !st.Task.Running {
				return st
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("the install never ended")
		return Status{}
	}
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	if st := waitIdle(); !st.ModelPresent || !st.RuntimePresent || st.Task.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	// Installed: a second install has nothing to fetch.
	if err := m.installModel(context.Background()); err != nil {
		t.Errorf("present: %v", err)
	}
	// A download that hangs is cancelled, and a second Install meanwhile is refused.
	_ = os.Remove(m.modelPath())
	modelURL = srv.URL + "/slow"
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(); !errors.Is(err, ErrBusy) {
		t.Errorf("second install: %v", err)
	}
	m.Cancel()
	if st := waitIdle(); !strings.Contains(st.Task.Error, "cancelled") {
		t.Errorf("cancel: %+v", st.Task)
	}
}

func TestUnpackRefusals(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, b, 0o600)
		return p
	}
	if err := unpack(write("bad.tar.gz", []byte("not gzip")), filepath.Join(dir, "o1")); err == nil {
		t.Error("not gzip")
	}
	if err := unpack(filepath.Join(dir, "missing.tar.gz"), filepath.Join(dir, "o2")); err == nil {
		t.Error("missing archive")
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("this is not a tar stream at all, just text padded out to look like one....."))
	_ = zw.Close()
	if err := unpack(write("notar.tar.gz", gz.Bytes()), filepath.Join(dir, "o3")); err == nil {
		t.Error("not a tar")
	}
	if err := unpack(write("slip.tar.gz", tgzOf(t, map[string]string{"../x": "x"}, nil)), filepath.Join(dir, "o4")); err == nil {
		t.Error("tar slip")
	}
	// A file where a folder must go, for a folder entry, a file entry and a link.
	blocked := func(build func(tw *tar.Writer)) []byte {
		var buf bytes.Buffer
		g := gzip.NewWriter(&buf)
		tw := tar.NewWriter(g)
		_ = tw.WriteHeader(&tar.Header{Name: "a", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
		_, _ = tw.Write([]byte("a"))
		build(tw)
		_ = tw.Close()
		_ = g.Close()
		return buf.Bytes()
	}
	for i, build := range []func(tw *tar.Writer){
		func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "a/b/", Typeflag: tar.TypeDir, Mode: 0o755})
		},
		func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "a/f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
			_, _ = tw.Write([]byte("f"))
		},
		func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "a/l", Typeflag: tar.TypeSymlink, Linkname: "x"})
		},
		func(tw *tar.Writer) {
			// A link where a folder already is cannot replace it.
			_ = tw.WriteHeader(&tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755})
			_ = tw.WriteHeader(&tar.Header{Name: "d/sub/x", Typeflag: tar.TypeReg, Mode: 0o644})
			_ = tw.WriteHeader(&tar.Header{Name: "d/sub", Typeflag: tar.TypeSymlink, Linkname: "y"})
		},
	} {
		p := write("blocked"+strconv.Itoa(i)+".tar.gz", blocked(build))
		if err := unpack(p, filepath.Join(dir, "b"+strconv.Itoa(i))); err == nil {
			t.Errorf("blocked %d unpacked", i)
		}
	}
	if err := sameFolderLink(filepath.Join(dir, "l"), ""); err != nil {
		t.Error("an empty link is skipped")
	}
}

func TestUnzipRefusals(t *testing.T) {
	dir := t.TempDir()
	// A folder entry, and a link entry that is skipped.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_, _ = zw.Create("d/")
	lh := &zip.FileHeader{Name: "link"}
	lh.SetMode(os.ModeSymlink | 0o777)
	w, _ := zw.CreateHeader(lh)
	_, _ = w.Write([]byte("/etc/passwd"))
	// A plain, not executable file.
	w, _ = zw.Create("d/readme")
	_, _ = w.Write([]byte("x"))
	// An entry in a compression nobody knows cannot be opened.
	raw, _ := zw.CreateRaw(&zip.FileHeader{Name: "odd", Method: 99})
	_, _ = raw.Write([]byte("zz"))
	_ = zw.Close()
	p := filepath.Join(dir, "a.zip")
	_ = os.WriteFile(p, buf.Bytes(), 0o600)
	if err := unpack(p, filepath.Join(dir, "o")); err == nil {
		t.Error("an unknown compression")
	}
	if _, err := os.Lstat(filepath.Join(dir, "o", "link")); err == nil {
		t.Error("links are skipped")
	}
	if fi, err := os.Stat(filepath.Join(dir, "o", "d", "readme")); err != nil || fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("readme: %v", err)
	}
	// Bigger than the budget.
	old := maxUnpacked
	maxUnpacked = 4
	defer func() { maxUnpacked = old }()
	big := filepath.Join(dir, "big.zip")
	_ = os.WriteFile(big, zipOf(t, map[string]string{"f": "0123456789"}), 0o600)
	if err := unpack(big, filepath.Join(dir, "big")); err == nil || !strings.Contains(err.Error(), "more than expected") {
		t.Errorf("bomb: %v", err)
	}
	// A file that cannot be opened for writing (a folder in its place).
	_ = os.MkdirAll(filepath.Join(dir, "w", "f"), 0o755)
	maxUnpacked = old
	if err := unpack(big, filepath.Join(dir, "w")); err == nil {
		t.Error("a folder in place of the file")
	}
}

func TestStartEdges(t *testing.T) {
	// The preferred port is taken: another one is used.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	taken := l.Addr().(*net.TCPAddr).Port
	if p := freePort(taken); p == taken || p == 0 {
		t.Errorf("free port %d", p)
	}
	listen = func(string, string) (net.Listener, error) { return nil, errors.New("no ports") }
	if p := freePort(taken); p != 0 {
		t.Errorf("no port free: %d", p)
	}
	listen = net.Listen
	// A llama-server that cannot be run.
	m := readyManager(t, nil)
	notExe := filepath.Join(t.TempDir(), "llama-server")
	_ = os.WriteFile(notExe, []byte("x"), 0o600)
	lookPath = func(string) (string, error) { return notExe, nil }
	if err := m.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "starting llama-server") {
		t.Errorf("not executable: %v", err)
	}
	// Stop with nothing running does nothing.
	m.Stop()
	if healthy("http://127.0.0.1:1") {
		t.Error("nothing listens on port 1")
	}
}

func TestAServerThatNeverLoadsIsStoppedAndOneThatIgnoresSIGTERMIsKilled(t *testing.T) {
	t.Setenv("MOTITA_FAKE_LLAMA_SLOW", "1")
	oldT, oldP, oldG := healthTimeout, healthPoll, stopGrace
	healthTimeout, healthPoll, stopGrace = 300*time.Millisecond, 20*time.Millisecond, 200*time.Millisecond
	defer func() { healthTimeout, healthPoll, stopGrace = oldT, oldP, oldG }()
	m := readyManager(t, nil)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := m.Status(context.Background())
		if !st.Server.Starting && !st.Server.Running {
			if !strings.Contains(st.Server.Error, "did not load") {
				t.Fatalf("server = %+v", st.Server)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the slow server was never stopped")
}
