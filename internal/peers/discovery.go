package peers

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/tailscale"
)

// BeaconPort is the UDP port the beacons travel on.
const BeaconPort = 47877

// The multicast groups the beacons are sent to. Both are scoped to the local network: a router
// never forwards them, which is what keeps a beacon off the internet by construction.
var (
	Group6 = netip.MustParseAddr("ff02::6d74")
	Group4 = netip.MustParseAddr("239.255.77.84")
)

// Every is how often a gateway announces itself and looks at the tailnet and the stale peers.
const Every = 15 * time.Second

// Conn is one beacon socket. *net.UDPConn is the real one.
type Conn interface {
	ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error)
	WriteToUDPAddrPort(b []byte, addr netip.AddrPort) (int, error)
	Close() error
}

// Discovery announces this gateway and listens for the others.
type Discovery struct {
	Registry *Registry
	Prober   *Prober
	// Hello is what this gateway announces, and false when it is not reachable from another
	// machine (bound to loopback): it listens then, but does not announce.
	Hello func() (Hello, bool)
	// Listen opens a beacon socket for "udp6" or "udp4" on group; ListenMulticast by default.
	Listen func(network string, group netip.Addr) (Conn, error)
	// Zones are the interfaces an IPv6 beacon is sent on; MulticastZones by default.
	Zones func() []string
	// Tailnet lists the tailnet's online machines; nil when there is no Tailscale.
	Tailnet func(ctx context.Context) []tailscale.Peer
	// Every is the announce period, Every by default.
	Every time.Duration

	conns []beaconConn
	mu    sync.Mutex
}

// beaconConn is a socket and the group it sends to.
type beaconConn struct {
	conn  Conn
	group netip.Addr
}

// ListenMulticast joins group on the beacon port.
func ListenMulticast(network string, group netip.Addr) (Conn, error) {
	return net.ListenMulticastUDP(network, nil, &net.UDPAddr{IP: group.AsSlice(), Port: BeaconPort})
}

// MulticastZones names the interfaces that are up, carry multicast and are not the loopback.
func MulticastZones() []string {
	ifs, _ := net.Interfaces()
	var zones []string
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagMulticast != 0 && ifi.Flags&net.FlagLoopback == 0 {
			zones = append(zones, ifi.Name)
		}
	}
	return zones
}

// Run announces and listens until ctx ends. A socket that cannot be opened (no IPv6, no
// multicast) is skipped: the tailnet and the typed addresses still work.
func (d *Discovery) Run(ctx context.Context) {
	if d.Listen == nil {
		d.Listen = ListenMulticast
	}
	if d.Zones == nil {
		d.Zones = MulticastZones
	}
	if d.Every <= 0 {
		d.Every = Every
	}
	var wg sync.WaitGroup
	for _, g := range []struct {
		network string
		group   netip.Addr
	}{{"udp6", Group6}, {"udp4", Group4}} {
		c, err := d.Listen(g.network, g.group)
		if err != nil {
			continue
		}
		d.mu.Lock()
		d.conns = append(d.conns, beaconConn{conn: c, group: g.group})
		d.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.read(c)
		}()
	}
	ticker := time.NewTicker(d.Every)
	defer ticker.Stop()
	for tick := 0; ; tick++ {
		d.announce()
		d.probeStale(ctx)
		// The tailnet is looked at less often: every machine on it is asked over HTTP, and most
		// of them run no gateway.
		if tick%4 == 0 {
			d.scanTailnet(ctx)
		}
		select {
		case <-ctx.Done():
			d.mu.Lock()
			for _, c := range d.conns {
				_ = c.conn.Close()
			}
			d.mu.Unlock()
			wg.Wait()
			return
		case <-ticker.C:
		}
	}
}

// read handles the beacons one socket receives until it is closed.
func (d *Discovery) read(c Conn) {
	buf := make([]byte, 2048)
	for {
		n, from, err := c.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		var h Hello
		if json.Unmarshal(buf[:n], &h) != nil {
			continue
		}
		if d.Registry.Saw(h, netip.AddrPortFrom(from.Addr(), uint16(h.Port)), SourceLAN) {
			// A gateway seen for the first time is greeted back at once, so both list each
			// other without waiting for the next period.
			d.announce()
			go d.confirm(h)
		}
	}
}

// confirm asks a gateway first seen on the LAN at the IPv6 addresses it announced, so it is reached
// at one of those rather than at the link-local address its beacon came from.
func (d *Discovery) confirm(h Hello) {
	for _, raw := range h.Addrs {
		ip, err := netip.ParseAddr(raw)
		if err != nil || !ip.Is6() || ip.IsLinkLocalUnicast() || !Allowed(ip) {
			continue
		}
		at := netip.AddrPortFrom(ip, uint16(h.Port))
		ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
		got, _, err := d.Prober.Hello(ctx, at.String())
		cancel()
		if err == nil && got.ID == h.ID {
			d.Registry.Saw(got, at, SourceLAN)
			return
		}
	}
}

// LocalAddrs are this machine's addresses another gateway may reach it at: the private and
// tailnet ones, IPv6 first, never a loopback or link-local one.
func LocalAddrs(addrs []net.Addr) []string {
	var v6, v4 []string
	for _, a := range addrs {
		prefix, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := prefix.Addr().Unmap()
		if !Allowed(ip) || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.Is6() {
			v6 = append(v6, ip.String())
		} else {
			v4 = append(v4, ip.String())
		}
	}
	return append(v6, v4...)
}

// announce sends this gateway's beacon on every socket, on every interface for IPv6.
func (d *Discovery) announce() {
	h, ok := d.Hello()
	if !ok {
		return
	}
	msg, _ := json.Marshal(h)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.conns {
		if c.group.Is4() {
			_, _ = c.conn.WriteToUDPAddrPort(msg, netip.AddrPortFrom(c.group, BeaconPort))
			continue
		}
		for _, zone := range d.Zones() {
			_, _ = c.conn.WriteToUDPAddrPort(msg, netip.AddrPortFrom(c.group.WithZone(zone), BeaconPort))
		}
	}
}

// hello is one hello of the discovery, which gives up sooner than one asked for by hand: a scan
// asks many machines in turn.
func (d *Discovery) hello(ctx context.Context, addr string) (Hello, netip.AddrPort, error) {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	return d.Prober.Hello(ctx, addr)
}

// probeStale asks the connected gateways no beacon was heard from directly: across a VPN that is
// the only way to know they are there.
func (d *Discovery) probeStale(ctx context.Context) {
	for id, addr := range d.Registry.Stale() {
		// The address is one Saw wrote down, so it parses; one a hand typed was resolved then.
		at, _ := netip.ParseAddrPort(addr)
		if h, _, err := d.hello(ctx, addr); err == nil && h.ID == id {
			d.Registry.Saw(h, netip.AddrPortFrom(at.Addr(), uint16(h.Port)), d.Registry.SourceOf(id))
		}
	}
}

// scanTailnet asks every online machine of the tailnet, at its IPv6 address first, whether a
// gateway answers there on this gateway's own port.
func (d *Discovery) scanTailnet(ctx context.Context) {
	if d.Tailnet == nil {
		return
	}
	self, _ := d.Hello()
	for _, p := range d.Tailnet(ctx) {
		for _, ip := range p.IPs {
			addr := netip.AddrPortFrom(ip, uint16(self.Port)).String()
			if h, _, err := d.hello(ctx, addr); err == nil {
				d.Registry.Saw(h, netip.AddrPortFrom(ip, uint16(h.Port)), SourceTailscale)
				break
			}
		}
	}
}
