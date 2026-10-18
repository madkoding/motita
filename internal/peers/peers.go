// Package peers is how one motita gateway finds the others on the same network and keeps the ones
// the person connected to.
//
// A gateway is FOUND three ways: the beacons other gateways send to a link-local multicast group
// (the LAN), the online machines of the tailnet when Tailscale runs, and an address typed by hand
// (a VPN that carries no multicast). Being found grants nothing: a found gateway is only listed.
// It is CONNECTED (paired) when the person types that gateway's own token, which this one then
// keeps to speak to it on their behalf. Only addresses of private networks are ever spoken to -
// the LAN, a VPN, the tailnet - so nothing here reaches the internet, whatever a beacon claims.
package peers

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Protocol names the beacon and hello format, so another program on the same port is ignored.
const Protocol = "motita-peer/1"

// HelloPath is the endpoint every gateway answers, without a token, with who it is.
const HelloPath = "/v1/peer/hello"

// TTL is how long a sighting counts as online: a few beacon intervals, so one lost packet does not
// make a machine blink.
const TTL = 50 * time.Second

// Hello is what a gateway says about itself, in a beacon and at HelloPath.
type Hello struct {
	Protocol string `json:"motita"`
	// ID is the gateway's random, stable identity: the name and the address can change.
	ID string `json:"id"`
	// Name is the machine's name as its owner set it, the host name by default.
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// Port is where its API listens.
	Port int `json:"port"`
	// Addrs are the machine's own routable addresses. A beacon arrives from a link-local IPv6
	// address, which needs its interface to be used; these let the receiver find a better one.
	Addrs []string `json:"addrs,omitempty"`
}

// Valid reports whether a hello is one this package can use.
func (h Hello) Valid() bool {
	return h.Protocol == Protocol && validID(h.ID) && h.Port > 0 && h.Port < 65536
}

// validID accepts the identities newID makes and nothing that could break a URL path.
func validID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// Source is how a gateway was found.
type Source string

const (
	SourceLAN       Source = "lan"
	SourceTailscale Source = "tailscale"
	SourceManual    Source = "manual"
)

// Peer is another gateway as the API shows it. It never carries the token.
type Peer struct {
	ID string `json:"id"`
	// Name is what the gateway calls itself; Alias is what the person renamed it to here.
	Name    string `json:"name"`
	Alias   string `json:"alias,omitempty"`
	Address string `json:"address"`
	Source  Source `json:"source"`
	Version string `json:"version,omitempty"`
	// Paired says this gateway holds its token, so its sessions can be opened from here.
	Paired bool `json:"paired"`
	Online bool `json:"online"`
}

// Self is this gateway as the API shows it.
type Self struct {
	ID string `json:"id"`
	// Name is the name it announces: the one the person chose, or the host name.
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
}

// Target is where a paired gateway is spoken to, and with what.
type Target struct {
	Address string
	Token   string
}

// ErrUnknown is a peer this gateway has neither seen nor kept.
var ErrUnknown = errors.New("no computer with that id was found on the network")

// saved is a connected gateway as the file keeps it.
type saved struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Alias   string `json:"alias,omitempty"`
	Address string `json:"address"`
	Source  Source `json:"source"`
	Version string `json:"version,omitempty"`
	Token   string `json:"token"`
}

// document is the file: this gateway's identity and the gateways it is connected to.
type document struct {
	SelfID   string  `json:"self_id"`
	SelfName string  `json:"self_name,omitempty"`
	Peers    []saved `json:"peers"`
}

// sighting is a gateway seen recently, at every address it was seen at.
type sighting struct {
	hello  Hello
	source Source
	addrs  map[netip.AddrPort]time.Time
}

// Registry is the gateways found and kept. Safe for concurrent use.
type Registry struct {
	path     string
	hostname string
	// Now is the clock, a field so a test moves it.
	Now func() time.Time

	mu   sync.Mutex
	doc  document
	live map[string]*sighting
}

// newID is a random identity.
func newID() string {
	b := make([]byte, 16)
	// crypto/rand.Read never fails: it crashes the program rather than hand out a weak value.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Open loads the registry kept at path, or starts one with a new identity. An empty path keeps
// everything in memory. hostname is the machine's name, the default this gateway announces.
func Open(path, hostname string) (*Registry, error) {
	r := &Registry{path: path, hostname: strings.TrimSpace(hostname), Now: time.Now, live: map[string]*sighting{}}
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &r.doc); err != nil {
				return nil, fmt.Errorf("%s is not a computers file: %w", path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}
	if validID(r.doc.SelfID) {
		return r, nil
	}
	r.doc.SelfID = newID()
	return r, r.save()
}

// save writes the file, readable by its owner only: it holds other gateways' tokens.
func (r *Registry) save() error {
	if r.path == "" {
		return nil
	}
	data, _ := json.MarshalIndent(r.doc, "", "  ")
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Self is this gateway.
func (r *Registry) Self() Self {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Self{ID: r.doc.SelfID, Name: cmp.Or(r.doc.SelfName, r.hostname, "motita"), Hostname: r.hostname}
}

// SetName renames this gateway; an empty name goes back to the host name.
func (r *Registry) SetName(name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doc.SelfName = name
	return r.save()
}

// cleanName trims a name and refuses one too long to show.
func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if len([]rune(name)) > 64 {
		return "", errors.New("the name is too long: 64 characters at most")
	}
	return name, nil
}

// Saw records a gateway that answered from at. It reports whether the gateway is one not seen
// before (so the caller can greet it back at once). This gateway itself, a malformed hello and an
// address outside the private networks are ignored.
func (r *Registry) Saw(h Hello, at netip.AddrPort, src Source) bool {
	at = netip.AddrPortFrom(at.Addr().Unmap(), at.Port())
	if !h.Valid() || !Allowed(at.Addr()) {
		return false
	}
	h.Name, _ = cleanName(h.Name)
	r.mu.Lock()
	defer r.mu.Unlock()
	if h.ID == r.doc.SelfID {
		return false
	}
	now := r.Now()
	s, known := r.live[h.ID]
	if !known {
		s = &sighting{source: src, addrs: map[netip.AddrPort]time.Time{}}
		r.live[h.ID] = s
	}
	s.hello = h
	if src != SourceLAN || s.source == SourceLAN {
		// A tailnet or typed address is the more telling source; the LAN never overrides one.
		s.source = src
	}
	s.addrs[at] = now
	if i := r.savedIndex(h.ID); i >= 0 {
		p := &r.doc.Peers[i]
		best := s.best(now)
		if p.Name != h.Name || p.Version != h.Version || p.Address != best {
			p.Name, p.Version, p.Address = h.Name, h.Version, best
			_ = r.save()
		}
	}
	return !known
}

// best is the address to speak to a sighting at, among the fresh ones: see rank.
func (s *sighting) best(now time.Time) string {
	var best netip.AddrPort
	var bestAt time.Time
	for a, at := range s.addrs {
		if now.Sub(at) > TTL {
			delete(s.addrs, a)
			continue
		}
		if !best.IsValid() || rank(a.Addr()) < rank(best.Addr()) ||
			rank(a.Addr()) == rank(best.Addr()) && at.After(bestAt) {
			best, bestAt = a, at
		}
	}
	if !best.IsValid() {
		return ""
	}
	return best.String()
}

// rank orders the addresses a gateway is reached at: IPv6 first (what a tailnet or a LAN with
// IPv6 hands out, and stable), then IPv4, then the link-local ones, which need their interface.
func rank(a netip.Addr) int {
	switch {
	case a.IsLinkLocalUnicast():
		return 2
	case a.Is6():
		return 0
	default:
		return 1
	}
}

// cgnat is the shared address space Tailscale's IPv4 addresses come from.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Allowed reports whether a gateway may be spoken to at a: the private networks only - the LAN
// (and its link-local fallback), a VPN's private range, the tailnet - and this machine.
func Allowed(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLoopback() || cgnat.Contains(a)
}

// savedIndex is the position of a kept gateway, or -1.
func (r *Registry) savedIndex(id string) int {
	return slices.IndexFunc(r.doc.Peers, func(p saved) bool { return p.ID == id })
}

// List is every gateway found or kept: the connected ones first, then by name.
func (r *Registry) List() []Peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.list()
}

// list is List with the lock held.
func (r *Registry) list() []Peer {
	now := r.Now()
	out := []Peer{}
	for _, p := range r.doc.Peers {
		peer := Peer{ID: p.ID, Name: p.Name, Alias: p.Alias, Address: p.Address, Source: p.Source,
			Version: p.Version, Paired: true}
		if s, ok := r.live[p.ID]; ok {
			if best := s.best(now); best != "" {
				peer.Address, peer.Online = best, true
			}
		}
		out = append(out, peer)
	}
	for id, s := range r.live {
		if r.savedIndex(id) >= 0 {
			continue
		}
		best := s.best(now)
		if best == "" {
			delete(r.live, id)
			continue
		}
		out = append(out, Peer{ID: id, Name: s.hello.Name, Address: best, Source: s.source,
			Version: s.hello.Version, Online: true})
	}
	slices.SortFunc(out, func(a, b Peer) int {
		return cmp.Or(cmp.Compare(boolRank(!a.Paired), boolRank(!b.Paired)),
			cmp.Compare(strings.ToLower(cmp.Or(a.Alias, a.Name)), strings.ToLower(cmp.Or(b.Alias, b.Name))),
			cmp.Compare(a.ID, b.ID))
	})
	return out
}

// boolRank orders false before true.
func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Find is one gateway of List.
func (r *Registry) Find(id string) (Peer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.find(id)
}

// find is Find with the lock held.
func (r *Registry) find(id string) (Peer, bool) {
	list := r.list()
	i := slices.IndexFunc(list, func(p Peer) bool { return p.ID == id })
	if i < 0 {
		return Peer{}, false
	}
	return list[i], true
}

// Pair keeps a gateway with its token, at the address it was last seen at (or address, when it
// was never seen).
func (r *Registry) Pair(id, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("the other computer's token is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.find(id)
	if !ok {
		return ErrUnknown
	}
	if i := r.savedIndex(id); i >= 0 {
		r.doc.Peers[i].Token = token
		return r.save()
	}
	r.doc.Peers = append(r.doc.Peers, saved{ID: p.ID, Name: p.Name, Address: p.Address, Source: p.Source,
		Version: p.Version, Token: token})
	return r.save()
}

// Rename sets the name a kept gateway is shown under here; empty goes back to its own name.
func (r *Registry) Rename(id, alias string) error {
	alias, err := cleanName(alias)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.savedIndex(id)
	if i < 0 {
		return ErrUnknown
	}
	r.doc.Peers[i].Alias = alias
	return r.save()
}

// Forget drops a kept gateway and its token. It is listed again when it is next seen, unconnected.
func (r *Registry) Forget(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.savedIndex(id)
	if i < 0 {
		return ErrUnknown
	}
	r.doc.Peers = slices.Delete(r.doc.Peers, i, i+1)
	delete(r.live, id)
	return r.save()
}

// Target is where and how a connected gateway is spoken to.
func (r *Registry) Target(id string) (Target, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.find(id)
	if !ok || !p.Paired {
		return Target{}, false
	}
	return Target{Address: p.Address, Token: r.doc.Peers[r.savedIndex(id)].Token}, true
}

// Stale lists the connected gateways not seen for half the TTL, with the address they were last
// at, for the caller to ask again directly: a VPN carries no beacon.
func (r *Registry) Stale() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	out := map[string]string{}
	for _, p := range r.doc.Peers {
		fresh := false
		if s, ok := r.live[p.ID]; ok {
			for _, at := range s.addrs {
				fresh = fresh || now.Sub(at) < TTL/2
			}
		}
		if !fresh && p.Address != "" {
			out[p.ID] = p.Address
		}
	}
	return out
}

// SourceOf is how a gateway was found, SourceManual for one never seen.
func (r *Registry) SourceOf(id string) Source {
	p, ok := r.Find(id)
	if !ok {
		return SourceManual
	}
	return p.Source
}
