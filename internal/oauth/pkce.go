package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// PKCE holds the verifier of an authorisation-code flow, its S256 challenge and
// the state that ties the callback to the request that started it.
type PKCE struct {
	Verifier  string
	Challenge string
	State     string
}

// RandReader is the source of random bytes for PKCE. It is exported so tests
// in other packages can inject a failing reader to cover the error path.
var RandReader = rand.Reader

// NewPKCE creates a random code verifier, its S256 challenge and a state.
func NewPKCE() (PKCE, error) {
	verifierBytes := make([]byte, 32)
	if _, err := io.ReadFull(RandReader, verifierBytes); err != nil {
		return PKCE{}, fmt.Errorf("could not generate PKCE verifier: %w", err)
	}
	stateBytes := make([]byte, 16)
	if _, err := io.ReadFull(RandReader, stateBytes); err != nil {
		return PKCE{}, fmt.Errorf("could not generate state: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	// The challenge is the hash of the verifier STRING as sent (RFC 7636 §4.2),
	// not of the random bytes behind it: a server recomputes it from what it
	// receives in code_verifier.
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		State:     base64.RawURLEncoding.EncodeToString(stateBytes),
	}, nil
}
