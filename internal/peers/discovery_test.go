package peers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/tailscale"
)

// packet is one datagram a fake socket receives.
type packet struct {
	data []byte
	from netip.AddrPort
}

// fakeConn is a beacon socket fed by a channel, recording what it sends.
type fakeConn struct {
	in     chan packet
	mu     sync.Mutex
	sent   []netip.AddrPort
	closed chan struct{}
	once   sync.Once
}

func newFakeConn() *fakeConn {
	return &fakeConn{in: make(chan packet, 8), closed: make(chan struct{})}
}

func (c *fakeConn) ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error) {
	select {
	case p := <-c.in:
		return copy(b, p.data), p.from, nil
	case <-c.closed:
		return 0, netip.AddrPort{}, net.ErrClosed
	}
}

func (c *fakeConn) WriteToUDPAddrPort(_ []byte, addr netip.AddrPort) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, addr)
	return 0, nil
}

func (c *fakeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeConn) sends() []netip.AddrPort {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]netip.AddrPort(nil), c.sent...)
}

// eventually waits for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 400 {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

func beacon(h Hello) []byte {
	b, _ := json.Marshal(h)
	return b
}

func TestDiscoveryHearsBeaconsGreetsBackAndConfirmsIPv6(t *testing.T) {
	reg, _ := Open("", "box")
	h := hello("peer0001", "desk")
	h.Addrs = []string{"bogus", "192.168.1.9", "fe80::9", "2001:db8::9", "fd00::9"}
	srv := gatewayServer(t, h, "tok")
	c6, c4 := newFakeConn(), newFakeConn()
	d := &Discovery{
		Registry: reg,
		Prober:   redirected(srv),
		Hello: func() (Hello, bool) {
			return Hello{Protocol: Protocol, ID: reg.Self().ID, Port: 7477}, true
		},
		Listen: func(network string, _ netip.Addr) (Conn, error) {
			if network == "udp6" {
				return c6, nil
			}
			return c4, nil
		},
		Zones: func() []string { return []string{"eth0", "wlan0"} },
		Every: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx)
		close(done)
	}()
	// The first announce: two zones on IPv6, the group on IPv4.
	eventually(t, "first announce", func() bool { return len(c6.sends()) == 2 && len(c4.sends()) == 1 })
	if got := c6.sends()[0]; got.Addr().Zone() != "eth0" || got.Port() != BeaconPort {
		t.Fatalf("v6 send = %v", got)
	}
	c4.in <- packet{data: []byte("not json"), from: ap("192.168.1.9:5")}
	c6.in <- packet{data: beacon(h), from: ap("[fe80::9%eth0]:5")}
	// Greeted back at once, and reached at the announced IPv6 address once confirmed.
	eventually(t, "greeting", func() bool { return len(c6.sends()) == 4 })
	eventually(t, "confirmed v6", func() bool {
		p, _ := reg.Find("peer0001")
		return p.Address != "" && netip.MustParseAddrPort(p.Address).Addr().Is6() &&
			!netip.MustParseAddrPort(p.Address).Addr().IsLinkLocalUnicast()
	})
	cancel()
	<-done
}

func TestConfirmGivesUpOnAnotherGateway(t *testing.T) {
	reg, _ := Open("", "box")
	srv := gatewayServer(t, hello("other001", "x"), "tok")
	d := &Discovery{Registry: reg, Prober: redirected(srv)}
	h := hello("peer0001", "desk")
	h.Addrs = []string{"fd00::9"}
	d.confirm(h)
	if len(reg.List()) != 0 {
		t.Fatalf("listed: %+v", reg.List())
	}
}

func TestDiscoveryWithoutSocketsStillProbesAndScans(t *testing.T) {
	reg, _ := Open("", "box")
	srv := gatewayServer(t, hello("peer0001", "desk"), "tok")
	reg.Saw(hello("peer0001", "desk"), ap("192.168.1.5:7477"), SourceManual)
	_ = reg.Pair("peer0001", "tok", "fp")
	reg.mu.Lock()
	delete(reg.live, "peer0001")
	reg.mu.Unlock()
	tail := hello("peer0002", "nas")
	tailSrv := gatewayServer(t, tail, "tok")
	scanned := 0
	var calls atomic.Int32
	d := &Discovery{
		Registry: reg,
		Prober:   redirected(srv),
		Hello: func() (Hello, bool) {
			calls.Add(1)
			return Hello{Port: 7477}, false
		},
		Listen: func(string, netip.Addr) (Conn, error) { return nil, errors.New("no multicast") },
		Tailnet: func(context.Context) []tailscale.Peer {
			scanned++
			return []tailscale.Peer{{HostName: "nas", IPs: []netip.Addr{netip.MustParseAddr("fd7a:115c:a1e0::2")}}}
		},
		Every: time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx)
		close(done)
	}()
	eventually(t, "probed", func() bool { p, _ := reg.Find("peer0001"); return p.Online })
	eventually(t, "second period", func() bool { return calls.Load() >= 3 })
	cancel()
	<-done
	if p, _ := reg.Find("peer0001"); !p.Online || p.Source != SourceManual {
		t.Fatalf("stale peer not probed: %+v", p)
	}
	// The tailnet machine answers as another gateway than the probe server: only peer0001's
	// server is reachable, so it is seen under that id; point the prober at the tailnet one.
	d.Prober = redirected(tailSrv)
	d.scanTailnet(context.Background())
	if p, ok := reg.Find("peer0002"); !ok || p.Source != SourceTailscale {
		t.Fatalf("tailnet peer: %+v", p)
	}
	if scanned < 2 {
		t.Fatalf("scanned %d times", scanned)
	}
}

func TestScanTailnetTriesTheNextAddressAndNeedsTailscale(t *testing.T) {
	reg, _ := Open("", "box")
	d := &Discovery{Registry: reg, Prober: NewProber(testCert), Hello: func() (Hello, bool) { return Hello{Port: 1}, true }}
	d.scanTailnet(context.Background())
	d.Tailnet = func(context.Context) []tailscale.Peer {
		return []tailscale.Peer{{IPs: []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("8.8.4.4")}}}
	}
	d.scanTailnet(context.Background())
	if len(reg.List()) != 0 {
		t.Fatal("listed")
	}
}

func TestRunWithTheRealSockets(t *testing.T) {
	reg, _ := Open("", "box")
	d := &Discovery{Registry: reg, Prober: NewProber(testCert), Hello: func() (Hello, bool) { return Hello{}, false }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Run(ctx)
	if d.Every != Every {
		t.Fatalf("every = %v", d.Every)
	}
	_ = MulticastZones()
}

func TestLocalAddrsKeepsTheReachableOnesIPv6First(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.1.2"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("8.8.8.8"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("fd00::2"), Mask: net.CIDRMask(64, 128)},
		&net.IPAddr{IP: net.ParseIP("10.0.0.1")},
	}
	got := LocalAddrs(addrs)
	if len(got) != 2 || got[0] != "fd00::2" || got[1] != "192.168.1.2" {
		t.Fatalf("got %v", got)
	}
}
