package gateway

import (
	"net/http"
	"sync"
	"testing"
)

// The merged mark is written by the end of a merge run (setMerged, under stateMu) while the
// handlers that refuse an integrated session read it from their own request goroutines. Every one
// of them reads it through isMerged now; run under -race, an unlocked read is reported here.
func TestTheMergedMarkIsReadUnderItsLock(t *testing.T) {
	routes := []struct{ path, body string }{
		{"/task", `{"task":"x"}`},
		{"/plan", `{"prompt":"x"}`},
		{"/run", `{"task":"x"}`},
		{"/pr", `{}`},
		{"/pr/fix", `{}`},
		{"/merge", `{}`},
		{"/continue", `{}`},
	}
	for _, rt := range routes {
		t.Run(rt.path, func(t *testing.T) {
			srv := newTestServer(t, &fakeService{}, func(o *Options) {
				o.NewService = func() (Service, error) { return &fakeService{}, nil }
			})
			a := newSessionFor(t, srv)
			c := srv.conversationOf(a.ID)
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.setMerged("abc123")
			}()
			post(t, srv, sessionPath(srv, a.ID, rt.path), rt.body, testToken)
			wg.Wait()
			waitForNoRun(t, srv)

			// Once the mark is set every one of them refuses the session.
			w := post(t, srv, sessionPath(srv, a.ID, rt.path), rt.body, testToken)
			if w.Code != http.StatusConflict {
				t.Fatalf("%s on an integrated session = %d %s, want 409", rt.path, w.Code, w.Body.String())
			}
		})
	}
}
