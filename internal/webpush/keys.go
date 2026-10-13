// Package webpush sends Web Push messages to the browsers that asked for them: a phone with the
// page installed hears that a CI ended even when the page is closed.
//
// It is the standard protocol, with no third party in between other than the browser's own push
// service: the message is encrypted for each browser (RFC 8291, aes128gcm) and the request is
// signed with the gateway's own VAPID key (RFC 8292). The key is created the first time and kept
// next to the subscriptions, so a browser that subscribed stays subscribed across restarts. The
// standard library is all it uses.
package webpush

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// b64 is the encoding the Push API speaks: base64url without padding.
var b64 = base64.RawURLEncoding

// Keys is the gateway's VAPID identity: the browser is handed the public half when it subscribes,
// and the push service refuses any message not signed by the private half.
type Keys struct {
	priv *ecdsa.PrivateKey
	// pub is the uncompressed P-256 point, the 65 bytes the browser expects.
	pub []byte
}

// Public is the key the page passes to PushManager.subscribe as applicationServerKey.
func (k *Keys) Public() string { return b64.EncodeToString(k.pub) }

// LoadKeys reads the key saved at path, and creates and saves one the first time.
func LoadKeys(path string) (*Keys, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return createKeys(path)
	}
	if err != nil {
		return nil, err
	}
	d, err := b64.DecodeString(strings.TrimSpace(string(data)))
	var e *ecdh.PrivateKey
	if err == nil {
		e, err = ecdh.P256().NewPrivateKey(d)
	}
	if err != nil {
		return nil, fmt.Errorf("the push key in %s is not valid: %w", path, err)
	}
	return keysOf(e), nil
}

// createKeys makes a new key and saves it, readable by its owner only.
func createKeys(path string) (*Keys, error) {
	// Since Go 1.26 the key always comes from the system's secure source and cannot fail.
	e, _ := ecdh.P256().GenerateKey(rand.Reader)
	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err == nil {
		err = os.WriteFile(path, []byte(b64.EncodeToString(e.Bytes())+"\n"), 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("could not save the push key: %w", err)
	}
	return keysOf(e), nil
}

// keysOf is the signing key for a valid P-256 scalar; the ecdh package already checked it, so the
// parse cannot fail.
func keysOf(e *ecdh.PrivateKey) *Keys {
	priv, _ := ecdsa.ParseRawPrivateKey(elliptic.P256(), e.Bytes())
	return &Keys{priv: priv, pub: e.PublicKey().Bytes()}
}
