package gateway

import (
	"io"
	"strings"
	"testing"
)

// "Always allow this here", said from this computer's page about a command of another one, is kept
// by that one and honoured for the next command of the same shape.
func TestAlwaysAllowSaidThroughTheProxyIsKeptByTheOtherComputer(t *testing.T) {
	here := newTestServer(t, &fakeService{})
	svc := &askingShell{}
	there := newTestServer(t, svc, func(o *Options) { o.Token = otherToken })
	id := connect(t, here, there)
	path := "/v1/sessions/" + DefaultSession + "/shell"
	say := func(body string) string {
		b, _ := io.ReadAll(proxied(t, here, "POST", id, path, body).Body)
		return string(b)
	}
	if got := say(`{"command":"git push"}`); !strings.Contains(got, `"needs_approval":true`) {
		t.Fatalf("first: %s", got)
	}
	if got := say(`{"command":"git push","approved":true,"scope":"project"}`); !strings.Contains(got, "ran git push") {
		t.Fatalf("approved: %s", got)
	}
	if got := say(`{"command":"git push --tags"}`); !strings.Contains(got, "ran git push --tags") {
		t.Fatalf("the same shape must run without a yes: %s", got)
	}
}
