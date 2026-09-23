package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// webuiCookie is the name of the credential a browser holds.
const webuiCookie = "starlight_webui"

// webuiLabel domain-separates this derivation from any other use of the token, so a value
// derived for one purpose can never be replayed as one derived for another.
const webuiLabel = "starlight-webui-session-v1"

// cookieValue derives the browser's credential from the gateway token.
//
// Why a derivation and not the token itself:
//   - what the browser holds must NOT be a reusable bearer token against the API;
//   - nothing has to be stored: the expected value is recomputed per request, so there is no
//     session table, no id to guess and no expiry to sweep;
//   - rotating the token invalidates every cookie issued under the old one, by itself.
//
// HMAC rather than a bare hash: a hash of a short token can be attacked offline, and an HMAC
// cannot without the key - which here is the token.
func cookieValue(token string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(webuiLabel))
	return hex.EncodeToString(m.Sum(nil))
}
