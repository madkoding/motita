package gateway

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// requireToken refuses every request that does not carry a valid credential.
//
// It is the second line of defence for the empty token as well: the check lives in authorized,
// which refuses everything when no token was configured. (Start refuses the empty token
// outright; this is the same defence, in the place every request passes through.)
func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized(token, r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="starlight"`)
			http.Error(w, "a valid bearer token is required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorized accepts either of two credentials, and nothing else.
//
// Both are compared in CONSTANT TIME. A byte-by-byte comparison leaks the length of the token by
// timing and its prefix by repetition, and this is the only thing between the network and an
// agent that runs commands on this machine.
//
// The second credential is the browser's cookie, whose value is derived from the token rather
// than being it (see webui.go). Accepting it does not open anything: a request with no
// credential, or with a cookie that was not derived from the CURRENT token, is refused exactly
// as it was before the cookie existed.
func authorized(token string, r *http.Request) bool {
	if token == "" {
		// Not "no authentication needed": a gateway that was started without a token serves
		// nobody. It matters here as much as on the bearer path, because a cookie derived from
		// an EMPTY token would otherwise match the empty token and let everyone in - which is
		// exactly the hole this check exists to close.
		return false
	}
	if bearerMatches(token, r.Header.Get("Authorization")) {
		return true
	}
	c, err := r.Cookie(webuiCookie)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(cookieValue(token))) == 1
}

// bearerMatches reports whether an Authorization header carries the configured token.
//
// Constant time, and an empty configured token matches NOTHING: a gateway that was started
// without a token serves nobody, and the safe reading of an empty secret is to trust no one.
func bearerMatches(token, header string) bool {
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(bearer(header)), []byte(token)) == 1
}

// requireBearer refuses everything that does not carry the bearer token, and deliberately does
// NOT accept the browser cookie.
//
// It guards the one endpoint whose entire purpose is to turn a token into a cookie. Requiring
// the token there keeps that contract single-entry: a browser that already holds a cookie has no
// business minting itself another one, and the endpoint stays useless to anything that does not
// know the token.
func requireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerMatches(token, r.Header.Get("Authorization")) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="starlight"`)
			http.Error(w, "a valid bearer token is required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearer extracts the token from an Authorization header, accepting only the Bearer scheme.
//
// The scheme is required rather than ignored: a header that carries a bare string is not what
// this gateway accepts, and silently accepting it would make a client that sends the wrong
// header work until the day it met a different server.
func bearer(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
