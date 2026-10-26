package peers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The probe transports must never honour a proxy named by the environment. A
// peer lives on the local network, a VPN or the tailnet, and a proxy would both
// hide the address the guard checks and stand between the probe and the
// certificate it pins. These tests point the environment at a dead proxy and
// prove the probes still reach the gateway directly.
func TestProbesIgnoreTheProxyFromTheEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")

	g := gatewayServer(t, hello("peer0001", "desk"), "tok")
	p := NewProber(testCert)
	ctx := context.Background()

	// The hello is plain HTTP: it would go through HTTP_PROXY if the transport
	// honoured it.
	if h, _, err := p.Hello(ctx, g.addr()); err != nil || h.ID != "peer0001" {
		t.Fatalf("hello through a dead proxy: %+v %v", h, err)
	}

	// The answer is TLS: it would go through HTTPS_PROXY if the transport
	// honoured it.
	g.answer = AnswerBody{State: AskAllowed, Token: "granted"}
	if a, err := p.Answer(ctx, g.addr(), g.pin); err != nil || a.Token != "granted" {
		t.Fatalf("answer through a dead proxy: %+v %v", a, err)
	}
}

// Both transports leave Proxy nil on purpose: a peer is reached directly.
func TestProbeTransportsDoNotUseAProxy(t *testing.T) {
	for name, tr := range map[string]*http.Transport{
		"Transport":      Transport(testCert),
		"probeTransport": probeTransport(testCert),
	} {
		if tr.Proxy != nil {
			t.Fatalf("%s: Proxy is set; a peer must be reached directly", name)
		}
	}
}

// A peer that answers without TLS must be refused: the probe speaks TLS and a
// plain server cannot complete the handshake.
func TestAnswerRefusesAServerWithoutTLS(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()

	addr := plain.Listener.Addr().String()
	if _, err := NewProber(testCert).Answer(context.Background(), addr, "not-the-pin"); err == nil {
		t.Fatal("a plain HTTP server was accepted as a peer")
	}
}

// A TLS server whose certificate is not the pinned one must be refused with
// ErrIdentity, even though its handshake would otherwise succeed.
func TestAnswerRefusesAPeerWithAnotherCertificate(t *testing.T) {
	g := gatewayServer(t, hello("peer0001", "desk"), "tok")
	g.answer = AnswerBody{State: AskAllowed, Token: "granted"}
	if _, err := NewProber(testCert).Answer(context.Background(), g.addr(), "not-the-pin"); !errors.Is(err, ErrIdentity) {
		t.Fatalf("another certificate: %v", err)
	}
}
