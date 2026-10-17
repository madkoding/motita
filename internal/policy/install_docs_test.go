package policy

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The installers refuse to install when they cannot check SHA256SUMS.sig, and the check needs
// OpenSSL 3. Stock macOS ships LibreSSL, which has no `pkeyutl -rawin`, so the one-line install
// stops there. A reader has to learn that from the docs before running the line, not from the
// error after it.
func TestTheInstallDocsSayTheSignatureNeedsOpenSSL(t *testing.T) {
	root := repoRoot(t)
	installer := readFile(t, filepath.Join(root, "scripts/install.sh"))
	m := regexp.MustCompile(`(?m)^RELEASE_PUBLIC_KEY="([^"]*)"`).FindStringSubmatch(installer)
	if m == nil {
		t.Fatal("scripts/install.sh no longer sets RELEASE_PUBLIC_KEY; change this test with it")
	}
	if m[1] == "" {
		t.Skip("no release key is configured, so the installers check no signature")
	}

	for _, doc := range []string{"README.md", "docs/GUIDE.md", "docs/REFERENCE.md", "site/docs-install.html"} {
		body := readFile(t, filepath.Join(root, doc))
		for _, want := range []string{"OpenSSL 3", "SHA256SUMS.sig", "MOTITA_INSECURE_SKIP_VERIFY"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not mention %q, but the installer refuses to install without it", doc, want)
			}
		}
	}

	stale := map[string]string{
		"docs/REFERENCE.md":      "not a compromised release",
		"SECURITY.md":            "once one is configured",
		"site/docs-install.html": "nothing else to install first",
	}
	for doc, phrase := range stale {
		if strings.Contains(readFile(t, filepath.Join(root, doc)), phrase) {
			t.Errorf("%s still says %q, but a release key is configured and checked", doc, phrase)
		}
	}
}
