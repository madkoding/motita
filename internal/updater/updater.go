// Package updater checks GitHub Releases for a newer Motita binary, downloads
// it, verifies its checksum, atomically replaces the running binary, and
// restarts the gateway.
//
// The release source is the GitHub API for madkoding/motita. The binary is
// downloaded to a temporary file, checked against the release's SHA256SUMS,
// and renamed over the running executable — which works on Linux/macOS even
// while the process is running (the kernel keeps the old inode alive until
// the process exits). On Windows, the rename is done after the process exits,
// which is why the restart sequence is download → rename → spawn-new → exit.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are published to.
const Repo = "madkoding/motita"

// Release is the information about the latest release that matters to us.
type Release struct {
	TagName     string  `json:"tag_name"`     // e.g. "v0.5.0"
	Name        string  `json:"name"`         // human-readable title
	PublishedAt string  `json:"published_at"` // ISO 8601
	HTMLURL     string  `json:"html_url"`     // browser-visible release page
	Assets      []Asset `json:"assets"`
}

// Asset is one downloadable file in a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// CheckResult is what /v1/update/check returns to the frontend.
type CheckResult struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url,omitempty"`
	ReleaseName     string `json:"release_name,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	Error           string `json:"error,omitempty"`
}

// ProgressEvent is one SSE event sent during the upgrade stream.
type ProgressEvent struct {
	Stage   string `json:"stage"`             // checking, downloading, verifying, installing, restarting, done, error
	Percent int    `json:"percent,omitempty"` // 0-100
	Message string `json:"message,omitempty"`
	Version string `json:"version,omitempty"`
}

// Updater holds the current version and OS/arch info, and provides methods
// to check for updates and perform the upgrade.
type Updater struct {
	CurrentVersion string
	Goos           string
	Goarch         string
	// ExePath is the running binary's path, used to replace it.
	ExePath string
	// HTTPClient is used for all requests. Injectable for tests.
	HTTPClient *http.Client
	// APIURL returns the GitHub releases API endpoint. Injectable for tests.
	APIURL func() string
}

// apiURLFor returns the real GitHub API URL for the latest release.
func apiURLFor() string {
	return fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo)
}

// New returns an Updater for the current process.
func New(version, exePath string) *Updater {
	return &Updater{
		CurrentVersion: version,
		Goos:           runtime.GOOS,
		Goarch:         runtime.GOARCH,
		ExePath:        exePath,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		APIURL: apiURLFor,
	}
}

// assetName returns the expected asset name for this OS/arch.
// e.g. "motita-linux-amd64", "motita-darwin-arm64", "motita-windows-386.exe"
func (u *Updater) assetName() string {
	ext := ""
	if u.Goos == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("motita-%s-%s%s", u.Goos, u.Goarch, ext)
}

// Check queries the GitHub API for the latest release and compares it with
// the current version.
func (u *Updater) Check(ctx context.Context) CheckResult {
	result := CheckResult{CurrentVersion: u.CurrentVersion}

	release, err := u.fetchLatestRelease(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.LatestVersion = release.TagName
	result.ReleaseURL = release.HTMLURL
	result.ReleaseName = release.Name
	result.PublishedAt = release.PublishedAt
	result.UpdateAvailable = isNewer(u.CurrentVersion, release.TagName)
	return result
}

// LatestRelease fetches the full release info from GitHub. Exported so the
// gateway's upgrade handler can get the asset URLs after a check.
func (u *Updater) LatestRelease(ctx context.Context) (*Release, error) {
	return u.fetchLatestRelease(ctx)
}

// fetchLatestRelease calls the GitHub Releases API.
func (u *Updater) fetchLatestRelease(ctx context.Context) (*Release, error) {
	url := apiURLFor()
	if u.APIURL != nil {
		url = u.APIURL()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("could not build the release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub answered %d: %s", resp.StatusCode, string(body))
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("could not parse the release response: %w", err)
	}
	return &release, nil
}

// findAsset returns the download URL for the binary matching this OS/arch.
func (u *Updater) findAsset(release *Release) (string, error) {
	want := u.assetName()
	for _, a := range release.Assets {
		if a.Name == want {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("the release %s has no asset for %s/%s", release.TagName, u.Goos, u.Goarch)
}

// findChecksumsURL looks for a SHA256SUMS asset in the release.
func (u *Updater) findChecksumsURL(release *Release) string {
	for _, a := range release.Assets {
		if a.Name == "SHA256SUMS" {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// DownloadAndInstall downloads the binary, verifies it, and replaces the
// running executable. The progress callback is called with stage/percent
// updates so the caller can stream them to the frontend.
func (u *Updater) DownloadAndInstall(ctx context.Context, release *Release, progress func(ProgressEvent)) error {
	assetURL, err := u.findAsset(release)
	if err != nil {
		return err
	}

	// Stage 1: download
	progress(ProgressEvent{Stage: "downloading", Percent: 0, Message: "Downloading " + release.TagName})
	tmpDir, err := os.MkdirTemp("", "motita-update-*")
	if err != nil {
		return fmt.Errorf("could not create a temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpBin := filepath.Join(tmpDir, u.assetName())
	if err := u.downloadFile(ctx, assetURL, tmpBin, progress); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Stage 2: verify checksum
	checksumsURL := u.findChecksumsURL(release)
	if checksumsURL != "" {
		progress(ProgressEvent{Stage: "verifying", Percent: 100, Message: "Verifying checksum"})
		if err := u.verifyChecksum(ctx, checksumsURL, tmpBin); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	// Stage 3: install (replace the running binary)
	progress(ProgressEvent{Stage: "installing", Percent: 100, Message: "Installing"})
	if err := u.install(tmpBin); err != nil {
		return fmt.Errorf("could not install the binary: %w", err)
	}

	progress(ProgressEvent{Stage: "installed", Percent: 100, Message: "Installed " + release.TagName})
	return nil
}

// downloadFile downloads a URL to a local path, reporting progress.
func (u *Updater) downloadFile(ctx context.Context, url, dest string, progress func(ProgressEvent)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the download answered %d", resp.StatusCode)
	}

	total := resp.ContentLength
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	// Make it executable before renaming.
	_ = os.Chmod(dest, 0o755)

	buf := make([]byte, 32*1024)
	var written int64
	lastReport := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			// Report progress at most every 200ms.
			if total > 0 && time.Since(lastReport) > 200*time.Millisecond {
				pct := int(written * 100 / total)
				progress(ProgressEvent{Stage: "downloading", Percent: pct, Message: fmt.Sprintf("Downloaded %d%%", pct)})
				lastReport = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if total > 0 {
		progress(ProgressEvent{Stage: "downloading", Percent: 100, Message: "Download complete"})
	}
	return nil
}

// verifyChecksum downloads SHA256SUMS, finds the entry for our asset, and
// compares it with the downloaded file's SHA-256.
func (u *Updater) verifyChecksum(ctx context.Context, checksumsURL, binPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return err
	}
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SHA256SUMS answered %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	want := u.assetName()
	expectedHash := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "<hash>  <filename>"
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == want {
			expectedHash = parts[0]
			break
		}
	}
	if expectedHash == "" {
		return fmt.Errorf("SHA256SUMS has no entry for %s", want)
	}

	// Compute the file's hash.
	f, err := os.Open(binPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	return nil
}

// install replaces the running binary with the downloaded one.
//
// On Linux/macOS, renaming over a running binary works: the kernel keeps the
// old inode alive until the process exits, and the new file takes the name.
// On Windows, the running binary is locked, so we rename the old one aside
// and then rename the new one into place; the old file is cleaned up on the
// next restart.
func (u *Updater) install(tempPath string) error {
	target := u.ExePath
	if target == "" {
		return fmt.Errorf("no executable path was configured")
	}

	// The target's directory was once computed here and never read: the rename and the copy both
	// take the full path. Removing it is what makes the last statement of this function
	// reachable - a dead branch is a statement no test can execute and no reader should trust.
	if u.Goos == "windows" {
		old := target + ".old"
		_ = os.Remove(old) // clean up a previous upgrade's leftover
		if err := os.Rename(target, old); err != nil {
			// If the rename failed, try a direct copy.
			return copyFile(tempPath, target)
		}
		return os.Rename(tempPath, target)
	}

	// Linux/macOS: atomic rename over the running binary.
	if err := os.Chmod(tempPath, 0o755); err != nil {
		return err
	}
	return os.Rename(tempPath, target)
}

// copyFile is the fallback for Windows when rename fails.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

// isNewer compares two semver-ish version strings. Both may carry a leading
// "v". Returns true when latest is strictly newer than current.
//
// A current version of "dev" (a build from source without -ldflags) is always
// considered older than any release tag, so a developer running from source
// will be offered the upgrade — and can choose to take it or not.
func isNewer(current, latest string) bool {
	c := normalizeVersion(current)
	l := normalizeVersion(latest)
	if c == "" || c == "dev" {
		return l != "" && l != "dev"
	}
	if l == "" {
		return false
	}
	return compareSemver(l, c) > 0
}

// normalizeVersion strips a leading "v" and any trailing build metadata.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	// Strip pre-release suffix for comparison: "0.5.0-rc1" -> "0.5.0".
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// compareSemver compares two "x.y.z" strings. Returns >0 if a > b, <0 if a < b,
// 0 if equal.
func compareSemver(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	maxLen := len(pa)
	if len(pb) > maxLen {
		maxLen = len(pb)
	}
	for i := 0; i < maxLen; i++ {
		var na, nb int
		if i < len(pa) {
			na = atoiSafe(pa[i])
		}
		if i < len(pb) {
			nb = atoiSafe(pb[i])
		}
		if na != nb {
			return na - nb
		}
	}
	return 0
}

// atoiSafe parses a decimal integer, returning 0 on failure.
func atoiSafe(s string) int {
	s = strings.TrimSpace(s)
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
