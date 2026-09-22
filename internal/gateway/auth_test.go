package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestARequestWithoutTheTokenIsRefused(t *testing.T) {
	h := requireToken("the-token", okHandler())
	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"wrong scheme", "Basic the-token", http.StatusUnauthorized},
		{"bare token", "the-token", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"the token", "Bearer the-token", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}

// An empty configured token is not "open": it refuses everything. Serving on an empty token
// would make a misconfiguration into an exposure.
func TestAnEmptyTokenRefusesEverything(t *testing.T) {
	h := requireToken("", okHandler())
	for _, header := range []string{"", "Bearer ", "Bearer anything"} {
		r := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("header %q: status = %d, want 401", header, w.Code)
		}
	}
}

// A refusal tells the client how to authenticate. Without it a 401 is a wall with no door.
func TestARefusalNamesTheScheme(t *testing.T) {
	h := requireToken("the-token", okHandler())
	r := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="starlight"` {
		t.Errorf("WWW-Authenticate = %q", got)
	}
}

func TestTheAcceptedRequestReachesTheHandler(t *testing.T) {
	reached := false
	h := requireToken("the-token", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	r := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	r.Header.Set("Authorization", "Bearer the-token")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !reached {
		t.Error("the handler was not reached with a valid token")
	}
}
