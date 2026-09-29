package gateway

// The two guards the happy paths cannot reach, and the one function whose whole
// job is deciding what counts as a confirmation.
//
// `?force=1` is the ONLY thing that authorises destroying uncommitted work, so
// the question "what counts as a confirmation" has to be answered by a test and
// not by reading the code: a spelling that slips through means work is destroyed
// by a request that did not mean to, and a spelling that is refused means a user
// who SAW the list cannot proceed.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestIsForcedOnlyAcceptsAConfirmation: the accepted spellings are the ones a
// client actually sends. Everything else - including the parameter being absent -
// is not a confirmation, so a truncated or malformed request can only ever be
// safe.
func TestIsForcedOnlyAcceptsAConfirmation(t *testing.T) {
	cases := map[string]bool{
		"1": true, "true": true, "yes": true,
		"TRUE": true, "Yes": true, " 1 ": true,
		"0": false, "false": false, "": false, "maybe": false, "no": false, "2": false,
	}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete,
				"/v1/sessions/s1?force="+url.QueryEscape(value), nil)
			if got := isForced(req); got != want {
				t.Errorf("isForced(force=%q) = %v, want %v", value, got, want)
			}
		})
	}
	// The parameter absent entirely: the ordinary DELETE a client sends when it has
	// nothing to confirm.
	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/s1", nil)
	if isForced(req) {
		t.Error("no force parameter at all is not a confirmation")
	}
}

// TestTheProjectPreviewNeedsAProjectDirectory: a gateway started without one has
// no projects and nothing to preview. It says so rather than answering with an
// empty list, which would read as "nothing to lose".
func TestTheProjectPreviewNeedsAProjectDirectory(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = ""
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	rec := send(t, srv, http.MethodGet, "/v1/projects/p1/deletion-preview", testToken, "")
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("a gateway without a project directory must answer 501, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// TestTheProjectPreviewOfAnUnknownProjectIsNotFound: a preview about a project
// that does not exist cannot describe anything, and pretending otherwise would
// offer a confirmation over nothing.
func TestTheProjectPreviewOfAnUnknownProjectIsNotFound(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	rec := send(t, srv, http.MethodGet, "/v1/projects/does-not-exist/deletion-preview", testToken, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown project must answer 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
