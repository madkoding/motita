package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateTokenGeneratesOnceAndReuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")

	first, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(first) != 2*MinTokenBytes {
		t.Errorf("token = %q, want %d hex characters", first, 2*MinTokenBytes)
	}

	second, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second != first {
		t.Errorf("the token changed: %q then %q", first, second)
	}
}

func TestTheTokenFileIsWrittenForTheOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	if _, err := LoadOrCreateToken(path); err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600: a token another user can read is a token another user has", got)
	}
}

func TestABadTokenFileIsReportedNotReplaced(t *testing.T) {
	cases := map[string]string{
		"empty":    "\n",
		"blank":    "   \n",
		"short":    "abc123\n",
		"not hex":  strings.Repeat("z", 2*MinTokenBytes) + "\n",
		"nonsense": "correct horse battery staple\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gateway.token")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			if _, err := LoadOrCreateToken(path); err == nil {
				t.Fatal("a token file that cannot be used must be reported, not silently replaced")
			}
			// And it must SURVIVE: overwriting it would hide a path the operator did not
			// mean, and would invalidate every client holding the old token.
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}
			if string(after) != content {
				t.Errorf("the file was rewritten: %q", after)
			}
		})
	}
}

func TestNoTokenFileMeansNoToken(t *testing.T) {
	for _, path := range []string{"", "   "} {
		if _, err := LoadOrCreateToken(path); err == nil {
			t.Errorf("the path %q must be refused", path)
		}
	}
}

func TestAnUnreadableTokenFileIsReported(t *testing.T) {
	dir := t.TempDir()
	// A directory where the token file should be: the read fails with something that is
	// neither "missing" nor nil, which is the branch that must not be confused with
	// "generate a new one".
	if err := os.Mkdir(filepath.Join(dir, "gateway.token"), 0o700); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, err := LoadOrCreateToken(filepath.Join(dir, "gateway.token")); err == nil {
		t.Fatal("a read failure must be reported")
	}
}

func TestTheTokenDirectoryIsCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "gateway.token")
	if _, err := LoadOrCreateToken(path); err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the token must exist at %s: %v", path, err)
	}
}

// failingReader is a source of randomness that refuses, so the branch that reports it is a
// test and not a claim.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no randomness") }

func TestAFailingRandomSourceIsReported(t *testing.T) {
	original := randReader
	randReader = failingReader{}
	t.Cleanup(func() { randReader = original })

	if _, err := LoadOrCreateToken(filepath.Join(t.TempDir(), "gateway.token")); err == nil {
		t.Fatal("a failing source of randomness must be reported, never papered over")
	}
}

// A failing MkdirAll is reported. It is injected rather than provoked through the filesystem
// on purpose: a path whose parent is a FILE is refused by the READ above (ENOTDIR is not
// ErrNotExist), so the branch is unreachable from the outside and the injection is the only
// honest way to test it. See the note on mkdirAll in token.go.
func TestAFailingMkdirIsReported(t *testing.T) {
	original := mkdirAll
	mkdirAll = func(string, os.FileMode) error { return errors.New("permission denied") }
	t.Cleanup(func() { mkdirAll = original })

	path := filepath.Join(t.TempDir(), "state", "gateway.token")
	_, err := LoadOrCreateToken(path)
	if err == nil {
		t.Fatal("a directory that cannot be created must be reported")
	}
	if !strings.Contains(err.Error(), "token directory") {
		t.Errorf("err = %v, it must name the directory that failed", err)
	}
}

// And a failing write, for the same reason.
func TestAFailingWriteIsReported(t *testing.T) {
	original := writeFile
	writeFile = func(string, []byte, os.FileMode) error { return errors.New("no space left") }
	t.Cleanup(func() { writeFile = original })

	_, err := LoadOrCreateToken(filepath.Join(t.TempDir(), "gateway.token"))
	if err == nil {
		t.Fatal("a write failure must be reported")
	}
	if !strings.Contains(err.Error(), "token file") {
		t.Errorf("err = %v, it must name the file that failed", err)
	}
}

func TestIsHexRejectsEverythingElse(t *testing.T) {
	cases := map[string]bool{
		"":       false,
		"0123":   true,
		"aAbBfF": true,
		"0x12":   false,
		"12 34":  false,
		"12g4":   false,
	}
	for in, want := range cases {
		if got := isHex(in); got != want {
			t.Errorf("isHex(%q) = %v, want %v", in, got, want)
		}
	}
}

// A token of the minimum length in UPPERCASE hex is usable: refusing it would be refusing a
// token this program itself can generate, since hex.DecodeString is case-insensitive.
func TestAnUppercaseTokenIsAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	tok := strings.ToUpper(strings.Repeat("ab", MinTokenBytes))
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	got, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("an uppercase hex token must be accepted: %v", err)
	}
	if got != tok {
		t.Errorf("token = %q, want %q", got, tok)
	}
}

// Trailing whitespace is trimmed rather than rejected: a token file written by echo has a
// newline, and that is not a configuration error.
func TestSurroundingWhitespaceIsTrimmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	tok := strings.Repeat("ab", MinTokenBytes)
	if err := os.WriteFile(path, []byte("\n  "+tok+"  \n\n"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	got, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if got != tok {
		t.Errorf("token = %q, want %q", got, tok)
	}
}

// TestReadTokenDoesNotCreateOne: a client must READ a token, never mint one.
//
// LoadOrCreateToken is right for a server: it has to have a token before it can listen. For a
// client it is a silent failure - a missing token file would produce a freshly generated one, a
// saved credential in a file the operator never asked for, and a 401 on every request with nothing
// to explain why.
func TestReadTokenDoesNotCreateOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")

	_, err := ReadToken(path)
	if err == nil {
		t.Fatal("reading a token that does not exist must be reported, not invented")
	}
	if !strings.Contains(err.Error(), "no token file") {
		t.Errorf("the failure must say what is missing, got: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("ReadToken created a file: a client must never write a credential")
	}
}

// TestReadTokenAcceptsAUsableFile: the happy path, and the trailing newline the file carries is
// not a reason to fail.
func TestReadTokenAcceptsAUsableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	if err := os.WriteFile(path, []byte("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadToken(path)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789" {
		t.Errorf("the token came back as %q, the newline must be trimmed", got)
	}
}

// TestReadTokenRejectsAnUnusableFile: a file that is not a token must be reported rather than used.
// A too-short token is the shape a truncated or half-written file has.
func TestReadTokenRejectsAnUnusableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	if err := os.WriteFile(path, []byte("tooshort\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadToken(path); err == nil {
		t.Fatal("a token shorter than MinTokenBytes must be refused")
	}
}

// TestReadTokenRefusesNonHex: right LENGTH but wrong SHAPE, which is the case a length check alone
// misses - and the one where a client would go on to present garbage and be refused everywhere.
func TestReadTokenRefusesNonHex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	body := strings.Repeat("z", 2*MinTokenBytes) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadToken(path); err == nil {
		t.Fatal("a token that is not hexadecimal must be refused")
	}
}

// TestReadTokenWithNoPathConfigured: the operator configured nothing, and the message says that
// rather than blaming a file.
func TestReadTokenWithNoPathConfigured(t *testing.T) {
	if _, err := ReadToken("  "); err == nil {
		t.Fatal("an empty path must be reported")
	}
}

// TestReadTokenReportsAFileItCannotOpen: a path that exists but cannot be read is neither
// "missing" nor "usable", and reporting it as missing would send the operator looking for a file
// that is right there.
func TestReadTokenReportsAFileItCannotOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	// A DIRECTORY where the token file should be: the read fails with something that is neither
	// "does not exist" nor nil, and the two must not be confused.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_, err := ReadToken(path)
	if err == nil {
		t.Fatal("a read failure must be reported")
	}
	if !strings.Contains(err.Error(), "could not read") {
		t.Errorf("the failure must say it could not be read, got: %v", err)
	}
}
