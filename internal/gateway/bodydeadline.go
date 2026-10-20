package gateway

import (
	"io"
	"net/http"
	"time"
)

// bodyIdleTimeout is how long a request body may go without a byte arriving. A variable so a test
// does not wait thirty seconds.
var bodyIdleTimeout = 30 * time.Second

// bodyDeadline bounds the read of a request body, and it wraps everything, ahead of the token check.
//
// Without it, a peer with no token declares a body and never sends it: net/http drains the unread
// body before it writes the 401, that read has no deadline, and the connection stays open for as
// long as the peer likes. Every read that brings bytes pushes the deadline on, so a slow upload that
// keeps moving is not cut off. The end of the body is left alone: there net/http clears the
// deadline itself and reads the connection in the background to notice a client that leaves, and a
// deadline set again would end that read and cancel the request context of a stream still running.
func bodyDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 {
			rc := http.NewResponseController(w)
			if rc.SetReadDeadline(time.Now().Add(bodyIdleTimeout)) == nil {
				// On a copy: net/http checks the type of ITS request's body to choose between
				// draining it and closing at once (a large body, an unanswered 100-continue).
				r = r.WithContext(r.Context())
				r.Body = &deadlineBody{ReadCloser: r.Body, rc: rc}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// deadlineBody moves the read deadline of its connection on as the body arrives: see bodyDeadline.
type deadlineBody struct {
	io.ReadCloser
	rc *http.ResponseController
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && err == nil {
		_ = b.rc.SetReadDeadline(time.Now().Add(bodyIdleTimeout))
	}
	return n, err
}
