// Package peers is how one motita gateway finds the others on the same network and keeps the ones
// the person connected to.
//
// A gateway is FOUND three ways: the beacons other gateways send to a link-local multicast group
// (the LAN), the online machines of the tailnet when Tailscale runs, and an address typed by hand
// (a VPN that carries no multicast). Being found grants nothing: a found gateway is only listed.
// It is CONNECTED (paired) when the person types that gateway's own token, or when the person at
// that gateway lets this one in (see Ask), and this one then keeps the token to speak to it on
// their behalf, over TLS, pinned to the certificate it presented then (see identity.go). Only
// addresses of private networks are ever spoken to - the LAN, a VPN, the tailnet - so nothing here
// reaches the internet, whatever a beacon claims.
package peers

import (
	"cmp"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
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
const Protocol = "motita-peer/2"

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
	// Cert is its certificate (see EncodeCert): what the TLS connections to it are pinned to.
	Cert string `json:"cert"`
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
		lower := c >= 'a' && c <= 'z'
		digit := c >= '0' && c <= '9'
		if !lower && !digit && c != '-' {
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
	// Running is how many of its sessions have a run in flight, for a connected computer that
	// answers.
	Running int `json:"running"`
	// Ask is this gateway's request to be let in, while one is pending or was refused.
	Ask *Ask `json:"ask,omitempty"`
}

// Ask is a request this gateway sent to be let into another computer.
type Ask struct {
	// Code is what both screens show, to be compared: see PairCode.
	Code string `json:"code"`
	// State is AskWaiting or AskDenied.
	State string `json:"state"`
}

// The states of an Ask, and what Answer reports.
const (
	AskWaiting = "waiting"
	AskDenied  = "denied"
	AskAllowed = "allowed"
)

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
	// Cert is the certificate the connection must present.
	Cert string
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
	Cert    string `json:"cert"`
}

// grant is a computer let into this one, with the token it was given. The token is only good on a
// connection that presents the same certificate.
type grant struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Cert  string    `json:"cert"`
	Token string    `json:"token"`
	At    time.Time `json:"at"`
}

// Grant is a computer let into this one, as the API shows it.
type Grant struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

// Request is a computer asking to be let into this one, as the API shows it.
type Request struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Code    string `json:"code"`
}

// pending is a Request with what the registry keeps about it.
type pending struct {
	Request
	cert  string
	at    time.Time
	state string
	token string
}

// AskTTL is how long a request to be let in waits for an answer.
const AskTTL = 5 * time.Minute

// MaxRequests bounds the requests waiting at once: anyone on the network can ask.
const MaxRequests = 8

// ErrBusy is a request refused because too many are waiting.
var ErrBusy = errors.New("too many computers are asking to connect to this one: try again in a few minutes")

// document is the file: this gateway's identity and the gateways it is connected to.
type document struct {
	SelfID   string `json:"self_id"`
	SelfName string `json:"self_name,omitempty"`
	// SelfKey is this gateway's certificate and key, as PEM.
	SelfKey string  `json:"self_key,omitempty"`
	Peers   []saved `json:"peers"`
	Granted []grant `json:"granted,omitempty"`
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
	// cert is this gateway's certificate, fp its fingerprint.
	cert tls.Certificate
	fp   string
	// requests are the computers asking to be let in, by their fingerprint.
	requests map[string]*pending
	// asks are this gateway's own requests to others, by their id.
	asks map[string]Ask
	// running is how many runs each connected computer has in flight, as last read.
	running map[string]int
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
	r := &Registry{path: path, hostname: strings.TrimSpace(hostname), Now: time.Now, live: map[string]*sighting{},
		requests: map[string]*pending{}, asks: map[string]Ask{}, running: map[string]int{}}
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &r.doc); err != nil {
				return nil, fmt.Errorf("%s is not a computers file: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// A first run: the identity is made below.
		default:
			return nil, err
		}
	}
	fresh := !validID(r.doc.SelfID)
	if fresh {
		r.doc.SelfID = newID()
	}
	cert, ok := parseCertificate(r.doc.SelfKey)
	if !ok {
		r.doc.SelfKey = newCertificate(r.doc.SelfID)
		cert, _ = parseCertificate(r.doc.SelfKey)
		fresh = true
	}
	r.cert, r.fp = cert, Fingerprint(cert.Certificate[0])
	if !fresh {
		return r, nil
	}
	return r, r.save()
}

// Certificate is this gateway's certificate, Cert the same as it is announced, and Fingerprint its
// fingerprint.
func (r *Registry) Certificate() tls.Certificate { return r.cert }
func (r *Registry) Cert() string                 { return EncodeCert(r.cert.Certificate[0]) }
func (r *Registry) Fingerprint() string          { return r.fp }

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
				peer.Address, peer.Online, peer.Running = best, true, r.running[p.ID]
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
		peer := Peer{ID: id, Name: s.hello.Name, Address: best, Source: s.source, Version: s.hello.Version, Online: true}
		if a, ok := r.asks[id]; ok {
			peer.Ask = &a
		}
		out = append(out, peer)
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

// Pair keeps a gateway with its token, pinned to the certificate cert (see EncodeCert), at the
// address it was last seen at.
func (r *Registry) Pair(id, token, cert string) error {
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
	delete(r.asks, id)
	if i := r.savedIndex(id); i >= 0 {
		r.doc.Peers[i].Token, r.doc.Peers[i].Cert = token, cert
		return r.save()
	}
	r.doc.Peers = append(r.doc.Peers, saved{ID: p.ID, Name: p.Name, Address: p.Address, Source: p.Source,
		Version: p.Version, Token: token, Cert: cert})
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
	delete(r.running, id)
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
	s := r.doc.Peers[r.savedIndex(id)]
	return Target{Address: p.Address, Token: s.Token, Cert: s.Cert}, true
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

// SetRunning records how many runs a connected computer has in flight.
func (r *Registry) SetRunning(id string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running[id] = n
}

// SetAsk records this gateway's request to be let into the computer id; a zero Ask drops it.
func (r *Registry) SetAsk(id string, a Ask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a == (Ask{}) {
		delete(r.asks, id)
		return
	}
	r.asks[id] = a
}

// Ask records a computer asking to be let into this one, from address, with the certificate
// fingerprint its connection presented, and returns the code to compare. Asking again replaces
// the request; at most MaxRequests wait at once.
func (r *Registry) Ask(id, name, address, cert string) (string, error) {
	name, _ = cleanName(name)
	if !validID(id) || name == "" || cert == "" {
		return "", errors.New("a request to connect needs the computer's id and name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	for fp, q := range r.requests {
		if now.Sub(q.at) > AskTTL || q.ID == id {
			delete(r.requests, fp)
		}
	}
	if _, again := r.requests[cert]; !again && len(r.requests) >= MaxRequests {
		return "", ErrBusy
	}
	code := PairCode(r.fp, cert)
	r.requests[cert] = &pending{Request: Request{ID: id, Name: name, Address: address, Code: code},
		cert: cert, at: now, state: AskWaiting}
	return code, nil
}

// Requests are the computers waiting for an answer, oldest first.
func (r *Registry) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	var waiting []*pending
	for _, q := range r.requests {
		if q.state == AskWaiting && now.Sub(q.at) <= AskTTL {
			waiting = append(waiting, q)
		}
	}
	slices.SortFunc(waiting, func(a, b *pending) int { return a.at.Compare(b.at) })
	out := []Request{}
	for _, q := range waiting {
		out = append(out, q.Request)
	}
	return out
}

// Decide answers the computer id's request: allowed, it is given a token of its own, kept here
// until it is revoked.
func (r *Registry) Decide(id string, allow bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	for _, q := range r.requests {
		if q.ID != id || q.state != AskWaiting || now.Sub(q.at) > AskTTL {
			continue
		}
		if !allow {
			q.state = AskDenied
			return nil
		}
		q.state, q.token = AskAllowed, newID()+newID()
		r.doc.Granted = slices.DeleteFunc(r.doc.Granted, func(g grant) bool { return g.ID == id })
		r.doc.Granted = append(r.doc.Granted, grant{ID: id, Name: q.Name, Cert: q.cert, Token: q.token, At: now})
		return r.save()
	}
	return ErrUnknown
}

// Answer is the state of the request sent by the computer with certificate fingerprint cert, and
// the token when it was allowed. An answer given (allowed or denied) is handed out once; "" is no
// request.
func (r *Registry) Answer(cert string) (state, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q, ok := r.requests[cert]
	if !ok || r.Now().Sub(q.at) > AskTTL {
		delete(r.requests, cert)
		return "", ""
	}
	if q.state != AskWaiting {
		delete(r.requests, cert)
	}
	return q.state, q.token
}

// Granted reports whether token is one this gateway gave to the computer whose connection
// presented the certificate fingerprint cert.
func (r *Registry) Granted(token, cert string) bool {
	if token == "" || cert == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ok := false
	for _, g := range r.doc.Granted {
		// Every grant is compared, in constant time: the loop takes as long whichever matches.
		match := subtle.ConstantTimeCompare([]byte(g.Token), []byte(token)) &
			subtle.ConstantTimeCompare([]byte(g.Cert), []byte(cert))
		ok = ok || match == 1
	}
	return ok
}

// Grants are the computers let into this one.
func (r *Registry) Grants() []Grant {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Grant{}
	for _, g := range r.doc.Granted {
		out = append(out, Grant{ID: g.ID, Name: g.Name, At: g.At})
	}
	return out
}

// Revoke takes back the token a computer was let in with.
func (r *Registry) Revoke(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := slices.IndexFunc(r.doc.Granted, func(g grant) bool { return g.ID == id })
	if i < 0 {
		return ErrUnknown
	}
	r.doc.Granted = slices.Delete(r.doc.Granted, i, i+1)
	return r.save()
}
