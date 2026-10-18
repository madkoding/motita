package peers

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestSniffServesPlainAndTLSOnOnePort(t *testing.T) {
	raw, _ := net.Listen("tcp", "127.0.0.1:0")
	ln := Sniff(raw, ServerConfig(testCert))
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			_, _ = io.WriteString(w, "tls")
			return
		}
		_, _ = io.WriteString(w, "plain")
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	addr := ln.Addr().String()

	// A connection that sends nothing and closes is dropped without holding the others up.
	idle, _ := net.Dial("tcp", addr)
	_ = idle.Close()

	res, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != "plain" {
		t.Fatalf("plain = %q", body)
	}
	tr := Transport(testCert)
	req, _ := http.NewRequestWithContext(WithPin(context.Background(), EncodeCert(testCert.Certificate[0])),
		http.MethodGet, "https://"+addr+"/", nil)
	res, err = tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != "tls" {
		t.Fatalf("tls = %q", body)
	}
}

// scriptedListener hands out what accepts says, in order, then blocks.
type scriptedListener struct {
	net.Listener
	accepts chan func() (net.Conn, error)
}

func (s *scriptedListener) Accept() (net.Conn, error) { return (<-s.accepts)() }

func TestSniffSurvivesAFailedAcceptAndDropsWhatArrivesAfterClosing(t *testing.T) {
	old := acceptPause
	acceptPause = time.Millisecond
	t.Cleanup(func() { acceptPause = old })
	raw, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { _ = raw.Close() })
	script := &scriptedListener{Listener: raw, accepts: make(chan func() (net.Conn, error), 4)}
	here, there := net.Pipe()
	script.accepts <- func() (net.Conn, error) { return nil, errors.New("too many open files") }
	script.accepts <- func() (net.Conn, error) { return here, nil }
	script.accepts <- func() (net.Conn, error) { return nil, net.ErrClosed }
	ln := Sniff(script, &tls.Config{})
	if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("accept after closing: %v", err)
	}
	// The connection accepted before the close sends its first byte only now: it is closed.
	go func() { _, _ = there.Write([]byte("G")) }()
	buf := make([]byte, 1)
	_ = there.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := there.Read(buf); err == nil {
		t.Fatal("the late connection was kept")
	}
}

func TestFingerprintOfReadsOnlyACertificate(t *testing.T) {
	if FingerprintOf(EncodeCert(testCert.Certificate[0])) != Fingerprint(testCert.Certificate[0]) {
		t.Fatal("fingerprint of an encoded certificate")
	}
	if FingerprintOf("not base64!") != "" || FingerprintOf("aGVsbG8=") != "" {
		t.Fatal("a fingerprint of what is not a certificate")
	}
}
