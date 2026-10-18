package peers

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"time"
)

// THE IDENTITY OF A GATEWAY ON THE WIRE.
//
// Every gateway holds a self-signed certificate, made once and kept with its identity. Gateways
// speak to each other over TLS only, each presenting its own certificate, and each one knows the
// other by the SHA-256 FINGERPRINT of that certificate: there is no authority to vouch for a
// machine on a LAN, so the fingerprint seen when two computers are first connected is pinned, and
// a connection that later presents another one is refused before a token is ever sent on it.
//
// TLS arrives on the gateway's one port: the first byte of a TLS connection (a handshake record,
// 0x16) is never the first byte of an HTTP request, so Sniff hands each connection to the TLS or
// the plain server by it. The browser keeps the plain address it always used.

// handshakeRecord is the first byte of every TLS connection.
const handshakeRecord = 0x16

// sniffTimeout bounds the wait for a connection's first byte.
const sniffTimeout = 10 * time.Second

// ErrIdentity is a computer that presents another certificate than the one pinned for it.
var ErrIdentity = errors.New("the other computer presented a different identity than the one it was connected with: " +
	"if it was reinstalled, disconnect it and connect it again")

// Fingerprint is the hex SHA-256 of a certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// PairCode is the short code both computers show while one asks the other to let it in. It is made
// from both fingerprints, in either order, so a machine in the middle - which would present its
// own certificate to each side - makes the two screens show different codes.
func PairCode(a, b string) string {
	pair := []string{a, b}
	slices.Sort(pair)
	sum := sha256.Sum256([]byte(pair[0] + ":" + pair[1]))
	n := (uint32(sum[0])<<16 | uint32(sum[1])<<8 | uint32(sum[2])) % 1000000
	return fmt.Sprintf("%03d %03d", n/1000, n%1000)
}

// newCertificate makes a gateway's self-signed certificate and key, as PEM.
func newCertificate(id string) string {
	// Neither can fail: the key comes from crypto/rand on a curve the library supports, and the
	// template is complete.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "motita " + id},
		DNSNames:     []string{"motita-peer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// parseCertificate reads what newCertificate made.
func parseCertificate(pemData string) (tls.Certificate, bool) {
	c, err := tls.X509KeyPair([]byte(pemData), []byte(pemData))
	return c, err == nil
}

// ServerConfig is the TLS side of a gateway's port: its certificate, and the other gateway's
// asked for (not required: a browser has none) so a request can be tied to the computer sending it.
func ServerConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"http/1.1"},
	}
}

// pinKey carries the fingerprint a connection must present; seenKey a place to write the one it did.
type (
	pinKey  struct{}
	seenKey struct{}
)

// WithPin is ctx for a request to the computer whose certificate has fingerprint pin.
func WithPin(ctx context.Context, pin string) context.Context {
	return context.WithValue(ctx, pinKey{}, pin)
}

// withSeen is ctx that records the fingerprint the other end presented in *seen.
func withSeen(ctx context.Context, seen *string) context.Context {
	return context.WithValue(ctx, seenKey{}, seen)
}

// clientConfig is the TLS side of a request to another gateway: this one's certificate, and the
// other's checked against the pin in ctx when there is one. There is no authority to check it
// against (a LAN has none), so the certificate is not verified as a web browser would: what makes
// it trusted is its fingerprint, pinned the first time the person connected the two computers.
func clientConfig(ctx context.Context, cert tls.Certificate) *tls.Config {
	pin, pinned := ctx.Value(pinKey{}).(string)
	seen, _ := ctx.Value(seenKey{}).(*string)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"http/1.1"},
		// The chain is not verified because there is none: VerifyConnection checks the pin.
		InsecureSkipVerify: true, //nolint:gosec // identity is the pinned fingerprint, checked below
		VerifyConnection: func(cs tls.ConnectionState) error {
			fp := Fingerprint(cs.PeerCertificates[0].Raw)
			if seen != nil {
				*seen = fp
			}
			if pinned && fp != pin {
				return ErrIdentity
			}
			return nil
		},
	}
}

// dialTLS opens a guarded connection to address and shakes hands as clientConfig says.
func dialTLS(ctx context.Context, dialer *net.Dialer, cert tls.Certificate, address string) (net.Conn, error) {
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, clientConfig(ctx, cert))
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

// Sniff is ln serving plain HTTP and TLS on the same port: each connection goes to the TLS server
// when its first byte opens a handshake, and as it is otherwise.
func Sniff(ln net.Listener, cfg *tls.Config) net.Listener {
	s := &sniffer{Listener: ln, cfg: cfg, conns: make(chan net.Conn), done: make(chan struct{})}
	go s.loop()
	return s
}

type sniffer struct {
	net.Listener
	cfg   *tls.Config
	conns chan net.Conn
	done  chan struct{}
	err   error
}

// acceptPause is the wait after a failed accept that is not the listener closing (too many open
// files, say), so the loop does not spin.
var acceptPause = 50 * time.Millisecond

func (s *sniffer) loop() {
	for {
		c, err := s.Listener.Accept()
		if errors.Is(err, net.ErrClosed) {
			s.err = err
			close(s.done)
			return
		}
		if err != nil {
			time.Sleep(acceptPause)
			continue
		}
		// Each connection is read in its own goroutine: one that sends nothing holds no one else up.
		go s.sniff(c)
	}
}

func (s *sniffer) sniff(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(sniffTimeout))
	r := bufio.NewReader(c)
	first, err := r.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		_ = c.Close()
		return
	}
	var out net.Conn = &peeked{Conn: c, r: r}
	if first[0] == handshakeRecord {
		out = tls.Server(out, s.cfg)
	}
	select {
	case s.conns <- out:
	case <-s.done:
		_ = c.Close()
	}
}

// Accept is the next connection, plain or TLS.
func (s *sniffer) Accept() (net.Conn, error) {
	select {
	case c := <-s.conns:
		return c, nil
	case <-s.done:
		return nil, s.err
	}
}

// peeked is a connection whose first bytes were already read into r.
type peeked struct {
	net.Conn
	r *bufio.Reader
}

func (p *peeked) Read(b []byte) (int, error) { return p.r.Read(b) }
