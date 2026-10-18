package gateway

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// desktopService is the fake that also takes the desktop choice, like the runner.
type desktopService struct {
	fakeService
	ons []bool
}

func (d *desktopService) SetDesktopControl(on bool) { d.ons = append(d.ons, on) }

// withDesktop makes this machine answer err when asked for its desktop.
func withDesktop(t *testing.T, err error) {
	t.Helper()
	old := desktopProbe
	t.Cleanup(func() { desktopProbe = old })
	desktopProbe = func() error { return err }
}

// Controlling the desktop is off until a confirmed change, which is saved and reaches the open
// conversations; turning it off needs nothing, and the page is told when there is no desktop.
func TestDesktopControlIsOffUntilAConfirmedChange(t *testing.T) {
	withConfinement(t, false)
	withDesktop(t, errors.New("no graphical session"))
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	svc := &desktopService{}
	srv := newTestServer(t, svc, func(o *Options) { o.ConfigPath = path })
	v := decodeSecurity(t, get(t, srv, "/v1/security", testToken))
	if v.DesktopControl || v.DesktopReason != "no graphical session" {
		t.Fatalf("GET = %+v, want off with the reason", v)
	}
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"desktop_control":true}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "control the desktop") {
		t.Fatalf("unconfirmed = %d %s, want 400", w.Code, w.Body.String())
	}
	if mustLoad(t, path).Agent.DesktopControl || len(svc.ons) != 0 {
		t.Fatal("a refused change reached the file or a session")
	}
	withDesktop(t, nil)
	v = decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"desktop_control":true,"confirm":true}`))
	if !v.DesktopControl || v.DesktopReason != "" || !mustLoad(t, path).Agent.DesktopControl {
		t.Fatalf("PUT on = %+v", v)
	}
	if len(svc.ons) != 1 || !svc.ons[0] {
		t.Errorf("the open session takes the change: %v", svc.ons)
	}
	v = decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"desktop_control":false}`))
	if v.DesktopControl || mustLoad(t, path).Agent.DesktopControl {
		t.Fatalf("turning it off needs no confirmation: %+v", v)
	}
}

// A desktop choice the file cannot take is refused whole.
func TestDesktopControlThatCannotBeWritten(t *testing.T) {
	withConfinement(t, false)
	t.Setenv("MOTITA_AGENT_DESKTOP_CONTROL", "false")
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	w := sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"desktop_control":true,"confirm":true}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("PUT = %d %s, want 500", w.Code, w.Body.String())
	}
}
