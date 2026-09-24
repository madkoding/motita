package gateway

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

// A wildcard bind must not be handed to a client as an address to CALL.
//
// Go binds "0.0.0.0" as the dual-stack wildcard "[::]", and that is what the listener reports. The
// string is an address to LISTEN on, not one to dial: put in a URL it names no reachable host, and
// a real browser refuses it. Measured rather than assumed — a page on a wildcard bind loads over
// 127.0.0.1, over localhost and over the machine's LAN address, and returns an empty document over
// [::]. So the announced link, the service file, and everything a client discovered from them were
// all advertising an address that could not be used.
func TestAWildcardBindIsAnnouncedAsAnAddressToCall(t *testing.T) {
	// A wildcard bind needs NOTHING else to be allowed: there is no second act any more, and this
	// call is the proof. The address the socket listens on is still normalised for the client (see
	// Addr), which is what this test is about; how far it reaches is a separate answer.
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Listen = "0.0.0.0:0" })

	addr := srv.Addr()
	if strings.HasPrefix(addr, "[::]") || strings.HasPrefix(addr, "0.0.0.0") {
		t.Fatalf("Addr() is %q: it reports the address the socket LISTENS on, which no client can call", addr)
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatalf("Addr() is %q, which is not host:port: %v", addr, err)
	}

	// The proof that matters is not the string's shape but that it WORKS: the gateway answers on
	// the address it announces. This is the assertion the old behaviour failed.
	resp, err := http.Get(srv.BaseURL() + "/v1/health")
	if err != nil {
		t.Fatalf("the announced address %q cannot be reached: %v", srv.BaseURL(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the announced address answered %d, want 200", resp.StatusCode)
	}

	// A caller can also tell that this gateway is reachable from further away, which is a different
	// question from the address and has a different answer.
	if !srv.ReachableFromNetwork() {
		t.Fatal("a wildcard bind must report itself as reachable from the network")
	}
}

// A loopback bind keeps reporting exactly what it bound, and must NOT claim to be reachable from
// anywhere else: over-reporting exposure is the failure that gets a port opened for no reason.
func TestALoopbackBindAnnouncesItselfAndDeniesNetworkReach(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Listen = "127.0.0.1:0" })

	if !strings.HasPrefix(srv.Addr(), "127.0.0.1:") {
		t.Fatalf("Addr() is %q, want the loopback address that was bound", srv.Addr())
	}
	if srv.ReachableFromNetwork() {
		t.Fatal("a loopback bind claimed to be reachable from the network")
	}
	resp, err := http.Get(srv.BaseURL() + "/v1/health")
	if err != nil {
		t.Fatalf("the announced address %q cannot be reached: %v", srv.BaseURL(), err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the announced address answered %d, want 200", resp.StatusCode)
	}
}

// An ephemeral port asked for as a wildcard still reports the REAL port, because that is what made
// the address usable in the first place: the host is normalised, the port is the one the kernel
// chose and is never normalised away.
func TestAWildcardEphemeralBindStillReportsTheRealPort(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Listen = "0.0.0.0:0" })

	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatalf("Addr() is %q: %v", srv.Addr(), err)
	}
	if port == "" || port == "0" {
		t.Fatalf("Addr() is %q: the port the kernel chose must be reported", srv.Addr())
	}
	// And it is a port, not a fragment of something else.
	if strings.ContainsAny(port, "[]:") {
		t.Fatalf("Addr() is %q: the port half is not a port", srv.Addr())
	}
}

// The defensive branches: a listener whose address is not host:port is reported and not guessed at.
//
// A TCP listener always reports host:port, so this is unreachable through the real bind — and that
// is exactly why it is tested directly rather than left as a branch nothing ever runs. The address
// is injected, so the behaviour is asserted instead of being assumed to be harmless.
func TestAnUnparseableListenerAddressIsReportedVerbatim(t *testing.T) {
	// Built directly rather than through Start: the point is a listener whose address cannot be
	// split, and swapping the field under a running Serve() is a data race on the very value being
	// read. Only Addr() and ReachableFromNetwork() consult the listener here.
	srv := &Server{listener: &bogusListener{addr: fakeAddr("not-an-address")}}

	if got := srv.Addr(); got != "not-an-address" {
		t.Fatalf("Addr() is %q, want the listener's string verbatim when it cannot be split", got)
	}
	// Unknown shape is treated as NOT exposed: the safe direction, because claiming exposure that
	// is not there gets a firewall opened for nothing, while the reverse is caught by the bind
	// guard in Start.
	if srv.ReachableFromNetwork() {
		t.Fatal("an address that cannot be parsed must not be reported as reachable from the network")
	}
}

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

// bogusListener satisfies net.Listener without listening on anything.
type bogusListener struct{ addr net.Addr }

func (l *bogusListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *bogusListener) Close() error              { return nil }
func (l *bogusListener) Addr() net.Addr            { return l.addr }
