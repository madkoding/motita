package llm

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"sync"
)

// getCertPool returns a CertPool that contains the CA certificates baked into
// this binary. It is lazy so tests that never make TLS calls are unaffected.
// The bundle is Mozilla's CA list – the same source as what curl and Go use as a
// fallback. A CGO_ENABLED=0 binary running in a minimal container would otherwise
// fail every TLS handshake with "certificate signed by unknown authority".
var lazyCertPool *x509.CertPool
var certOnce sync.Once

func getCertPool() *x509.CertPool {
	certOnce.Do(func() {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(embeddedCertPEM))
		lazyCertPool = pool
	})
	return lazyCertPool
}

//go:embed ca-bundle.crt
var embeddedCertPEM string

// TLSConfig returns a *tls.Config that verifies certificates using the CA bundle
// compiled into this binary. The returned config is safe to share.
func TLSConfig() *tls.Config {
	return &tls.Config{
		RootCAs:    getCertPool(),
		MinVersion: tls.VersionTLS12,
	}
}
