package peers

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProbeTimeout bounds one hello: a machine that does not answer this fast is not on this network.
const ProbeTimeout = 3 * time.Second

// errOutside is an address outside the private networks.
var errOutside = errors.New("only computers on the local network, a VPN or the tailnet can be reached")

// guard refuses a connection to an address outside the private networks. It runs on the address
// actually dialled, after any name was resolved, so a name that resolves to the internet - or is
// re-pointed there between two requests - is refused all the same.
func guard(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !Allowed(ap.Addr()) {
		return errOutside
	}
	return nil
}

// Transport is the HTTP transport every request to another gateway goes through: direct (never
// through a proxy from the environment, which would be outside the network), guarded, and over TLS
// with this gateway's certificate, checking the other's against the pin in the request's context
// (see WithPin).
func Transport(cert tls.Certificate) *http.Transport {
	dialer := &net.Dialer{Timeout: ProbeTimeout, Control: guard}
	return &http.Transport{
		Proxy: nil,
		DialTLSContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialTLS(ctx, dialer, cert, address)
		},
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
}

// probeURL is the base of every probe. The address asked is NOT part of the URL: it travels in the
// request's context to the dialer (see probeTransport), so what a person typed never becomes a URL
// a request is sent to, and the guard still checks the address actually connected to.
const probeURL = "https://motita-peer"

// targetKey carries the address a probe dials in its context.
type targetKey struct{}

// probeTransport dials the address in the request's context, guarded and over TLS like Transport.
// It keeps no connection: every probe names its own address under the same URL, and a pooled
// connection would answer for another one.
func probeTransport(cert tls.Certificate) *http.Transport {
	dialer := &net.Dialer{Timeout: ProbeTimeout, Control: guard}
	return &http.Transport{
		Proxy: nil,
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			address, _ := ctx.Value(targetKey{}).(string)
			return dialTLS(ctx, dialer, cert, address)
		},
		DisableKeepAlives: true,
	}
}

// Prober asks other gateways who they are, and speaks to them before they are connected.
type Prober struct {
	Client *http.Client
}

// NewProber is a prober over probeTransport, presenting cert.
func NewProber(cert tls.Certificate) *Prober {
	return &Prober{Client: &http.Client{Transport: probeTransport(cert), Timeout: ProbeTimeout}}
}

// request is a probe of path at address. The Host header names the address, as a browser
// reaching it would.
func request(ctx context.Context, method, address, path string, body []byte) *http.Request {
	// A constant URL cannot fail to parse.
	req, _ := http.NewRequestWithContext(context.WithValue(ctx, targetKey{}, address), method, probeURL+path,
		bytes.NewReader(body))
	req.Host = address
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// WithPort completes an address typed without a port with this gateway's own, which is the port
// every gateway listens on unless its owner changed it. A bare IPv6 address is bracketed.
func WithPort(address string, port int) string {
	address = strings.TrimSpace(address)
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.Trim(address, "[]"), strconv.Itoa(port))
}

// Hello asks the gateway at address who it is, and reports the address it actually answered at.
// The certificate it claims is the one its connection presented, or it is refused.
func (p *Prober) Hello(ctx context.Context, address string) (Hello, netip.AddrPort, error) {
	var remote netip.AddrPort
	var seen string
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		remote, _ = netip.ParseAddrPort(info.Conn.RemoteAddr().String())
	}}
	ctx = withSeen(httptrace.WithClientTrace(ctx, trace), &seen)
	res, err := p.Client.Do(request(ctx, http.MethodGet, address, HelloPath, nil))
	if err != nil {
		if errors.Is(err, errOutside) {
			return Hello{}, remote, errOutside
		}
		return Hello{}, remote, fmt.Errorf("no motita answered at %s: %w", address, err)
	}
	defer res.Body.Close()
	var h Hello
	if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&h) != nil || !h.Valid() || h.Cert != seen {
		return Hello{}, remote, fmt.Errorf("what answered at %s is not a motita gateway, or one too old to connect to", address)
	}
	return h, netip.AddrPortFrom(remote.Addr().Unmap(), remote.Port()), nil
}

// ErrToken is a token the other gateway refused.
var ErrToken = errors.New("the other computer did not accept that token")

// ErrRefused is a gateway whose network rules do not let this one in.
var ErrRefused = errors.New("the other computer refuses connections from this one: " +
	"in its Settings, Network, let the local network in")

// sessionsPath is the other computer's list of sessions: a token is checked, and its runs counted,
// by reading it.
const sessionsPath = "/v1/sessions"

// sessions reads the list of sessions of the gateway at address, which must present pin, with
// token.
func (p *Prober) sessions(ctx context.Context, address, pin, token string) (*http.Response, error) {
	req := request(WithPin(ctx, pin), http.MethodGet, address, sessionsPath, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := p.Client.Do(req)
	if err != nil {
		if errors.Is(err, ErrIdentity) {
			return nil, ErrIdentity
		}
		return nil, fmt.Errorf("the other computer could not be reached at %s: %w", address, err)
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		err = ErrToken
	case res.StatusCode == http.StatusForbidden:
		err = ErrRefused
	case res.StatusCode != http.StatusOK:
		err = fmt.Errorf("the other computer answered %s", res.Status)
	}
	if err != nil {
		_ = res.Body.Close()
		return nil, err
	}
	return res, nil
}

// CheckToken reports whether the gateway at address, presenting pin, accepts token.
func (p *Prober) CheckToken(ctx context.Context, address, pin, token string) error {
	res, err := p.sessions(ctx, address, pin, token)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

// Running counts the sessions of a connected computer with a run in flight.
func (p *Prober) Running(ctx context.Context, t Target) (int, error) {
	res, err := p.sessions(ctx, t.Address, t.Cert, t.Token)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	var list struct {
		Sessions []struct {
			Running bool `json:"running"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&list); err != nil {
		return 0, fmt.Errorf("the other computer's sessions could not be read: %w", err)
	}
	n := 0
	for _, s := range list.Sessions {
		if s.Running {
			n++
		}
	}
	return n, nil
}

// PairPath is where a gateway asks another to let it in (POST) and reads the answer (GET). The
// connection's certificate says who is asking.
const PairPath = "/v1/peer/pair"

// AskBody is what a gateway sends when it asks to be let in.
type AskBody struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AnswerBody is the answer to a request to be let in.
type AnswerBody struct {
	State string `json:"state"`
	Token string `json:"token,omitempty"`
}

// Ask asks the gateway at address, presenting pin, to let this one (self) in.
func (p *Prober) Ask(ctx context.Context, address, pin string, self AskBody) error {
	body, _ := json.Marshal(self)
	res, err := p.Client.Do(request(WithPin(ctx, pin), http.MethodPost, address, PairPath, body))
	if err != nil {
		return fmt.Errorf("the other computer could not be reached at %s: %w", address, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		return answerError(res)
	}
	return nil
}

// Answer reads the answer to this gateway's request from the gateway at address.
func (p *Prober) Answer(ctx context.Context, address, pin string) (AnswerBody, error) {
	res, err := p.Client.Do(request(WithPin(ctx, pin), http.MethodGet, address, PairPath, nil))
	if err != nil {
		return AnswerBody{}, fmt.Errorf("the other computer could not be reached at %s: %w", address, err)
	}
	defer res.Body.Close()
	var a AnswerBody
	if res.StatusCode != http.StatusOK {
		return a, answerError(res)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&a); err != nil {
		return a, fmt.Errorf("the other computer's answer could not be read: %w", err)
	}
	return a, nil
}

// answerError is the other computer's refusal, in its own words when it gave them.
func answerError(res *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body)
	if body.Error != "" {
		return errors.New(body.Error)
	}
	return fmt.Errorf("the other computer answered %s", res.Status)
}
