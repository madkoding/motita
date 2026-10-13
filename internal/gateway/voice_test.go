package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeVoice answers a fixed transcript, or a fixed error, and records what it was given.
type fakeVoice struct {
	text  string
	err   error
	got   []byte
	ctype string
}

func (f *fakeVoice) Transcribe(_ context.Context, audio []byte, contentType string) (string, error) {
	f.got, f.ctype = audio, contentType
	return f.text, f.err
}

func postAudio(t *testing.T, srv *Server, path, ctype string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	if ctype != "" {
		req.Header.Set(headerContentType, ctype)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// TestVoiceIsOffWithoutAService: the interface learns that speech input is off, and the
// transcription endpoint says so instead of failing somewhere else.
func TestVoiceIsOffWithoutAService(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, "/v1/voice", testToken)
	var v voiceView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", w.Code, w.Body.String())
	}
	if v.Enabled || v.WakePhrases == nil || len(v.WakePhrases) != 0 || v.MaxAudioBytes != defaultMaxAudioBytes {
		t.Errorf("view = %+v", v)
	}
	w = postAudio(t, srv, "/v1/voice/transcribe", "audio/webm", strings.NewReader("a"))
	if w.Code != http.StatusNotImplemented {
		t.Errorf("POST = %d", w.Code)
	}
	if w := get(t, srv, "/v1/voice", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the voice settings are behind the token: %d", w.Code)
	}
}

func TestTranscribeAnswersTheTextAndTheWakePhrase(t *testing.T) {
	fv := &fakeVoice{text: "Hey Motita, run the tests"}
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.Voice = fv
		o.WakePhrases = []string{"hey motita"}
		o.MaxAudioBytes = 16
	})
	var v voiceView
	_ = json.Unmarshal(get(t, srv, "/v1/voice", testToken).Body.Bytes(), &v)
	if !v.Enabled || len(v.WakePhrases) != 1 || v.MaxAudioBytes != 16 {
		t.Errorf("view = %+v", v)
	}

	w := postAudio(t, srv, "/v1/voice/transcribe?wake=1", "audio/webm;codecs=opus", strings.NewReader("AUDIO"))
	var got transcription
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", w.Code, w.Body.String())
	}
	if got.Text != fv.text || got.Wake == nil || got.Wake.Phrase != "hey motita" || got.Wake.Rest != "run the tests" {
		t.Errorf("answer = %+v", got)
	}
	if string(fv.got) != "AUDIO" || fv.ctype != "audio/webm;codecs=opus" {
		t.Errorf("the service got %q %q", fv.got, fv.ctype)
	}

	// Without ?wake=1 no phrase is looked for, and none is reported.
	w = postAudio(t, srv, "/v1/voice/transcribe", "audio/webm", strings.NewReader("AUDIO"))
	got = transcription{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Wake != nil || strings.Contains(w.Body.String(), "wake") {
		t.Errorf("no wake was asked for: %s", w.Body.String())
	}

	// Asked for, but the transcript does not open with a phrase.
	fv.text = "just talking"
	w = postAudio(t, srv, "/v1/voice/transcribe?wake=1", "audio/webm", strings.NewReader("AUDIO"))
	if strings.Contains(w.Body.String(), "wake") {
		t.Errorf("no phrase was said: %s", w.Body.String())
	}
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("the connection dropped") }

func TestTranscribeRefusals(t *testing.T) {
	fv := &fakeVoice{err: errors.New("the transcription endpoint answered 401")}
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.Voice = fv
		o.MaxAudioBytes = 4
	})
	cases := []struct {
		name, ctype string
		body        io.Reader
		status      int
	}{
		{"type", "text/plain", strings.NewReader("a"), http.StatusUnsupportedMediaType},
		{"too big", "audio/wav", strings.NewReader("12345"), http.StatusRequestEntityTooLarge},
		{"unreadable", "audio/wav", failingBody{}, http.StatusBadRequest},
		{"empty", "audio/wav", strings.NewReader(""), http.StatusBadRequest},
		{"upstream", "audio/wav", strings.NewReader("a"), http.StatusBadGateway},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if w := postAudio(t, srv, "/v1/voice/transcribe", c.ctype, c.body); w.Code != c.status {
				t.Errorf("status = %d, want %d: %s", w.Code, c.status, w.Body.String())
			}
		})
	}
}

// writeTestCert writes a self-signed pair for 127.0.0.1 and returns the paths and a pool that
// trusts it, so the test checks a real handshake instead of skipping verification.
func writeTestCert(t *testing.T) (certPath, keyPath string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "motita test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certPath, keyPath, pool
}

// TestTheGatewayAlsoServesHTTPS: the second listener answers the same API over TLS, while the
// plain one keeps working for the local clients.
func TestTheGatewayAlsoServesHTTPS(t *testing.T) {
	certPath, keyPath, pool := writeTestCert(t)
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.TLSListen, o.TLSCert, o.TLSKey = "127.0.0.1:0", certPath, keyPath
	})
	if srv.TLSAddr() == "" {
		t.Fatal("the https listener must be bound")
	}
	tlsConfig := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	resp, err := client.Get("https://" + srv.TLSAddr() + "/v1/health")
	if err != nil {
		t.Fatalf("https: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil {
		t.Errorf("https status = %d", resp.StatusCode)
	}
	if plain := newTestServer(t, &fakeService{}); plain.TLSAddr() != "" {
		t.Error("no https listener was asked for")
	}
}

func TestHTTPSStartFailures(t *testing.T) {
	certPath, keyPath, _ := writeTestCert(t)
	if _, err := Start(Options{Service: &fakeService{}, Token: testToken, Listen: "127.0.0.1:0",
		TLSListen: "127.0.0.1:0", TLSCert: certPath + ".missing", TLSKey: keyPath}); err == nil ||
		!strings.Contains(err.Error(), "could not be loaded") {
		t.Errorf("a missing certificate must stop the start: %v", err)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	if _, err := Start(Options{Service: &fakeService{}, Token: testToken, Listen: "127.0.0.1:0",
		TLSListen: busy.Addr().String(), TLSCert: certPath, TLSKey: keyPath}); err == nil ||
		!strings.Contains(err.Error(), "for https") {
		t.Errorf("a busy https address must stop the start: %v", err)
	}
}

// TestAStoppedHTTPSListenerIsLogged: a failure of the https socket is reported, and the plain one
// the local clients use stays up.
func TestAStoppedHTTPSListenerIsLogged(t *testing.T) {
	certPath, keyPath, _ := writeTestCert(t)
	srv, sink := startServer(t, Options{Token: testToken, Listen: "127.0.0.1:0",
		TLSListen: "127.0.0.1:0", TLSCert: certPath, TLSKey: keyPath})
	_ = srv.tlsListener.Close()
	go func() { _ = srv.Serve() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(sink.lines(t), "\n"), "the https listener stopped") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the stop was not logged: %v", sink.lines(t))
}
