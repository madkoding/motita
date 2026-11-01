package gateway

import (
	"net/http"
	"testing"
)

// directService is the fake that also takes the direct-mode choice, like the runner.
type directService struct {
	fakeService
	ons []bool
}

func (d *directService) SetDirect(on bool) { d.ons = append(d.ons, on) }

// Direct mode is off until it is turned on, needs no confirmation either way, is saved, reaches the
// open conversations and is handed to new ones.
func TestDirectModeSetting(t *testing.T) {
	withConfinement(t, false)
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	svc := &directService{}
	srv := newTestServer(t, svc, func(o *Options) { o.ConfigPath = path })
	if v := decodeSecurity(t, get(t, srv, "/v1/security", testToken)); v.Direct {
		t.Fatalf("GET = %+v, want off", v)
	}
	v := decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"direct":true}`))
	if !v.Direct || !mustLoad(t, path).Agent.Direct {
		t.Fatalf("PUT on = %+v", v)
	}
	if len(svc.ons) != 1 || !svc.ons[0] {
		t.Errorf("the open session takes the change: %v", svc.ons)
	}
	fresh := &directService{}
	srv.startingDiskAccess(fresh)
	if len(fresh.ons) != 1 || !fresh.ons[0] {
		t.Errorf("a new session starts with the saved choice: %v", fresh.ons)
	}
	v = decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"direct":false}`))
	if v.Direct || mustLoad(t, path).Agent.Direct {
		t.Fatalf("PUT off = %+v", v)
	}
}
