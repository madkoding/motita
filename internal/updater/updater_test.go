package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v0.4.0", "v0.5.0", true},
		{"v0.5.0", "v0.5.0", false},
		{"v0.5.0", "v0.4.0", false},
		{"dev", "v0.5.0", true},
		{"v0.5.0", "dev", false},
		{"0.4.0", "0.5.0", true},
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v2.0.0", true},
		{"", "v0.5.0", true},
		{"v0.5.0", "", false},
	}
	for _, tt := range tests {
		got := isNewer(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v0.5.0", "0.5.0"},
		{"0.5.0", "0.5.0"},
		{"v0.5.0-rc1", "0.5.0"},
		{" dev ", "dev"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeVersion(tt.in)
		if got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.5.0", "0.5.0", 0},
		{"0.5.0", "0.4.0", 1},
		{"0.4.0", "0.5.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"1.2.3", "1.2.4", -1},
	}
	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if (tt.want > 0 && got <= 0) || (tt.want < 0 && got >= 0) || (tt.want == 0 && got != 0) {
			t.Errorf("compareSemver(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	u := &Updater{Goos: "linux", Goarch: "amd64"}
	if got := u.assetName(); got != "motita-linux-amd64" {
		t.Errorf("assetName linux/amd64 = %q, want motita-linux-amd64", got)
	}
	u = &Updater{Goos: "windows", Goarch: "386"}
	if got := u.assetName(); got != "motita-windows-386.exe" {
		t.Errorf("assetName windows/386 = %q, want motita-windows-386.exe", got)
	}
	u = &Updater{Goos: "darwin", Goarch: "arm64"}
	if got := u.assetName(); got != "motita-darwin-arm64" {
		t.Errorf("assetName darwin/arm64 = %q, want motita-darwin-arm64", got)
	}
}

// TestCheckWithMockServer verifies that Check parses the GitHub API response
// and correctly reports whether an update is available.
func TestCheckWithMockServer(t *testing.T) {
	release := Release{
		TagName:     "v0.6.0",
		Name:        "Motita v0.6.0",
		PublishedAt: "2026-09-25T12:00:00Z",
		HTMLURL:     "https://github.com/madkoding/motita/releases/tag/v0.6.0",
		Assets: []Asset{
			{Name: "motita-linux-amd64", BrowserDownloadURL: "http://example.com/motita-linux-amd64"},
			{Name: "SHA256SUMS", BrowserDownloadURL: "http://example.com/SHA256SUMS"},
		},
	}
	body, _ := json.Marshal(release)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	u := &Updater{
		CurrentVersion: "v0.5.0",
		Goos:           "linux",
		Goarch:         "amd64",
		HTTPClient:     srv.Client(),
		APIURL:         func() string { return srv.URL },
	}

	result := u.Check(context.Background())
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !result.UpdateAvailable {
		t.Error("expected update available for v0.5.0 -> v0.6.0")
	}
	if result.LatestVersion != "v0.6.0" {
		t.Errorf("latest = %q, want v0.6.0", result.LatestVersion)
	}
}

func TestCheckNoUpdate(t *testing.T) {
	release := Release{TagName: "v0.5.0"}
	body, _ := json.Marshal(release)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	u := &Updater{
		CurrentVersion: "v0.5.0",
		HTTPClient:     srv.Client(),
		APIURL:         func() string { return srv.URL },
	}

	result := u.Check(context.Background())
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.UpdateAvailable {
		t.Error("expected no update for same version")
	}
}

func TestCheckServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	u := &Updater{
		CurrentVersion: "v0.5.0",
		HTTPClient:     srv.Client(),
		APIURL:         func() string { return srv.URL },
	}

	result := u.Check(context.Background())
	if result.Error == "" {
		t.Error("expected an error for a 404 response")
	}
	if result.UpdateAvailable {
		t.Error("expected no update available on error")
	}
}

// TestFindAsset verifies that the correct asset URL is found.
func TestFindAsset(t *testing.T) {
	u := &Updater{Goos: "linux", Goarch: "amd64"}
	release := &Release{
		Assets: []Asset{
			{Name: "motita-linux-386", BrowserDownloadURL: "http://x/386"},
			{Name: "motita-linux-amd64", BrowserDownloadURL: "http://x/amd64"},
			{Name: "SHA256SUMS", BrowserDownloadURL: "http://x/sums"},
		},
	}
	url, err := u.findAsset(release)
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://x/amd64" {
		t.Errorf("got %q, want http://x/amd64", url)
	}
}

func TestFindAssetMissing(t *testing.T) {
	u := &Updater{Goos: "linux", Goarch: "arm64"}
	release := &Release{
		Assets: []Asset{
			{Name: "motita-linux-amd64", BrowserDownloadURL: "http://x/amd64"},
		},
	}
	_, err := u.findAsset(release)
	if err == nil {
		t.Error("expected error for missing asset")
	}
}

// TestVerifyChecksum verifies the checksum flow with a mock server.
func TestVerifyChecksum(t *testing.T) {
	// Create a fake binary file.
	tmpDir := t.TempDir()
	binPath := tmpDir + "/motita-linux-amd64"
	binContent := []byte("fake binary content")
	if err := writeFile(binPath, binContent); err != nil {
		t.Fatal(err)
	}

	// Compute the real hash.
	hash := sha256hex(binContent)
	sums := fmt.Sprintf("%s  motita-linux-amd64\n", hash)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	}))
	defer srv.Close()

	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	if err := u.verifyChecksum(context.Background(), srv.URL, binPath); err != nil {
		t.Fatalf("verifyChecksum failed: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := tmpDir + "/motita-linux-amd64"
	if err := writeFile(binPath, []byte("real content")); err != nil {
		t.Fatal(err)
	}
	// Wrong hash.
	sums := "0000000000000000000000000000000000000000000000000000000000000000  motita-linux-amd64\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	}))
	defer srv.Close()

	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	err := u.verifyChecksum(context.Background(), srv.URL, binPath)
	if err == nil {
		t.Error("expected checksum mismatch error")
	}
}

// helpers

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func sha256hex(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// ---------------------------------------------------------------------------
// Coverage tests for New, LatestRelease, findChecksumsURL, DownloadAndInstall,
// downloadFile, install, and copyFile.
// ---------------------------------------------------------------------------

func TestNew(t *testing.T) {
	u := New("v0.5.0", "/usr/local/bin/motita")
	if u == nil {
		t.Fatal("New returned nil")
	}
	if u.CurrentVersion != "v0.5.0" {
		t.Errorf("CurrentVersion = %q, want v0.5.0", u.CurrentVersion)
	}
	if u.ExePath != "/usr/local/bin/motita" {
		t.Errorf("ExePath = %q", u.ExePath)
	}
	if u.HTTPClient == nil {
		t.Error("HTTPClient is nil")
	}
	if u.HTTPClient.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", u.HTTPClient.Timeout)
	}
	if u.APIURL == nil {
		t.Error("APIURL is nil")
	}
	if u.Goos == "" || u.Goarch == "" {
		t.Error("Goos/Goarch not set from runtime")
	}
}

func TestLatestRelease(t *testing.T) {
	rel := Release{TagName: "v0.7.0", HTMLURL: "http://x"}
	body, _ := json.Marshal(rel)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	u := &Updater{
		HTTPClient: srv.Client(),
		APIURL:     func() string { return srv.URL },
	}
	got, err := u.LatestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v0.7.0" {
		t.Errorf("TagName = %q", got.TagName)
	}
}

func TestLatestReleaseContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	u := &Updater{
		HTTPClient: srv.Client(),
		APIURL:     func() string { return srv.URL },
	}
	_, err := u.LatestRelease(ctx)
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

func TestFetchLatestReleaseBadURL(t *testing.T) {
	u := &Updater{
		HTTPClient: &http.Client{},
		APIURL:     func() string { return "http://\x7f.invalid" },
	}
	_, err := u.fetchLatestRelease(context.Background())
	if err == nil {
		t.Error("expected error from bad URL")
	}
}

func TestFetchLatestReleaseBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()

	u := &Updater{
		HTTPClient: srv.Client(),
		APIURL:     func() string { return srv.URL },
	}
	_, err := u.fetchLatestRelease(context.Background())
	if err == nil {
		t.Error("expected JSON decode error")
	}
}

func TestFindChecksumsURL(t *testing.T) {
	u := &Updater{}
	release := &Release{
		Assets: []Asset{
			{Name: "motita-linux-amd64", BrowserDownloadURL: "http://x/bin"},
			{Name: "SHA256SUMS", BrowserDownloadURL: "http://x/sums"},
		},
	}
	got := u.findChecksumsURL(release)
	if got != "http://x/sums" {
		t.Errorf("got %q, want http://x/sums", got)
	}
}

func TestFindChecksumsURLMissing(t *testing.T) {
	u := &Updater{}
	release := &Release{
		Assets: []Asset{
			{Name: "motita-linux-amd64", BrowserDownloadURL: "http://x/bin"},
		},
	}
	got := u.findChecksumsURL(release)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ---- downloadFile tests ----

func TestDownloadFile(t *testing.T) {
	content := []byte("hello world binary")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "downloaded")
	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, dest, func(ProgressEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(content) {
		t.Errorf("downloaded %q, want %q", got, content)
	}
}

func TestDownloadFileNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, filepath.Join(dir, "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error for 500")
	}
}

func TestDownloadFileBadDest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	u := &Updater{HTTPClient: srv.Client()}
	// dest in a non-existent directory
	err := u.downloadFile(context.Background(), srv.URL, "/nonexistent-dir-xyz/sub/file", func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error for unwritable dest")
	}
}

func TestDownloadFileBadURL(t *testing.T) {
	u := &Updater{HTTPClient: &http.Client{}}
	err := u.downloadFile(context.Background(), "http://\x7f.invalid", filepath.Join(t.TempDir(), "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error from bad URL")
	}
}

func TestDownloadFileContextCanceled(t *testing.T) {
	// Use a server that never responds, then cancel the context.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(ctx, srv.URL, filepath.Join(t.TempDir(), "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

func TestDownloadFileMidStreamCancel(t *testing.T) {
	// Server that streams slowly so the cancel hits mid-read.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		// Write one byte, then stall.
		_, _ = w.Write([]byte{0})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(10 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	dir := t.TempDir()
	u := &Updater{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	err := u.downloadFile(ctx, srv.URL, filepath.Join(dir, "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error from context deadline")
	}
}

func TestDownloadFileReadError(t *testing.T) {
	// A server that returns a body that errors during read: use a handler
	// that sets Content-Length but closes the connection prematurely.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		// hijack and close to cause an early read error
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, filepath.Join(dir, "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected read error")
	}
}

// ---- install tests ----

func TestInstallEmptyExePath(t *testing.T) {
	u := &Updater{ExePath: ""}
	err := u.install("/tmp/somefile")
	if err == nil {
		t.Error("expected error for empty ExePath")
	}
}

func TestInstallSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "motita")
	// Create a placeholder target so the dir exists.
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Create the new binary.
	newBin := filepath.Join(dir, "newmotita")
	if err := os.WriteFile(newBin, []byte("new binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	u := &Updater{ExePath: target}
	if err := u.install(newBin); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new binary" {
		t.Errorf("target content = %q, want 'new binary'", got)
	}
}

// ---- copyFile tests ----

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	content := []byte("copy me")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(content) {
		t.Errorf("copied = %q, want %q", got, content)
	}
	// Check permissions.
	info, _ := os.Stat(dst)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("perm = %v, want 0o755", info.Mode().Perm())
	}
}

func TestCopyFileSrcMissing(t *testing.T) {
	err := copyFile("/nonexistent-src-xyz", filepath.Join(t.TempDir(), "dst"))
	if err == nil {
		t.Error("expected error for missing src")
	}
}

func TestCopyFileBadDst(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := copyFile(src, "/nonexistent-dir-xyz/sub/dst")
	if err == nil {
		t.Error("expected error for unwritable dst")
	}
}

// ---- DownloadAndInstall end-to-end ----

// newUpdateServer creates a test server that serves a binary download and a
// SHA256SUMS file with the correct hash, returning the Release to use.
func newUpdateServer(t *testing.T, goos, goarch string) (*httptest.Server, Release) {
	t.Helper()
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	name := fmt.Sprintf("motita-%s-%s%s", goos, goarch, ext)
	binContent := []byte("binary payload for " + name)
	hash := sha256hex(binContent)
	sums := fmt.Sprintf("%s  %s\n", hash, name)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(binContent)))
		_, _ = w.Write(binContent)
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	})

	release := Release{
		TagName: "v0.7.0",
		Name:    "Motita v0.7.0",
		Assets: []Asset{
			{Name: name, BrowserDownloadURL: srv.URL + "/" + name, Size: int64(len(binContent))},
			{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/SHA256SUMS"},
		},
	}
	return srv, release
}

func TestDownloadAndInstallSuccess(t *testing.T) {
	srv, release := newUpdateServer(t, "linux", "amd64")
	defer srv.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "motita")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{
		Goos:       "linux",
		Goarch:     "amd64",
		ExePath:    target,
		HTTPClient: srv.Client(),
	}

	var events []ProgressEvent
	err := u.DownloadAndInstall(context.Background(), &release, func(e ProgressEvent) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "binary payload") {
		t.Errorf("target = %q, want new binary", got)
	}
	if len(events) == 0 {
		t.Error("expected progress events")
	}
}

func TestDownloadAndInstallNoChecksums(t *testing.T) {
	// Same but without SHA256SUMS asset → skip verification.
	ext := ""
	name := fmt.Sprintf("motita-linux-amd64%s", ext)
	binContent := []byte("no-checksums binary")

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(binContent)))
		_, _ = w.Write(binContent)
	})

	release := Release{
		TagName: "v0.7.0",
		Assets: []Asset{
			{Name: name, BrowserDownloadURL: srv.URL + "/" + name},
		},
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "motita")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{
		Goos:       "linux",
		Goarch:     "amd64",
		ExePath:    target,
		HTTPClient: srv.Client(),
	}

	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "no-checksums binary" {
		t.Errorf("target = %q", got)
	}
}

func TestDownloadAndInstallAssetMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	release := Release{
		TagName: "v0.7.0",
		Assets: []Asset{
			{Name: "motita-windows-amd64.exe", BrowserDownloadURL: "http://x"},
		},
	}

	u := &Updater{
		Goos:       "linux",
		Goarch:     "amd64",
		ExePath:    "/tmp/x",
		HTTPClient: srv.Client(),
	}

	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error for missing asset")
	}
}

func TestDownloadAndInstallBadChecksum(t *testing.T) {
	ext := ""
	name := fmt.Sprintf("motita-linux-amd64%s", ext)
	binContent := []byte("real content")
	// Wrong hash in the sums file.
	sums := "0000000000000000000000000000000000000000000000000000000000000000  motita-linux-amd64\n"

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(binContent)))
		_, _ = w.Write(binContent)
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	})

	release := Release{
		TagName: "v0.7.0",
		Assets: []Asset{
			{Name: name, BrowserDownloadURL: srv.URL + "/" + name},
			{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/SHA256SUMS"},
		},
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "motita")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{
		Goos:       "linux",
		Goarch:     "amd64",
		ExePath:    target,
		HTTPClient: srv.Client(),
	}

	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected checksum mismatch error")
	}
}

func TestDownloadAndInstallDownloadFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "oops", http.StatusInternalServerError)
	}))
	defer srv.Close()

	release := Release{
		TagName: "v0.7.0",
		Assets: []Asset{
			{Name: "motita-linux-amd64", BrowserDownloadURL: srv.URL + "/motita-linux-amd64"},
		},
	}

	u := &Updater{
		Goos:       "linux",
		Goarch:     "amd64",
		ExePath:    filepath.Join(t.TempDir(), "motita"),
		HTTPClient: srv.Client(),
	}

	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected download failure")
	}
}

// ---- verifyChecksum edge cases (to close remaining gaps) ----

func TestVerifyChecksumHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	err := u.verifyChecksum(context.Background(), srv.URL, filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Error("expected error for non-200")
	}
}

func TestVerifyChecksumBadURL(t *testing.T) {
	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: &http.Client{}}
	err := u.verifyChecksum(context.Background(), "http://\x7f.invalid", filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Error("expected error for bad URL")
	}
}

func TestVerifyChecksumNoEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abcd  some-other-file\n"))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "motita-linux-amd64")
	if err := writeFile(binPath, []byte("x")); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	err := u.verifyChecksum(context.Background(), srv.URL, binPath)
	if err == nil {
		t.Error("expected error for no matching entry")
	}
}

func TestVerifyChecksumFileMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abcd  motita-linux-amd64\n"))
	}))
	defer srv.Close()

	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	err := u.verifyChecksum(context.Background(), srv.URL, "/nonexistent-file-xyz")
	if err == nil {
		t.Error("expected error for missing binary file")
	}
}

func TestVerifyChecksumEmptyLines(t *testing.T) {
	// Exercises the empty-line and multi-line parsing branches.
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "motita-linux-amd64")
	binContent := []byte("payload")
	if err := writeFile(binPath, binContent); err != nil {
		t.Fatal(err)
	}
	hash := sha256hex(binContent)
	sums := fmt.Sprintf("\n\n%s  motita-linux-amd64\n\n", hash)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	}))
	defer srv.Close()

	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	if err := u.verifyChecksum(context.Background(), srv.URL, binPath); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyChecksumBodyReadError(t *testing.T) {
	// Server that closes connection mid-body to cause io.ReadAll error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	err := u.verifyChecksum(context.Background(), srv.URL, filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Error("expected body read error")
	}
}

// ---- fetchLatestRelease: non-OK status reads body ----

func TestFetchLatestReleaseNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	u := &Updater{HTTPClient: srv.Client(), APIURL: func() string { return srv.URL }}
	_, err := u.fetchLatestRelease(context.Background())
	if err == nil {
		t.Error("expected error for 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention status 429: %v", err)
	}
}

// ---- downloadFile: progress reporting with Content-Length > 0 ----

func TestDownloadFileProgressReporting(t *testing.T) {
	// Large enough content with known length so the >200ms progress branch fires.
	content := make([]byte, 0) // we'll write via a slow handler
	_ = content
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4")
		// Write byte by byte with a delay to trigger the 200ms progress report.
		for i := 0; i < 4; i++ {
			_, _ = w.Write([]byte{byte('A' + i)})
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(250 * time.Millisecond)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "out")

	var maxPct int
	u := &Updater{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	err := u.downloadFile(context.Background(), srv.URL, dest, func(e ProgressEvent) {
		if e.Percent > maxPct {
			maxPct = e.Percent
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if len(got) != 4 {
		t.Errorf("len = %d, want 4", len(got))
	}
}

// ---- downloadFile: write error (dest becomes unwritable mid-stream) ----

func TestDownloadFileWriteError(t *testing.T) {
	content := []byte("some content here")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	// Create dest as a directory so os.Create fails.
	dir := t.TempDir()
	dest := filepath.Join(dir, "blocker")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, dest, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected error because dest is a directory")
	}
}

// Ensure io is used (prevents unused import if some branches are optimized away).
var _ = io.EOF

// ---- Tests for remaining coverage gaps ----

// TestDownloadAndInstallInstallFails: install fails because the target's
// directory does not exist, exercising the install-error path (line 219-220).
func TestDownloadAndInstallInstallFails(t *testing.T) {
	srv, release := newUpdateServer(t, "linux", "amd64")
	defer srv.Close()

	// Point ExePath to a file in a nonexistent directory so os.Rename fails.
	u := &Updater{
		Goos:       "linux",
		Goarch:     "amd64",
		ExePath:    filepath.Join(t.TempDir(), "nonexistent-subdir", "motita"),
		HTTPClient: srv.Client(),
	}
	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected install to fail")
	}
}

// TestDownloadFileSelectContextCanceled: the context is canceled between
// read iterations, so the select's ctx.Done() case fires (line 258). The
// server sends one chunk then stalls; the first Read succeeds, the context
// is canceled, and the next loop iteration's select catches ctx.Done().
func TestDownloadFileSelectContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		// Send first chunk immediately.
		_, _ = w.Write([]byte("first chunk data"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Stall so the client's second Read blocks — but we cancel
		// the context before that, so the select catches it.
		time.Sleep(10 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after enough time for the first Read to succeed.
	// The first Read returns immediately (data is available), then
	// the loop processes it and goes back to the select. By then
	// the context is canceled, so select catches ctx.Done().
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	dir := t.TempDir()
	u := &Updater{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	err := u.downloadFile(ctx, srv.URL, filepath.Join(dir, "x"), func(ProgressEvent) {})
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

// TestDownloadFileWriteErrorMidStream: the dest file is created but then
// becomes unwritable (we remove it after create, so out.Write fails).
// This is hard to trigger reliably; instead we make dest a read-only file
// inside a read-only directory after os.Create succeeds — but os.Create
// already has the fd. The simplest approach: use a file that is created
// but the underlying FS write fails. On Linux, we can make the dest path
// point to a file on a read-only filesystem, but that's not portable.
//
// Instead, we use a trick: create the dest file as a FIFO (named pipe) —
// os.Create will succeed but Write will fail because no reader is present.
// Actually, os.Create on a FIFO will open it for writing and block.
//
// A more reliable approach: make dest a path where the directory is
// read-only after the file is created. But os.Create already opened the
// fd, so writes go to the open fd, not the directory.
//
// The most reliable way to hit the Write error: use a pipe whose write
// end is closed. We create a file, get the fd, then... we can't control
// it from outside.
//
// Skip this — it's extremely hard to trigger portably.
// Instead, we test the case where the dest is a directory (os.Create fails)
// which we already have in TestDownloadFileWriteError.

// TestVerifyChecksumBadURL2: HTTPClient.Do fails because the URL points to a
// port with no listener (line 297-298). The URL must pass NewRequest but fail
// at connection time.
func TestVerifyChecksumBadURL2(t *testing.T) {
	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: &http.Client{Timeout: 2 * time.Second}}
	err := u.verifyChecksum(context.Background(), "http://127.0.0.1:1/nope", filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Error("expected error from unreachable checksums URL")
	}
}

// TestInstallDirEmpty: filepath.Dir returns "" for a bare filename, so the
// `dir = "."` branch (line 363-364) is taken. We set ExePath to "motita"
// (no directory component) and install into the CWD.
func TestInstallDirEmpty(t *testing.T) {
	dir := t.TempDir()
	// Change to a temp dir so "motita" resolves there.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	// Create a new binary to install from.
	newBin := filepath.Join(dir, "newmotita")
	if err := os.WriteFile(newBin, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create the target with no directory component.
	target := "motita"
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(target) }()

	u := &Updater{ExePath: target}
	if err := u.install(newBin); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("got %q, want 'new'", got)
	}
}

// TestInstallChmodFails: os.Chmod on tempPath fails because it doesn't exist
// (line 379-380). We pass a non-existent tempPath.
func TestInstallChmodFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "motita")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &Updater{ExePath: target}
	err := u.install(filepath.Join(dir, "does-not-exist"))
	if err == nil {
		t.Error("expected error for non-existent tempPath")
	}
}

// TestCopyFileIOCopyFails: io.Copy fails because the destination is
// /dev/full (returns ENOSPC on write), covering line 399-400.
func TestCopyFileIOCopyFails(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("data that will fail to write"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := copyFile(src, "/dev/full")
	if err == nil {
		t.Error("expected io.Copy error writing to /dev/full")
	}
}

// TestVerifyChecksumIOCopyFails: io.Copy in verifyChecksum (line 337-338)
// fails because the binary path is /proc/self/mem which returns I/O error
// on read.
func TestVerifyChecksumIOCopyFails(t *testing.T) {
	if _, err := os.Stat("/proc/self/mem"); err != nil {
		t.Skip("/proc/self/mem not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abcd  motita-linux-amd64\n"))
	}))
	defer srv.Close()

	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	err := u.verifyChecksum(context.Background(), srv.URL, "/proc/self/mem")
	if err == nil {
		t.Error("expected io.Copy error reading /proc/self/mem")
	}
}

// TestDownloadFileWriteErrorMidStream: the out.Write fails because dest is
// /dev/full (returns ENOSPC on write), covering line 264-265.
func TestDownloadFileWriteErrorMidStream(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full not available")
	}
	content := []byte("content to write")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	u := &Updater{HTTPClient: srv.Client()}
	err := u.downloadFile(context.Background(), srv.URL, "/dev/full", func(ProgressEvent) {})
	if err == nil {
		t.Error("expected write error to /dev/full")
	}
}

// TestDownloadAndInstallMkdirTempFails: os.MkdirTemp fails because TMPDIR
// points to a nonexistent directory, covering line 198-199.
func TestDownloadAndInstallMkdirTempFails(t *testing.T) {
	// Create the temp dir and server BEFORE setting TMPDIR, since t.TempDir()
	// and httptest both use the temp directory.
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	// Now set TMPDIR to a nonexistent path so os.MkdirTemp("", ...) fails.
	// t.Setenv restores the original value after the test.
	t.Setenv("TMPDIR", "/nonexistent-tmpdir-xyz-abc")

	release := Release{
		TagName: "v0.7.0",
		Assets: []Asset{
			{Name: "motita-linux-amd64", BrowserDownloadURL: srv.URL + "/motita-linux-amd64"},
		},
	}
	u := &Updater{
		Goos:       "linux",
		Goarch:     "amd64",
		ExePath:    filepath.Join(dir, "motita"),
		HTTPClient: srv.Client(),
	}
	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected MkdirTemp error")
	}
}
