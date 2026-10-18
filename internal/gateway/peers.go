package gateway

import (
	"cmp"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/peers"
	"github.com/madkoding/motita/internal/tailscale"
)

// THE OTHER COMPUTERS, each running its own gateway.
//
// A gateway announces itself on the local network and finds the others there, on the tailnet, or
// at an address typed by hand (see the peers package). Finding one grants nothing. The person
// CONNECTS it by typing that computer's token once, or by asking it and having the person at that
// computer let this one in (a token of its own is then issued for this computer, tied to its
// certificate and revocable there). From then on this gateway speaks to it on their behalf, over
// TLS pinned to the certificate it presented when connected, and the page opens its sessions,
// projects and settings through /v1/peers/{id}/proxy/..., which forwards the request, the stream
// of events and the WebSocket as they are. The page never holds the other token, and the other
// gateway only ever sees a bearer client: its own token check, network rules and limits apply
// unchanged.

// peerProxyPrefix is where a connected computer's API is reached through this gateway.
const peerProxyPrefix = "/v1/peers/"

// openPeers loads this gateway's identity and the computers it is connected to, and starts looking
// for the others when discovery is on. A file that cannot be read is NOT fatal: the gateway serves,
// with an identity of this run only, and says so.
func (s *Server) openPeers() {
	host, _ := osHostname()
	reg, err := peers.Open(s.opts.PeerFile, host)
	if err != nil {
		s.warn("could not open the computers file; other computers are kept in memory only", "error", err.Error())
		reg, _ = peers.Open("", host)
	}
	s.peers = reg
	s.prober = peers.NewProber(reg.Certificate())
	s.peerProxy = peers.Transport(reg.Certificate())
	// The other gateways speak TLS on this same port; the browser keeps its plain address.
	s.listener = peers.Sniff(s.listener, peers.ServerConfig(reg.Certificate()))
	if !s.opts.Discovery {
		return
	}
	d := &peers.Discovery{Registry: reg, Prober: s.prober, Hello: s.peerHello}
	if tn, ok := s.opts.Tailscale.(interface {
		Peers(ctx context.Context) []tailscale.Peer
	}); ok {
		d.Tailnet = tn.Peers
	}
	go runDiscovery(d, s.baseCtx)
}

// runDiscovery runs the announcer; a variable so a test sees it started without sockets.
var runDiscovery = (*peers.Discovery).Run

// interfaceAddrs is this machine's addresses, a variable so a test can choose them.
var interfaceAddrs = net.InterfaceAddrs

// peerHello is what this gateway says about itself, and whether another computer can reach it: a
// gateway bound to loopback answers no one else, so it does not announce.
func (s *Server) peerHello() (peers.Hello, bool) {
	self := s.peers.Self()
	host, port, _ := net.SplitHostPort(s.listener.Addr().String())
	p, _ := strconv.Atoi(port)
	addrs, _ := interfaceAddrs()
	return peers.Hello{Protocol: peers.Protocol, ID: self.ID, Name: self.Name, Version: s.opts.Version,
		Port: p, Addrs: peers.LocalAddrs(addrs), Cert: s.peers.Cert()}, !isLoopback(host)
}

// handlePeerHello answers another gateway that asks who this one is.
func (s *Server) handlePeerHello(w http.ResponseWriter, _ *http.Request) {
	h, _ := s.peerHello()
	writeJSON(w, http.StatusOK, h)
}

// peerRoutes registers the endpoints about the other computers.
func (s *Server) peerRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	// Asked by another gateway, before it holds any token: the connection's certificate says who.
	mux.HandleFunc("POST "+peers.PairPath, s.handlePairAsk)
	mux.HandleFunc("GET "+peers.PairPath, s.handlePairAnswer)
	mux.Handle("GET /v1/peers", plain(s.handleListPeers))
	mux.Handle("POST /v1/peers", plain(s.handleAddPeer))
	mux.Handle("PUT /v1/peers/self", plain(s.handleRenameSelf))
	mux.Handle("PUT /v1/peers/{id}", plain(s.handleUpdatePeer))
	mux.Handle("DELETE /v1/peers/{id}", plain(s.handleForgetPeer))
	mux.Handle("POST /v1/peers/{id}/ask", plain(s.handleAskPeer))
	mux.Handle("POST /v1/peer-requests/{id}", plain(s.handleDecide))
	mux.Handle("DELETE /v1/peer-grants/{id}", plain(s.handleRevoke))
	// Every method: the page's GETs, POSTs, PUTs, PATCHes and DELETEs all go through.
	mux.Handle(peerProxyPrefix+"{id}/proxy/{rest...}", plain(s.handlePeerProxy))
}

// selfView is this computer as the page shows it.
type selfView struct {
	peers.Self
	// Reachable is false when the gateway listens on loopback only: no other computer can connect
	// to it then, and the page points at the network settings.
	Reachable bool `json:"reachable"`
	// Running is how many sessions here have a run in flight.
	Running int `json:"running"`
}

// peersView is what the computer endpoints answer.
type peersView struct {
	Self      selfView     `json:"self"`
	Peers     []peers.Peer `json:"peers"`
	Discovery bool         `json:"discovery"`
	// Requests are the computers asking to be let into this one; Grants the ones let in.
	Requests []peers.Request `json:"requests"`
	Grants   []peers.Grant   `json:"grants"`
}

func (s *Server) peersView() peersView {
	_, reachable := s.peerHello()
	return peersView{Self: selfView{Self: s.peers.Self(), Reachable: reachable, Running: s.runningHere()},
		Peers: s.peers.List(), Discovery: s.opts.Discovery, Requests: s.peers.Requests(), Grants: s.peers.Grants()}
}

// runningHere counts this gateway's sessions with a run in flight.
func (s *Server) runningHere() int {
	n := 0
	for _, c := range s.snapshot() {
		if c.status().Running {
			n++
		}
	}
	return n
}

func (s *Server) handleListPeers(w http.ResponseWriter, _ *http.Request) {
	s.kickRunning()
	writeJSON(w, http.StatusOK, s.peersView())
}

// runningEvery is how often the runs of the connected computers are read, at most: only while the
// page reads the list, which it does while it is open.
var runningEvery = 10 * time.Second

// kickRunning reads the connected computers' runs in the background, when the last read is old.
func (s *Server) kickRunning() {
	if time.Since(time.Unix(0, s.runningAt.Load())) < runningEvery || !s.runningBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.runningBusy.Store(false)
		s.readRunning(s.baseCtx)
		s.runningAt.Store(time.Now().UnixNano())
	}()
}

// readRunning asks each connected computer that answers how many runs it has in flight.
func (s *Server) readRunning(ctx context.Context) {
	for _, p := range s.peers.List() {
		if !p.Paired || !p.Online {
			continue
		}
		t, _ := s.peers.Target(p.ID)
		if n, err := s.prober.Running(ctx, t); err == nil {
			s.peers.SetRunning(p.ID, n)
		}
	}
}

// handleRenameSelf changes the name this computer is shown and announced under.
func (s *Server) handleRenameSelf(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if err := s.peers.SetName(body.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.peersView())
}

// handleAddPeer connects a computer by its address - the one way across a VPN, which carries no
// beacon - with its token, or, without one, asks it to let this one in.
func (s *Server) handleAddPeer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Address) == "" {
		writeError(w, http.StatusBadRequest, "the other computer's address is required")
		return
	}
	self, _ := s.peerHello()
	addr := peers.WithPort(body.Address, self.Port)
	h, at, ok := s.reach(w, r, addr, "")
	if !ok {
		return
	}
	if strings.TrimSpace(body.Token) == "" {
		s.peers.Saw(h, at, peers.SourceManual)
		s.askPeer(w, r, h, at, addr)
		return
	}
	if !s.connectPeer(w, r, addr, h.Cert, body.Token) {
		return
	}
	s.peers.Saw(h, at, peers.SourceManual)
	if s.pairPeer(w, h.ID, body.Token, h.Cert) {
		writeJSON(w, http.StatusOK, s.peersView())
	}
}

// reach asks the computer at addr who it is, and checks it is another one (and the one meant, when
// id is given). It writes the refusal and returns false when not. The address returned is where it
// answered, at the port its API listens on.
func (s *Server) reach(w http.ResponseWriter, r *http.Request, addr, id string) (peers.Hello, netip.AddrPort, bool) {
	h, at, err := s.prober.Hello(r.Context(), addr)
	switch {
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	case h.ID == s.peers.Self().ID:
		writeError(w, http.StatusBadRequest, "that address is this computer")
	case id != "" && h.ID != id:
		writeError(w, http.StatusBadGateway, "another computer answers at "+addr+" now: wait for it to be found again")
	default:
		return h, netip.AddrPortFrom(at.Addr(), uint16(h.Port)), true
	}
	return h, at, false
}

// connectPeer checks that the computer at addr, presenting the certificate cert, accepts token. It
// writes the refusal and returns false when not.
func (s *Server) connectPeer(w http.ResponseWriter, r *http.Request, addr, cert, token string) bool {
	err := s.prober.CheckToken(r.Context(), addr, cert, strings.TrimSpace(token))
	switch {
	case err == nil:
		return true
	case errors.Is(err, peers.ErrToken), errors.Is(err, peers.ErrRefused):
		// Not 401 or 403: those would read to the page as ITS OWN credential being refused.
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
	return false
}

// peerStatus is the status for a registry error.
func peerStatus(err error) int {
	if errors.Is(err, peers.ErrUnknown) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// handleUpdatePeer connects a found computer (token) or renames a connected one (alias).
func (s *Server) handleUpdatePeer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alias *string `json:"alias"`
		Token *string `json:"token"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	if body.Token != nil {
		p, ok := s.peers.Find(id)
		if !ok {
			writeError(w, http.StatusNotFound, peers.ErrUnknown.Error())
			return
		}
		h, _, ok := s.reach(w, r, p.Address, id)
		if !ok || !s.connectPeer(w, r, p.Address, h.Cert, *body.Token) || !s.pairPeer(w, id, *body.Token, h.Cert) {
			return
		}
	}
	if body.Alias != nil {
		if err := s.peers.Rename(id, *body.Alias); err != nil {
			writeError(w, peerStatus(err), err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, s.peersView())
}

// pairPeer keeps the token and the certificate it is good with, or writes why it could not.
func (s *Server) pairPeer(w http.ResponseWriter, id, token, cert string) bool {
	if err := s.peers.Pair(id, token, cert); err != nil {
		writeError(w, peerStatus(err), err.Error())
		return false
	}
	// A connection kept from before was made with the previous pin.
	s.peerProxy.CloseIdleConnections()
	return true
}

// handleForgetPeer disconnects a computer and drops its token.
func (s *Server) handleForgetPeer(w http.ResponseWriter, r *http.Request) {
	if err := s.peers.Forget(r.PathValue("id")); err != nil {
		writeError(w, peerStatus(err), err.Error())
		return
	}
	s.peerProxy.CloseIdleConnections()
	writeJSON(w, http.StatusOK, s.peersView())
}

// handleAskPeer asks a found computer to let this one in.
func (s *Server) handleAskPeer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.peers.Find(id)
	if !ok {
		writeError(w, http.StatusNotFound, peers.ErrUnknown.Error())
		return
	}
	if h, at, ok := s.reach(w, r, p.Address, id); ok {
		s.askPeer(w, r, h, at, p.Address)
	}
}

// askPeer sends the request to the computer h at addr and waits for its answer in the background:
// the person there may take a while to come to it.
func (s *Server) askPeer(w http.ResponseWriter, r *http.Request, h peers.Hello, at netip.AddrPort, addr string) {
	self := s.peers.Self()
	if err := s.prober.Ask(r.Context(), addr, h.Cert, peers.AskBody{ID: self.ID, Name: self.Name}); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	code := peers.PairCode(s.peers.Fingerprint(), peers.FingerprintOf(h.Cert))
	s.peers.SetAsk(h.ID, peers.Ask{Code: code, State: peers.AskWaiting})
	go s.awaitApproval(h, at, addr, code)
	writeJSON(w, http.StatusOK, s.peersView())
}

// askEvery is how often a request to be let in is checked on, and askWait how long for.
var (
	askEvery = 2 * time.Second
	askWait  = peers.AskTTL
)

// awaitApproval checks on this gateway's request to the computer h until it is answered or expires.
func (s *Server) awaitApproval(h peers.Hello, at netip.AddrPort, addr, code string) {
	src := s.peers.SourceOf(h.ID)
	deadline := time.Now().Add(askWait)
	tick := time.NewTicker(askEvery)
	defer tick.Stop()
	for time.Now().Before(deadline) && s.baseCtx.Err() == nil {
		<-tick.C
		// Talking to it is seeing it: a computer reached by address sends no beacon.
		s.peers.Saw(h, at, src)
		a, err := s.prober.Answer(s.baseCtx, addr, h.Cert)
		switch {
		case err != nil:
			continue
		case a.State == peers.AskAllowed:
			_ = s.peers.Pair(h.ID, a.Token, h.Cert)
			return
		case a.State == peers.AskDenied:
			s.peers.SetAsk(h.ID, peers.Ask{Code: code, State: peers.AskDenied})
			return
		default:
			// Still waiting for the person there.
		}
	}
	s.peers.SetAsk(h.ID, peers.Ask{})
}

// requireAuth is the check every endpoint but a few passes: the token or the browser's cookie, or
// a computer let into this one.
func (s *Server) requireAuth(h http.Handler) http.Handler {
	return requireCredential(s.opts.Token, s.peerGranted, h, s.opts.Origins...)
}

// clientCert is the fingerprint of the certificate the connection presented, or "".
func clientCert(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return peers.Fingerprint(r.TLS.PeerCertificates[0].Raw)
}

// peerGranted reports whether a request comes from a computer let into this one, with its token
// and over a connection that presents its certificate.
func (s *Server) peerGranted(r *http.Request) bool {
	return s.peers.Granted(bearer(r.Header.Get("Authorization")), clientCert(r))
}

// errNoCert is a request to connect that did not come from a gateway.
const errNoCert = "only another motita gateway can ask to connect, over TLS with its certificate"

// handlePairAsk is another computer asking to be let in. Anyone on the network can ask, so the
// requests waiting are few and short-lived, and the person here decides.
func (s *Server) handlePairAsk(w http.ResponseWriter, r *http.Request) {
	cert := clientCert(r)
	from, err := netip.ParseAddrPort(r.RemoteAddr)
	if cert == "" || err != nil || !peers.Allowed(from.Addr()) {
		writeError(w, http.StatusForbidden, errNoCert)
		return
	}
	var body peers.AskBody
	if !s.decodeBody(w, r, &body) {
		return
	}
	code, err := s.peers.Ask(body.ID, body.Name, from.Addr().Unmap().String(), cert)
	switch {
	case errors.Is(err, peers.ErrBusy):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.info("another computer asks to connect", "name", body.Name, "code", code)
		writeJSON(w, http.StatusAccepted, map[string]string{"code": code})
	}
}

// handlePairAnswer tells the computer that asked what was decided.
func (s *Server) handlePairAnswer(w http.ResponseWriter, r *http.Request) {
	state, token := s.peers.Answer(clientCert(r))
	if state == "" {
		writeError(w, http.StatusNotFound, "no request to connect from this computer is waiting")
		return
	}
	writeJSON(w, http.StatusOK, peers.AnswerBody{State: state, Token: token})
}

// handleDecide lets a computer that asked in, or not.
func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Allow bool `json:"allow"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if err := s.peers.Decide(r.PathValue("id"), body.Allow); err != nil {
		writeError(w, peerStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.peersView())
}

// handleRevoke takes back the token a computer was let in with.
func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.peers.Revoke(r.PathValue("id")); err != nil {
		writeError(w, peerStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.peersView())
}

// proxyRefused are the endpoints never forwarded: the other computer's own list of computers (no
// chains of hops, and no way to reach a third gateway from here), its cookie exchange (the page
// holds this gateway's cookie, never another's) and its shutdown.
var proxyRefused = []string{
	"/v1/peers", "/v1/peer/", "/v1/peer-requests/", "/v1/peer-grants/", "/v1/webui/", "/v1/shutdown",
}

// peerPath is the path forwarded, or "" when it is not one the page may reach: an API path, with no
// dot segments, outside proxyRefused.
func peerPath(rest string) string {
	p := "/" + rest
	if path.Clean(p) != p || !strings.HasPrefix(p, "/v1/") {
		return ""
	}
	for _, refused := range proxyRefused {
		if p == refused || strings.HasPrefix(p, strings.TrimSuffix(refused, "/")+"/") {
			return ""
		}
	}
	return p
}

// handlePeerProxy forwards one request to a connected computer, with its token and nothing of the
// browser's: no cookie, no origin, no forwarding headers. The answer comes back as it is, streamed
// as it is written, minus any cookie.
func (s *Server) handlePeerProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target, ok := s.peers.Target(id)
	if !ok {
		writeError(w, http.StatusNotFound, "that computer is not connected: connect it with its token first")
		return
	}
	p := peerPath(r.PathValue("rest"))
	if p == "" {
		writeError(w, http.StatusForbidden, "that endpoint of another computer is not reachable from here")
		return
	}
	// The escaped form keeps a name with an encoded slash (an artifact's) as the page wrote it.
	raw := strings.TrimPrefix(r.URL.EscapedPath(), peerProxyPrefix+id+"/proxy")
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = target.Address
			pr.Out.URL.Path = p
			pr.Out.URL.RawPath = raw
			pr.Out.Host = target.Address
			for _, h := range []string{"Cookie", "Origin", "Referer", browserHeader, "X-Forwarded-Host", "X-Forwarded-Proto"} {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set("Authorization", "Bearer "+target.Token)
		},
		Transport:     s.peerProxy,
		FlushInterval: -1,
		ModifyResponse: func(res *http.Response) error {
			// No cookie of the other computer's, and its security headers once only: this
			// gateway already set the same ones on the answer.
			for _, h := range []string{"Set-Cookie", "Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy"} {
				res.Header.Del(h)
			}
			if res.StatusCode == http.StatusUnauthorized {
				// The other computer's token changed. Passed on as it is, the page would read it as
				// its OWN credential refused and log out of this gateway.
				return errPeerToken
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			msg := cmp.Or(peerProxyMessage(err), "the computer "+target.Address+" did not answer: "+err.Error())
			writeError(w, http.StatusBadGateway, msg)
		},
	}
	proxy.ServeHTTP(w, r.WithContext(peers.WithPin(r.Context(), target.Cert)))
}

// errPeerToken is a connected computer that no longer accepts its token.
var errPeerToken = errors.New("the other computer no longer accepts its saved token (it changed): connect it again")

// peerProxyMessage is the sentence for a failure the person can act on, or "".
func peerProxyMessage(err error) string {
	for _, known := range []error{errPeerToken, peers.ErrIdentity} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return ""
}
