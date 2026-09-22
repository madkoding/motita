package gateway

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// requireToken refuses every request that does not carry the bearer token.
//
// The comparison is CONSTANT-TIME. A byte-by-byte comparison leaks the length of the token by
// timing and its prefix by repetition, and this token is the only thing between the network and
// an agent that runs commands on this machine.
//
// An EMPTY configured token refuses everything. It is not "no authentication needed", it is a
// gateway that was started without a token, and the safe reading of that is to serve nobody.
// (Start refuses the empty token outright; this is the second line of the same defence.)
func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := bearer(r.Header.Get("Authorization"))
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
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
