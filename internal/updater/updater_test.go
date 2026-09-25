package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
