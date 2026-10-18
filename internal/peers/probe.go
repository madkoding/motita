package peers

import (
	"context"
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
// through a proxy from the environment, which would be outside the network) and guarded.
func Transport() *http.Transport {
	dialer := &net.Dialer{Timeout: ProbeTimeout, Control: guard}
	return &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
}

// Prober asks other gateways who they are.
type Prober struct {
	Client *http.Client
}

// NewProber is a prober over Transport.
func NewProber() *Prober {
	return &Prober{Client: &http.Client{Transport: Transport(), Timeout: ProbeTimeout}}
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
func (p *Prober) Hello(ctx context.Context, address string) (Hello, netip.AddrPort, error) {
	var remote netip.AddrPort
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		remote, _ = netip.ParseAddrPort(info.Conn.RemoteAddr().String())
	}}
	ctx = httptrace.WithClientTrace(ctx, trace)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+HelloPath, nil)
	if err != nil {
		return Hello{}, remote, err
	}
	res, err := p.Client.Do(req)
	if err != nil {
		if errors.Is(err, errOutside) {
			return Hello{}, remote, errOutside
		}
		return Hello{}, remote, fmt.Errorf("no motita answered at %s: %w", address, err)
	}
	defer res.Body.Close()
	var h Hello
	if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&h) != nil || !h.Valid() {
		return Hello{}, remote, fmt.Errorf("what answered at %s is not a motita gateway, or one too old to connect to", address)
	}
	return h, netip.AddrPortFrom(remote.Addr().Unmap(), remote.Port()), nil
}

// ErrToken is a token the other gateway refused.
var ErrToken = errors.New("the other computer did not accept that token")

// ErrRefused is a gateway whose network rules do not let this one in.
var ErrRefused = errors.New("the other computer refuses connections from this one: " +
	"in its Settings, Network, let the local network in")

// CheckToken reports whether the gateway at address accepts token.
func (p *Prober) CheckToken(ctx context.Context, address, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/v1/sessions", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := p.Client.Do(req)
	if err != nil {
		return fmt.Errorf("the other computer could not be reached at %s: %w", address, err)
	}
	_ = res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return ErrToken
	case res.StatusCode == http.StatusForbidden:
		return ErrRefused
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("the other computer answered %s", res.Status)
	}
	return nil
}
