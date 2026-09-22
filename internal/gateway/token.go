package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MinTokenBytes is the shortest token the gateway accepts, in bytes. 32 bytes of randomness is
// 256 bits, which is not guessable; the check on READ is what stops a hand-written "changeme"
// from being accepted as one.
const MinTokenBytes = 32

// randReader is the source of randomness, as a variable so the failure path of token
// generation is a TEST rather than a hypothetical. io.ReadFull on crypto/rand does not fail in
// practice, and a branch nothing can reach is a branch that rots. This is the same shape the
// repository already uses for the listener in tools/mockllm (listenAndServe).
var randReader io.Reader = rand.Reader

// The two filesystem calls that can fail while writing the token, as variables for the same
// reason randReader is one.
//
// The branches they guard are NOT reachable through the public function: a path under a file,
// or a path that IS a directory, is refused earlier by the READ above (os.ReadFile reports
// ENOTDIR and EISDIR, and neither is ErrNotExist, so the read branch answers first). Both are
// real failures worth reporting precisely, so they are made testable rather than deleted.
var (
	mkdirAll  = os.MkdirAll
	writeFile = os.WriteFile
)

// LoadOrCreateToken returns the bearer token stored at path, generating one the first time.
//
// It generates rather than asking, because a gateway that ships with a default token is a
// gateway with no token at all.
//
// It REPORTS a file it cannot use instead of replacing it. An unusable file is nearly always a
// path that points somewhere the operator did not mean, and silently writing a fresh token
// there is how a credential ends up in a place nobody looks - and how a client holding the old
// one starts getting 401s with no explanation.
func LoadOrCreateToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("no token file was configured")
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		tok := strings.TrimSpace(string(data))
		if len(tok) < 2*MinTokenBytes || !isHex(tok) {
			return "", fmt.Errorf("the token in %s is not usable: it must be at least %d hexadecimal characters",
				path, 2*MinTokenBytes)
		}
		return tok, nil
	case !os.IsNotExist(err):
		return "", fmt.Errorf("could not read the token file %s: %w", path, err)
	}

	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := mkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("could not create the token directory %s: %w", dir, err)
		}
	}
	// 0600: the owner reads it and nobody else. The file IS the credential, so it gets the
	// same treatment the onboarding wizard gives the API key file.
	if err := writeFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("could not write the token file %s: %w", path, err)
	}
	return tok, nil
}

// newToken returns MinTokenBytes random bytes as hexadecimal.
func newToken() (string, error) {
	b := make([]byte, MinTokenBytes)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", fmt.Errorf("could not generate a token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// isHex reports whether s is hexadecimal only. A token made of other characters is refused
// rather than trimmed: it is a configuration error, and guessing at the intent would let a file
// no client can match succeed on the server side.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
