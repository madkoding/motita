// Package updater checks GitHub Releases for a newer Motita binary, downloads
// it, verifies its checksum, atomically replaces the running binary, and
// restarts the gateway.
//
// The release source is the GitHub API for madkoding/motita. The binary is
// downloaded to a temporary file, checked against the release's SHA256SUMS
// (itself verified against an ed25519 signature, SHA256SUMS.sig, once a release
// key is embedded in ReleasePublicKey), and renamed over the running executable — which works on Linux/macOS even
// while the process is running (the kernel keeps the old inode alive until
// the process exits). On Windows, the rename is done after the process exits,
// which is why the restart sequence is download → rename → spawn-new → exit.
package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/logx"
)

// Repo is the GitHub repository releases are published to.
const Repo = "madkoding/motita"

// ReleasePublicKey is the ed25519 key every release's SHA256SUMS is signed with, written as the
// base64 body of its PEM public key (the line `openssl pkey -pubout` prints between the BEGIN and
// END markers). scripts/release-signing-key.sh (`make release-key`) generates the pair, prints this
// value, and says where the private half goes (the RELEASE_SIGNING_KEY secret, never the repo).
//
// With it set, a release without a valid SHA256SUMS.sig is refused (an empty value would fall back
// to the checksum-only behaviour with a warning). The installers carry the same value.
const ReleasePublicKey = "MCowBQYDK2VwAyEASjFxVSWdJ8wOLF06ooBU3hDEWtR+aRwNbRAog83ZW1c="

// DefaultAssetHosts are the hosts release files may be fetched from, always over https: the
// download URL itself and the CDN hosts GitHub redirects it to.
var DefaultAssetHosts = []string{
	"github.com",
	"api.github.com",
	"objects.githubusercontent.com",
	"release-assets.githubusercontent.com",
}

// Download ceilings. A release binary is held under 24 MB by CI, so the binary cap is generous; the
// checksum file is a few lines; a signature is exactly ed25519.SignatureSize bytes.
const (
	maxBinarySize    = 100 << 20
	maxChecksumsSize = 64 << 10
)

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
	// PublicKey verifies SHA256SUMS.sig. Nil means no release key is configured: the checksum
	// alone is checked, and a warning says so.
	PublicKey ed25519.PublicKey
	// AssetHosts are the hosts release files may come from (https only). Nil means
	// DefaultAssetHosts. Injectable for tests.
	AssetHosts []string
}

// apiURLFor returns the real GitHub API URL for the latest release.
func apiURLFor() string {
	return fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo)
}

// New returns an Updater for the current process.
func New(version, exePath string) *Updater {
	u := &Updater{
		CurrentVersion: version,
		Goos:           runtime.GOOS,
		Goarch:         runtime.GOARCH,
		ExePath:        exePath,
		APIURL:         apiURLFor,
		PublicKey:      mustPublicKey(ReleasePublicKey),
	}
	u.HTTPClient = &http.Client{
		Timeout: 30 * time.Second,
		// A redirect is held to the same rule as the URL it came from: https, to a GitHub host.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return u.checkURL(req.URL.String())
		},
	}
	return u
}

// parsePublicKey decodes a ReleasePublicKey-shaped value. An empty value is no key at all.
func parsePublicKey(s string) (ed25519.PublicKey, error) {
	if s == "" {
		return nil, nil
	}
	der, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("the release public key is not base64: %w", err)
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("the release public key does not parse: %w", err)
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("the release public key is a %T, not ed25519", key)
	}
	return pub, nil
}

// mustPublicKey is parsePublicKey for the embedded constant: a malformed constant is a build
// mistake, and failing loudly beats silently falling back to checksum-only updates.
func mustPublicKey(s string) ed25519.PublicKey {
	pub, err := parsePublicKey(s)
	if err != nil {
		panic(err)
	}
	return pub
}

// checkURL refuses a release file URL that is not https or not on an allowed host. The release
// JSON is what names these URLs, so they are checked rather than trusted.
func (u *Updater) checkURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("the release URL does not parse: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("refusing %s: release files are only fetched over https", raw)
	}
	hosts := u.AssetHosts
	if hosts == nil {
		hosts = DefaultAssetHosts
	}
	for _, h := range hosts {
		if strings.EqualFold(parsed.Hostname(), h) {
			return nil
		}
	}
	return fmt.Errorf("refusing %s: %s is not a GitHub release host", raw, parsed.Hostname())
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

// findAsset returns the binary asset matching this OS/arch.
func (u *Updater) findAsset(release *Release) (Asset, error) {
	want := u.assetName()
	for _, a := range release.Assets {
		if a.Name == want {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("the release %s has no asset for %s/%s", release.TagName, u.Goos, u.Goarch)
}

// findURL returns the download URL of the release asset with this name, or "".
func findURL(release *Release, name string) string {
	for _, a := range release.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// findChecksumsURL looks for a SHA256SUMS asset in the release.
func (u *Updater) findChecksumsURL(release *Release) string {
	return findURL(release, "SHA256SUMS")
}

// DownloadAndInstall downloads the binary, verifies it, and replaces the
// running executable. The progress callback is called with stage/percent
// updates so the caller can stream them to the frontend.
func (u *Updater) DownloadAndInstall(ctx context.Context, release *Release, progress func(ProgressEvent)) error {
	asset, err := u.findAsset(release)
	if err != nil {
		return err
	}

	// The release is re-checked here, not only in Check: the gateway fetches it a second time
	// before installing, and that answer is the one that gets installed. An older or equal tag is
	// refused, so a stale or replayed release cannot downgrade the binary.
	if !isNewer(u.CurrentVersion, release.TagName) {
		return fmt.Errorf("the release %s is not newer than %s; refusing to downgrade", release.TagName, u.CurrentVersion)
	}

	// The checksums are REQUIRED, and are looked for before anything is downloaded. A release
	// without them used to install unverified, which turned "the publisher forgot a file" into
	// "run whatever the download returned". Refusing early also spares the user a download that
	// is going to be thrown away.
	checksumsURL := u.findChecksumsURL(release)
	if checksumsURL == "" {
		return fmt.Errorf("the release %s publishes no SHA256SUMS, so the download cannot be verified and will not be installed", release.TagName)
	}

	// With a release key embedded, the checksums only count when they are signed by it: a
	// checksum file published beside the binary vouches for nothing if both were uploaded by
	// whoever compromised the release. Without a key (the placeholder) the old behaviour stays,
	// so updates keep working until a key is configured.
	sigURL := ""
	if u.PublicKey != nil {
		sigURL = findURL(release, "SHA256SUMS.sig")
		if sigURL == "" {
			return fmt.Errorf("the release %s publishes no SHA256SUMS.sig, so its checksums cannot be trusted and nothing will be installed", release.TagName)
		}
	} else {
		logx.Global().Warn("no release signing key is embedded; verifying the update by checksum only", "release", release.TagName)
	}
	for _, raw := range []string{asset.BrowserDownloadURL, checksumsURL, sigURL} {
		if raw == "" {
			continue
		}
		if err := u.checkURL(raw); err != nil {
			return err
		}
	}

	// The download is capped at the size the release declares, so a server cannot stream an
	// endless body into the staging directory.
	limit := asset.Size
	if limit > maxBinarySize {
		return fmt.Errorf("the release asset %s declares %d bytes, more than the %d allowed", asset.Name, asset.Size, maxBinarySize)
	}
	if limit <= 0 {
		limit = maxBinarySize
	}

	// Stage 1: download
	progress(ProgressEvent{Stage: "downloading", Percent: 0, Message: "Downloading " + release.TagName})
	tmpDir, err := os.MkdirTemp(u.stagingDir(), ".motita-update-*")
	if err != nil {
		return fmt.Errorf("could not create a temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpBin := filepath.Join(tmpDir, u.assetName())
	if err := u.downloadFile(ctx, asset.BrowserDownloadURL, limit, tmpBin, progress); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Stage 2: verify checksum
	progress(ProgressEvent{Stage: "verifying", Percent: 100, Message: "Verifying checksum"})
	if err := u.verifyChecksum(ctx, checksumsURL, sigURL, tmpBin); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Stage 3: install (replace the running binary)
	progress(ProgressEvent{Stage: "installing", Percent: 100, Message: "Installing"})
	if err := u.install(tmpBin); err != nil {
		return fmt.Errorf("could not install the binary: %w", err)
	}

	progress(ProgressEvent{Stage: "installed", Percent: 100, Message: "Installed " + release.TagName})
	return nil
}

// stagingDir is where the download waits to be installed: beside the executable it will replace.
//
// It used to be the system temp directory. Reported from a real machine: /tmp was a tmpfs and the
// binary lived on the root disk, so the final rename failed with "invalid cross-device link" -
// after the download and the checksum had both succeeded - and /update could never work there. A
// rename is only atomic (and only possible) within one filesystem, and the executable's own
// directory is the one place guaranteed to share it. Without a configured path there is nothing to
// sit beside, and install refuses anyway.
func (u *Updater) stagingDir() string {
	if u.ExePath == "" {
		return os.TempDir()
	}
	return filepath.Dir(u.ExePath)
}

// downloadFile downloads a URL to a local path, reporting progress. More than limit bytes is an
// error rather than a bigger file.
func (u *Updater) downloadFile(ctx context.Context, url string, limit int64, dest string, progress func(ProgressEvent)) error {
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

	body := io.LimitReader(resp.Body, limit+1)
	buf := make([]byte, 32*1024)
	var written int64
	lastReport := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := body.Read(buf)
		if written+int64(n) > limit {
			return fmt.Errorf("the download is larger than the %d bytes expected", limit)
		}
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

// fetchSmall downloads a small release file whole, refusing one longer than max bytes.
func (u *Updater) fetchSmall(ctx context.Context, url, name string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", name, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, max)
	}
	return body, nil
}

// verifyChecksum downloads SHA256SUMS, checks its signature when a release key is configured,
// finds the entry for our asset, and compares it with the downloaded file's SHA-256.
func (u *Updater) verifyChecksum(ctx context.Context, checksumsURL, sigURL, binPath string) error {
	body, err := u.fetchSmall(ctx, checksumsURL, "SHA256SUMS", maxChecksumsSize)
	if err != nil {
		return err
	}
	if u.PublicKey != nil {
		sig, err := u.fetchSmall(ctx, sigURL, "SHA256SUMS.sig", ed25519.SignatureSize)
		if err != nil {
			return err
		}
		if !ed25519.Verify(u.PublicKey, body, sig) {
			return errors.New("SHA256SUMS.sig is not a valid signature by the release key")
		}
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
// next upgrade. If either rename fails the upgrade fails: copying over the
// target in place is not atomic and can leave a truncated executable behind.
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
			return fmt.Errorf("could not move the current binary aside: %w", err)
		}
		if err := os.Rename(tempPath, target); err != nil {
			// Put the previous binary back so there is still one to start; if even that fails,
			// it is left at <target>.old for the user to recover.
			_ = os.Rename(old, target)
			return err
		}
		return nil
	}

	// Linux/macOS: atomic rename over the running binary.
	if err := os.Chmod(tempPath, 0o755); err != nil {
		return err
	}
	return os.Rename(tempPath, target)
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
