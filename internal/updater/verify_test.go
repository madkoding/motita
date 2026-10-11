package updater

// The release-trust checks: the SHA256SUMS signature, the https + host allowlist on every release
// URL, the downgrade refusal, and the size ceilings. The key pair is generated per test run, so no
// private key ever lives in the repository.

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testHosts lets the TLS test servers (which listen on 127.0.0.1) stand in for GitHub.
var testHosts = []string{"127.0.0.1"}

// signedRelease serves a binary, its SHA256SUMS and, when sig is not nil, a SHA256SUMS.sig with
// that content. sign returns the signature of the checksum file it is given.
type signedRelease struct {
	srv     *httptest.Server
	release Release
	sums    []byte
}

func newSignedRelease(t *testing.T, sig func(sums []byte) []byte) *signedRelease {
	t.Helper()
	name := "motita-linux-amd64"
	bin := []byte("signed binary payload")
	sums := []byte(fmt.Sprintf("%s  %s\n", sha256hex(bin), name))

	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(bin) })
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(sums) })
	assets := []Asset{
		{Name: name, BrowserDownloadURL: srv.URL + "/" + name, Size: int64(len(bin))},
		{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/SHA256SUMS"},
	}
	if sig != nil {
		body := sig(sums)
		mux.HandleFunc("/SHA256SUMS.sig", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
		assets = append(assets, Asset{Name: "SHA256SUMS.sig", BrowserDownloadURL: srv.URL + "/SHA256SUMS.sig"})
	}
	return &signedRelease{srv: srv, release: Release{TagName: "v0.7.0", Assets: assets}, sums: sums}
}

func (r *signedRelease) updater(t *testing.T, pub ed25519.PublicKey) (*Updater, string) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "motita")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{
		CurrentVersion: "v0.6.0",
		Goos:           "linux",
		Goarch:         "amd64",
		ExePath:        target,
		HTTPClient:     r.srv.Client(),
		AssetHosts:     testHosts,
		PublicKey:      pub,
	}, target
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func assertUntouched(t *testing.T, target string) {
	t.Helper()
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Errorf("the installed binary was modified: %q", got)
	}
}

func TestASignedReleaseInstalls(t *testing.T) {
	pub, priv := newKey(t)
	rel := newSignedRelease(t, func(sums []byte) []byte { return ed25519.Sign(priv, sums) })
	u, target := rel.updater(t, pub)

	if err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {}); err != nil {
		t.Fatalf("a correctly signed release must install: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "signed binary payload" {
		t.Errorf("target = %q, want the new binary", got)
	}
}

// A checksum file and binary that agree prove nothing when both were uploaded by whoever holds the
// release: the signature is what binds them to the publisher.
func TestASignatureByAnotherKeyIsRefused(t *testing.T) {
	pub, _ := newKey(t)
	_, otherPriv := newKey(t)
	rel := newSignedRelease(t, func(sums []byte) []byte { return ed25519.Sign(otherPriv, sums) })
	u, target := rel.updater(t, pub)

	err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {})
	if err == nil || !strings.Contains(err.Error(), "not a valid signature") {
		t.Fatalf("a signature by another key must be refused, got %v", err)
	}
	assertUntouched(t, target)
}

func TestAReleaseWithoutASignatureIsRefusedOnceAKeyIsSet(t *testing.T) {
	pub, _ := newKey(t)
	rel := newSignedRelease(t, nil)
	u, target := rel.updater(t, pub)

	err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {})
	if err == nil || !strings.Contains(err.Error(), "SHA256SUMS.sig") {
		t.Fatalf("an unsigned release must be refused when a key is set, got %v", err)
	}
	assertUntouched(t, target)
}

// The placeholder key keeps today's behaviour: an unsigned release still installs on its checksum.
func TestWithoutAKeyTheChecksumAloneIsChecked(t *testing.T) {
	rel := newSignedRelease(t, nil)
	u, _ := rel.updater(t, nil)
	if err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {}); err != nil {
		t.Fatalf("without a key the checksum-only path must keep working: %v", err)
	}
}

func TestAnOversizedSignatureIsRefused(t *testing.T) {
	pub, priv := newKey(t)
	rel := newSignedRelease(t, func(sums []byte) []byte { return append(ed25519.Sign(priv, sums), 0) })
	u, target := rel.updater(t, pub)

	err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {})
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("a signature longer than ed25519.SignatureSize must be refused, got %v", err)
	}
	assertUntouched(t, target)
}

func TestAnOversizedChecksumFileIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, maxChecksumsSize+1))
	}))
	defer srv.Close()
	u := &Updater{Goos: "linux", Goarch: "amd64", HTTPClient: srv.Client()}
	err := u.verifyChecksum(context.Background(), srv.URL, "", filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("a SHA256SUMS over the cap must be refused, got %v", err)
	}
}

// A replayed or stale release must not downgrade the binary, even though the gateway's earlier
// Check said an update was available.
func TestAnOlderOrEqualReleaseIsNotInstalled(t *testing.T) {
	for _, current := range []string{"v0.7.0", "v0.8.0"} {
		rel := newSignedRelease(t, nil)
		u, target := rel.updater(t, nil)
		u.CurrentVersion = current
		err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {})
		if err == nil || !strings.Contains(err.Error(), "downgrade") {
			t.Fatalf("current %s: installing v0.7.0 must be refused, got %v", current, err)
		}
		assertUntouched(t, target)
	}
}

// Every URL the release JSON names is checked before anything is fetched.
func TestReleaseURLsOutsideTheAllowlistAreRefused(t *testing.T) {
	pub, priv := newKey(t)
	cases := map[string]int{"binary": 0, "SHA256SUMS": 1, "SHA256SUMS.sig": 2}
	for name, i := range cases {
		rel := newSignedRelease(t, func(sums []byte) []byte { return ed25519.Sign(priv, sums) })
		rel.release.Assets[i].BrowserDownloadURL = "http://127.0.0.1/" + name
		u, target := rel.updater(t, pub)
		err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {})
		if err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatalf("%s over http must be refused, got %v", name, err)
		}
		assertUntouched(t, target)
	}
}

func TestAnAssetDeclaringAnAbsurdSizeIsRefused(t *testing.T) {
	rel := newSignedRelease(t, nil)
	rel.release.Assets[0].Size = maxBinarySize + 1
	u, target := rel.updater(t, nil)
	err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {})
	if err == nil || !strings.Contains(err.Error(), "declares") {
		t.Fatalf("an asset over the ceiling must be refused, got %v", err)
	}
	assertUntouched(t, target)
}

// A body longer than the declared size is cut off, not written out in full.
func TestADownloadLongerThanDeclaredIsRefused(t *testing.T) {
	rel := newSignedRelease(t, nil)
	rel.release.Assets[0].Size = 4
	u, target := rel.updater(t, nil)
	err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {})
	if err == nil || !strings.Contains(err.Error(), "larger than the 4 bytes") {
		t.Fatalf("a download over its declared size must be refused, got %v", err)
	}
	assertUntouched(t, target)
}

// An asset with no declared size still gets the global ceiling rather than no limit at all.
func TestAnAssetWithoutASizeStillInstalls(t *testing.T) {
	rel := newSignedRelease(t, nil)
	rel.release.Assets[0].Size = 0
	u, _ := rel.updater(t, nil)
	if err := u.DownloadAndInstall(context.Background(), &rel.release, func(ProgressEvent) {}); err != nil {
		t.Fatalf("an asset without a declared size must still install under the ceiling: %v", err)
	}
}

func TestCheckURL(t *testing.T) {
	u := &Updater{}
	for _, ok := range []string{
		"https://github.com/madkoding/motita/releases/download/v1.0.0/SHA256SUMS",
		"https://release-assets.githubusercontent.com/x",
		"https://OBJECTS.githubusercontent.com/x",
	} {
		if err := u.checkURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for raw, want := range map[string]string{
		"http://github.com/x":                "https",
		"https://evil.example/x":             "not a GitHub release host",
		"https://github.com.evil.example/x":  "not a GitHub release host",
		"https://github.com\x7f/x":           "does not parse",
		"file:///etc/passwd":                 "https",
		"https://user@evil.example@github.x": "not a GitHub release host",
	} {
		if err := u.checkURL(raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error naming %q", raw, err, want)
		}
	}
}

// The client New builds re-checks every redirect, so an allowed URL cannot bounce the download to
// plain http or to another host.
func TestNewRefusesARedirectOffTheAllowlist(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer target.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, "http://127.0.0.1/x", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			http.Redirect(w, r, target.URL+"/ok", http.StatusFound)
		}
	}))
	defer srv.Close()

	u := New("v0.1.0", "")
	u.HTTPClient.Transport = srv.Client().Transport
	u.AssetHosts = testHosts
	dest := filepath.Join(t.TempDir(), "x")
	if err := u.downloadFile(context.Background(), srv.URL+"/ok", maxBinarySize, dest, func(ProgressEvent) {}); err != nil {
		t.Fatalf("an https redirect to an allowed host must be followed: %v", err)
	}
	if err := u.downloadFile(context.Background(), srv.URL+"/to-http", maxBinarySize, dest, func(ProgressEvent) {}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("a redirect to http must be refused, got %v", err)
	}
	if err := u.downloadFile(context.Background(), srv.URL+"/loop", maxBinarySize, dest, func(ProgressEvent) {}); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("a redirect loop must stop, got %v", err)
	}
}

func TestParsePublicKey(t *testing.T) {
	if pub, err := parsePublicKey(""); pub != nil || err != nil {
		t.Errorf("an empty key is no key: %v %v", pub, err)
	}

	pub, _ := newKey(t)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parsePublicKey(base64.StdEncoding.EncodeToString(der))
	if err != nil || !got.Equal(pub) {
		t.Errorf("a PEM-body ed25519 key must round-trip, got %v %v", got, err)
	}

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKIXPublicKey(&ec.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"not base64!": "not base64",
		base64.StdEncoding.EncodeToString([]byte("x")): "does not parse",
		base64.StdEncoding.EncodeToString(ecDER):       "not ed25519",
	} {
		if _, err := parsePublicKey(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", in, err, want)
		}
	}
}

// The embedded constant is parsed at startup; a malformed one is a build mistake and must not
// quietly degrade to checksum-only updates.
func TestMustPublicKeyPanicsOnAMalformedKey(t *testing.T) {
	if mustPublicKey(ReleasePublicKey) == nil && ReleasePublicKey != "" {
		t.Error("a configured key must parse")
	}
	defer func() {
		if recover() == nil {
			t.Error("a malformed key must panic")
		}
	}()
	mustPublicKey("not base64!")
}
