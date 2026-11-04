package localmodel

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// THE RUNTIME: llama.cpp's llama-server.
//
// Motita does not carry it (its binary stays small): a llama-server already on PATH is used as it
// is, and otherwise the user's Install downloads the official build of ggml-org/llama.cpp from its
// GitHub releases, the one for this system and GPU family (Metal on Apple Silicon, Vulkan on
// Linux and Windows, which runs on NVIDIA, AMD and Intel alike). GitHub publishes the SHA-256 of
// every release asset; an asset without one is refused, and the archive is checked against it
// before anything is unpacked. It is unpacked under the runtime folder and nowhere else.

// releasesAPI is where the llama.cpp releases are listed; a variable so a test can serve its own.
var releasesAPI = "https://api.github.com/repos/ggml-org/llama.cpp/releases"

// TagVariable pins the llama.cpp release to install (e.g. b6500); unset installs the latest.
const TagVariable = "MOTITA_LOCAL_LLAMA_TAG"

// maxReleaseJSON bounds the release metadata read.
const maxReleaseJSON = 8 << 20

// maxUnpacked bounds what one archive may unpack to, against an archive bomb; a variable so a test
// can make it small.
var maxUnpacked int64 = 2 << 30

type release struct {
	Tag    string  `json:"tag_name"`
	Assets []asset `json:"assets"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // "sha256:<hex>"
}

// assetPatterns are the build names to look for, best first, for a system.
func assetPatterns(goos, goarch string) []string {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return []string{"-bin-ubuntu-vulkan-x64.", "-bin-ubuntu-x64."}
	case "linux/arm64":
		return []string{"-bin-ubuntu-vulkan-arm64.", "-bin-ubuntu-arm64."}
	case "windows/amd64":
		return []string{"-bin-win-vulkan-x64.", "-bin-win-cpu-x64."}
	case "windows/arm64":
		return []string{"-bin-win-cpu-arm64."}
	case "darwin/arm64":
		return []string{"-bin-macos-arm64."}
	case "darwin/amd64":
		return []string{"-bin-macos-x64."}
	default:
		return nil
	}
}

// pickAsset is the release's build for this system, or an error saying none fits.
func pickAsset(r release, goos, goarch string) (asset, error) {
	for _, pattern := range assetPatterns(goos, goarch) {
		for _, a := range r.Assets {
			if strings.HasPrefix(a.Name, "llama-") && strings.Contains(a.Name, pattern) && isArchive(a.Name) {
				return a, nil
			}
		}
	}
	return asset{}, fmt.Errorf("llama.cpp %s has no build for %s/%s", r.Tag, goos, goarch)
}

func isArchive(name string) bool {
	return strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz")
}

// fetchRelease reads the release to install: the pinned tag, or the latest.
func fetchRelease(ctx context.Context, client *http.Client, tag string) (release, error) {
	url := releasesAPI + "/latest"
	if tag != "" {
		url = releasesAPI + "/tags/" + tag
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return release{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("llama.cpp releases: GitHub answered %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseJSON)).Decode(&r); err != nil {
		return release{}, fmt.Errorf("llama.cpp releases: %w", err)
	}
	if !safeName(r.Tag) {
		return release{}, fmt.Errorf("llama.cpp releases: unexpected tag %q", r.Tag)
	}
	return r, nil
}

// safeName is a tag that can be a folder name.
func safeName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// digestOf is the hex SHA-256 GitHub published for an asset, or "".
func digestOf(a asset) string {
	hex, ok := strings.CutPrefix(a.Digest, "sha256:")
	if !ok || len(hex) != 64 {
		return ""
	}
	return hex
}

// serverName is llama-server's file name on this system.
func serverName(goos string) string {
	if goos == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

// lookPath finds a llama-server on PATH; a variable so a test can say there is none.
var lookPath = exec.LookPath

// findServer is the llama-server under dir, or "".
func findServer(dir, goos string) string {
	want := serverName(goos)
	found := ""
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == want {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// unpack extracts archive into dir, refusing any entry that would land outside it.
func unpack(archive, dir string) error {
	if strings.HasSuffix(archive, ".zip") {
		return unzip(archive, dir)
	}
	return untar(archive, dir)
}

// inside is the path an entry lands at under dir, or an error when it would escape it.
func inside(dir, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("archive entry %q points outside its folder", name)
	}
	return filepath.Join(dir, clean), nil
}

// budget counts what an archive unpacks to.
type budget struct{ left int64 }

func (b *budget) copy(dst io.Writer, src io.Reader) error {
	n, err := io.Copy(dst, io.LimitReader(src, b.left+1))
	b.left -= n
	if b.left < 0 {
		return errors.New("the archive unpacks to more than expected")
	}
	return err
}

func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	b := &budget{left: maxUnpacked}
	for _, f := range zr.File {
		if err := unzipOne(f, dir, b); err != nil {
			return err
		}
	}
	return nil
}

func unzipOne(f *zip.File, dir string, b *budget) error {
	path, err := inside(dir, f.Name)
	if err != nil {
		return err
	}
	mode := f.Mode()
	if mode.IsDir() {
		return os.MkdirAll(path, 0o755)
	}
	if !mode.IsRegular() {
		return nil // symlinks and the like are skipped: a link could point anywhere
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	return writeFile(path, mode, src, b)
}

func untar(archive, dir string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	b := &budget{left: maxUnpacked}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path, err := inside(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(path, fs.FileMode(h.Mode), tr, b); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// llama.cpp's Linux builds link libfoo.so to libfoo.so.N: keep links that stay in the
			// same folder and name a plain file.
			if err := sameFolderLink(path, h.Linkname); err != nil {
				return err
			}
		}
	}
}

// sameFolderLink makes path a link to target when target is a bare name beside it.
func sameFolderLink(path, target string) error {
	if target == "" || strings.ContainsAny(target, `/\`) || target == ".." || target == "." {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Symlink(target, path)
}

func writeFile(path string, mode fs.FileMode, src io.Reader, b *budget) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := b.copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}
