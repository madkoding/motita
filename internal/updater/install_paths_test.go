package updater

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	srv := httptest.NewTLSServer(mux)
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
		AssetHosts: testHosts,
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

func TestDownloadAndInstallRefusesAReleaseWithoutChecksums(t *testing.T) {
	// A release with no SHA256SUMS is refused BEFORE the download, and the installed binary is
	// left untouched: nothing unverifiable is ever put in place.
	name := "motita-linux-amd64"
	downloaded := false

	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		downloaded = true
		_, _ = w.Write([]byte("unverifiable binary"))
	})

	release := Release{
		TagName: "v0.7.0",
		Assets:  []Asset{{Name: name, BrowserDownloadURL: srv.URL + "/" + name}},
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "motita")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Goos: "linux", Goarch: "amd64", ExePath: target, HTTPClient: srv.Client(), AssetHosts: testHosts}

	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
		t.Fatalf("a release without checksums must be refused, got %v", err)
	}
	if downloaded {
		t.Error("the binary must not be downloaded when it cannot be verified")
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Errorf("the installed binary was modified: %q", got)
	}
}

func TestDownloadAndInstallAssetMissing(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
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
		AssetHosts: testHosts,
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
	srv := httptest.NewTLSServer(mux)
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
		AssetHosts: testHosts,
	}

	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected checksum mismatch error")
	}
}

func TestDownloadAndInstallDownloadFails(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "oops", http.StatusInternalServerError)
	}))
	defer srv.Close()

	release := Release{
		TagName: "v0.7.0",
		Assets: []Asset{
			{Name: "motita-linux-amd64", BrowserDownloadURL: srv.URL + "/motita-linux-amd64"},
			{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/SHA256SUMS"},
		},
	}

	u := &Updater{
		Goos:       "linux",
		Goarch:     "amd64",
		ExePath:    filepath.Join(t.TempDir(), "motita"),
		HTTPClient: srv.Client(),
		AssetHosts: testHosts,
	}

	err := u.DownloadAndInstall(context.Background(), &release, func(ProgressEvent) {})
	if err == nil {
		t.Error("expected download failure")
	}
}
