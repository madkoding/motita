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
			w.Header().Set("WWW-Authenticate", `Bearer realm="motita"`)
			http.Error(w, "a valid bearer token is required", http.StatusUnauthorized)
			return
		}
		// A request the COOKIE authorised may have been sent by another page: see crossSiteRefusal.
		// The bearer token cannot be attached by a page that does not know it, so a bearer client
		// (the CLI, a script) is not asked for anything more.
		if !bearerMatches(token, r.Header.Get("Authorization")) {
			if msg := crossSiteRefusal(r); msg != "" {
				writeError(w, http.StatusForbidden, msg)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// browserHeader is the header the web interface sends on every request. A page on another origin
// cannot add a custom header to a cross-origin request without a CORS preflight, and this server
// answers no preflight, so its presence proves the request was built by a same-origin script.
const browserHeader = "X-Motita"

// crossSiteRefusal says why a cookie-authorised request is refused, or "" when it may proceed.
//
// The cookie is SameSite=Strict, but "site" ignores the port: a page served from any other port of
// this host (a dev server in a cloned repository, say) is same-site, and its browser attaches the
// cookie. A plain form or a text/plain fetch from there needs no preflight, so without this check
// it could drive every state-changing endpoint, approvals included.
//
// Reads (GET, HEAD) are left alone - another origin cannot read the answer - except a WebSocket
// upgrade, which is not subject to CORS at all and is checked by its Origin.
func crossSiteRefusal(r *http.Request) string {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			return ""
		}
	default:
		if r.Header.Get(browserHeader) != "1" {
			return "a request authorised by the browser cookie must carry the " + browserHeader + " header"
		}
	}
	// A browser always names the origin of a cross-origin request; when it is present it must be
	// this server's own.
	if origin := r.Header.Get("Origin"); origin != "" && !strings.EqualFold(origin, requestOrigin(r)) {
		return "this request was sent by another origin (" + origin + ") and is refused"
	}
	return ""
}

// requestOrigin is the scheme://host the request was addressed to, as a browser would write it in
// an Origin header for a page served by this server.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// authorized accepts either of two credentials, and nothing else.
//
// Both are compared in CONSTANT TIME. A byte-by-byte comparison leaks the length of the token by
// timing and its prefix by repetition, and this is the only thing between the network and an
// agent that runs commands on this machine.
//
// The second credential is the browser's cookie, whose value is derived from the token rather
// than being it (see webui.go). It authorises the whole API just as the token does, which is why
// a request it authorises also passes crossSiteRefusal. A request with no credential, or with a
// cookie that was not derived from the CURRENT token, is refused.
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
			w.Header().Set("WWW-Authenticate", `Bearer realm="motita"`)
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
