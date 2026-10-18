package peers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gatewayServer answers the hello and the token check like a gateway would.
func gatewayServer(t *testing.T, h Hello, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case HelloPath:
			_ = json.NewEncoder(w).Encode(h)
		case "/v1/sessions":
			switch r.Header.Get("Authorization") {
			case "Bearer " + token:
				w.WriteHeader(http.StatusOK)
			case "Bearer refused":
				w.WriteHeader(http.StatusForbidden)
			case "Bearer broken":
				w.WriteHeader(http.StatusInternalServerError)
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// redirected is a prober whose every connection goes to srv, whatever address is asked.
func redirected(srv *httptest.Server) *Prober {
	addr := strings.TrimPrefix(srv.URL, "http://")
	return &Prober{Client: &http.Client{Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}}
}

func TestHelloReadsTheGatewayAndWhereItAnswered(t *testing.T) {
	srv := gatewayServer(t, hello("peer0001", "desk"), "tok")
	h, at, err := NewProber().Hello(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if err != nil || h.ID != "peer0001" || !at.Addr().IsLoopback() {
		t.Fatalf("%+v %v %v", h, at, err)
	}
}

func TestHelloRefusesWhatIsNotAGateway(t *testing.T) {
	srv := gatewayServer(t, Hello{Protocol: "other"}, "tok")
	_, _, err := NewProber().Hello(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if err == nil || !strings.Contains(err.Error(), "not a motita gateway") {
		t.Fatalf("err = %v", err)
	}
}

func TestHelloReportsAnUnreachableOrPublicAddress(t *testing.T) {
	p := NewProber()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, _, err := p.Hello(context.Background(), addr); err == nil || !strings.Contains(err.Error(), "no motita answered") {
		t.Fatalf("closed port: %v", err)
	}
	if _, _, err := p.Hello(context.Background(), "8.8.8.8:7477"); !errors.Is(err, errOutside) {
		t.Fatalf("public: %v", err)
	}
	if _, _, err := p.Hello(context.Background(), "a b"); err == nil {
		t.Fatal("bad address accepted")
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
	srv := gatewayServer(t, hello("peer0001", "desk"), "tok")
	p, addr := NewProber(), strings.TrimPrefix(srv.URL, "http://")
	ctx := context.Background()
	if err := p.CheckToken(ctx, addr, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckToken(ctx, addr, "wrong"); !errors.Is(err, ErrToken) {
		t.Fatalf("wrong: %v", err)
	}
	if err := p.CheckToken(ctx, addr, "refused"); !errors.Is(err, ErrRefused) {
		t.Fatalf("refused: %v", err)
	}
	if err := p.CheckToken(ctx, addr, "broken"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("broken: %v", err)
	}
	if err := p.CheckToken(ctx, "8.8.8.8:1", "tok"); err == nil || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("public: %v", err)
	}
	if err := p.CheckToken(ctx, "a b", "tok"); err == nil {
		t.Fatal("bad address accepted")
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
