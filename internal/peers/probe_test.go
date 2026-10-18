package peers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// testCert is the certificate the test probers present.
var testCert, _ = parseCertificate(newCertificate("tester01"))

// fakeGateway answers like a gateway would: the hello, the token check and the pairing.
type fakeGateway struct {
	*httptest.Server
	cert tls.Certificate
	// pin is its certificate as it announces it.
	pin string

	mu sync.Mutex
	// asked is the last request to be let in and the fingerprint it came with.
	asked     AskBody
	askedCert string
	// answer is what GET PairPath says; status overrides 200 when set.
	answer AnswerBody
	status int
}

// addr is where the fake listens.
func (g *fakeGateway) addr() string { return g.Listener.Addr().String() }

// gatewayServer starts a gateway that says h (with its own certificate, unless h names one) in
// plain HTTP and answers the rest over TLS, and accepts token.
func gatewayServer(t *testing.T, h Hello, token string) *fakeGateway {
	t.Helper()
	g := &fakeGateway{}
	g.cert, _ = parseCertificate(newCertificate("gateway1"))
	g.pin = EncodeCert(g.cert.Certificate[0])
	if h.Cert == "" {
		h.Cert = g.pin
	}
	g.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		case r.URL.Path == HelloPath:
			_ = json.NewEncoder(w).Encode(h)
		case r.URL.Path == "/v1/sessions":
			sessionsAnswer(w, r.Header.Get("Authorization"), token)
		case r.URL.Path == PairPath && r.Method == http.MethodPost:
			g.ask(w, r)
		case r.URL.Path == PairPath:
			if g.status != 0 {
				w.WriteHeader(g.status)
				_, _ = w.Write([]byte(`garbage`))
				return
			}
			_ = json.NewEncoder(w).Encode(g.answer)
		default:
			http.NotFound(w, r)
		}
	}))
	g.Listener = Sniff(g.Listener, ServerConfig(g.cert))
	g.Start()
	t.Cleanup(g.Close)
	return g
}

// sessionsAnswer is the list of sessions for the right token, and each refusal for the others.
func sessionsAnswer(w http.ResponseWriter, auth, token string) {
	switch auth {
	case "Bearer " + token:
		_, _ = w.Write([]byte(`{"sessions":[{"running":true},{"running":false},{"running":true}]}`))
	case "Bearer garbled":
		_, _ = w.Write([]byte(`not json`))
	case "Bearer refused":
		w.WriteHeader(http.StatusForbidden)
	case "Bearer broken":
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusUnauthorized)
	}
}

// ask records a request to be let in, and answers it by the name it carries.
func (g *fakeGateway) ask(w http.ResponseWriter, r *http.Request) {
	_ = json.NewDecoder(r.Body).Decode(&g.asked)
	g.askedCert = Fingerprint(r.TLS.PeerCertificates[0].Raw)
	switch g.asked.Name {
	case "busy":
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"too many"}`))
	case "silent":
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

// redirected is a prober whose every connection goes to g, whatever address is asked.
func redirected(g *fakeGateway) *Prober {
	addr := g.addr()
	return &Prober{Client: &http.Client{Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialTLS(ctx, &net.Dialer{}, testCert, addr)
		},
	}}}
}

func TestHelloReadsTheGatewayAndWhereItAnswered(t *testing.T) {
	g := gatewayServer(t, hello("peer0001", "desk"), "tok")
	h, at, err := NewProber(testCert).Hello(context.Background(), g.addr())
	if err != nil || h.ID != "peer0001" || h.Cert != g.pin || !at.Addr().IsLoopback() {
		t.Fatalf("%+v %v %v", h, at, err)
	}
}

func TestHelloRefusesWhatIsNotAGatewayOrSaysNoCertificate(t *testing.T) {
	forged := hello("peer0001", "x")
	forged.Cert = "forged"
	for _, h := range []Hello{{Protocol: "other"}, forged} {
		g := gatewayServer(t, h, "tok")
		_, _, err := NewProber(testCert).Hello(context.Background(), g.addr())
		if err == nil || !strings.Contains(err.Error(), "not a motita gateway") {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestHelloReportsAnUnreachableOrPublicAddress(t *testing.T) {
	p := NewProber(testCert)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	_, _, err := p.Hello(context.Background(), addr)
	if err == nil || !strings.Contains(err.Error(), "no gateway answers the network") {
		t.Fatalf("closed port: %v", err)
	}
	// Nothing answering in time reads as what to check over there.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now())
	defer cancel()
	if _, _, err := p.Hello(ctx, "127.0.0.1:7477"); err == nil || !strings.Contains(err.Error(), "ufw allow 7477/tcp") {
		t.Fatalf("timeout: %v", err)
	}
	if _, _, err := p.Hello(context.Background(), "8.8.8.8:7477"); !errors.Is(err, errOutside) {
		t.Fatalf("public: %v", err)
	}
	if _, _, err := p.Hello(context.Background(), "a b"); err == nil || !strings.Contains(err.Error(), "no motita answered") {
		t.Fatal("bad address accepted")
	}
}

func TestAPlainHTTPServerIsNoGateway(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	if _, _, err := NewProber(testCert).Hello(context.Background(), srv.Listener.Addr().String()); err == nil {
		t.Fatal("a plain server answered")
	}
	pin := EncodeCert(testCert.Certificate[0])
	err := NewProber(testCert).CheckToken(context.Background(), srv.Listener.Addr().String(), pin, "tok")
	if err == nil || errors.Is(err, ErrIdentity) {
		t.Fatalf("TLS to a plain server: %v", err)
	}
}

func TestGuardRefusesWhatIsNotAnAddress(t *testing.T) {
	if guard("tcp", "nonsense", nil) == nil {
		t.Fatal("accepted")
	}
	if guard("tcp", "192.168.1.1:80", nil) != nil {
		t.Fatal("refused a LAN address")
	}
}

func TestCheckTokenTellsTheAnswersApart(t *testing.T) {
	g := gatewayServer(t, hello("peer0001", "desk"), "tok")
	p, addr := NewProber(testCert), g.addr()
	ctx := context.Background()
	if err := p.CheckToken(ctx, addr, g.pin, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckToken(ctx, addr, "another", "tok"); !errors.Is(err, ErrIdentity) {
		t.Fatalf("another certificate: %v", err)
	}
	if err := p.CheckToken(ctx, addr, g.pin, "wrong"); !errors.Is(err, ErrToken) {
		t.Fatalf("wrong: %v", err)
	}
	if err := p.CheckToken(ctx, addr, g.pin, "refused"); !errors.Is(err, ErrRefused) {
		t.Fatalf("refused: %v", err)
	}
	if err := p.CheckToken(ctx, addr, g.pin, "broken"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("broken: %v", err)
	}
	err := p.CheckToken(ctx, "8.8.8.8:1", g.pin, "tok")
	if err == nil || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("public: %v", err)
	}
	if err := p.CheckToken(ctx, "a b", g.pin, "tok"); err == nil {
		t.Fatal("bad address accepted")
	}
}

func TestRunningCountsTheRunsInFlight(t *testing.T) {
	g := gatewayServer(t, hello("peer0001", "desk"), "tok")
	p := NewProber(testCert)
	ctx := context.Background()
	if n, err := p.Running(ctx, Target{Address: g.addr(), Token: "tok", Cert: g.pin}); err != nil || n != 2 {
		t.Fatalf("n = %d, %v", n, err)
	}
	if _, err := p.Running(ctx, Target{Address: g.addr(), Token: "wrong", Cert: g.pin}); !errors.Is(err, ErrToken) {
		t.Fatalf("wrong: %v", err)
	}
	if _, err := p.Running(ctx, Target{Address: g.addr(), Token: "garbled", Cert: g.pin}); err == nil {
		t.Fatal("garbled list read")
	}
}

func TestAskSendsWhoIsAskingWithItsCertificate(t *testing.T) {
	g := gatewayServer(t, hello("peer0001", "desk"), "tok")
	p := NewProber(testCert)
	ctx := context.Background()
	if err := p.Ask(ctx, g.addr(), g.pin, AskBody{ID: "self0001", Name: "laptop"}); err != nil {
		t.Fatal(err)
	}
	if g.asked.ID != "self0001" || g.askedCert != Fingerprint(testCert.Certificate[0]) {
		t.Fatalf("asked %+v with %q", g.asked, g.askedCert)
	}
	if err := p.Ask(ctx, g.addr(), g.pin, AskBody{Name: "busy"}); err == nil || err.Error() != "too many" {
		t.Fatalf("busy: %v", err)
	}
	if err := p.Ask(ctx, g.addr(), g.pin, AskBody{Name: "silent"}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("silent: %v", err)
	}
	if err := p.Ask(ctx, g.addr(), "another", AskBody{}); err == nil {
		t.Fatal("asked a computer with another certificate")
	}
}

func TestAnswerReadsTheOtherComputersDecision(t *testing.T) {
	g := gatewayServer(t, hello("peer0001", "desk"), "tok")
	p := NewProber(testCert)
	ctx := context.Background()
	g.answer = AnswerBody{State: AskAllowed, Token: "granted"}
	if a, err := p.Answer(ctx, g.addr(), g.pin); err != nil || a.Token != "granted" {
		t.Fatalf("%+v %v", a, err)
	}
	g.status = http.StatusNotFound
	if _, err := p.Answer(ctx, g.addr(), g.pin); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("unknown: %v", err)
	}
	g.status = http.StatusOK
	if _, err := p.Answer(ctx, g.addr(), g.pin); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("garbage: %v", err)
	}
	if _, err := p.Answer(ctx, "8.8.8.8:1", g.pin); err == nil {
		t.Fatal("public answered")
	}
}

func TestWithPortCompletesABareAddress(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.2":      "192.168.1.2:7477",
		" 192.168.1.2:80 ": "192.168.1.2:80",
		"fd00::1":          "[fd00::1]:7477",
		"[fd00::1]":        "[fd00::1]:7477",
		"[fd00::1]:9":      "[fd00::1]:9",
		"box.local":        "box.local:7477",
	} {
		if got := WithPort(in, 7477); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestTheProxyTransportIsDirectGuardedAndPinned(t *testing.T) {
	tr := Transport(testCert)
	if tr.Proxy != nil {
		t.Fatal("a proxy from the environment would be used")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://8.8.8.8:7477/", nil)
	if _, err := tr.RoundTrip(req); !errors.Is(err, errOutside) {
		t.Fatalf("public: %v", err)
	}
	g := gatewayServer(t, hello("peer0001", "desk"), "tok")
	url := "https://" + g.addr() + HelloPath
	req, _ = http.NewRequestWithContext(WithPin(context.Background(), g.pin), http.MethodGet, url, nil)
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequestWithContext(WithPin(context.Background(), "another"), http.MethodGet, url, nil)
	tr.CloseIdleConnections()
	if _, err := tr.RoundTrip(req); !errors.Is(err, ErrIdentity) {
		t.Fatalf("another certificate: %v", err)
	}
}
