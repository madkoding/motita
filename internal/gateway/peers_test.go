package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/peers"
	"github.com/madkoding/motita/internal/tailscale"
)

const otherToken = "the-other-computers-token"

// peerPair is this gateway and another one on the same machine, standing for two computers.
func peerPair(t *testing.T, mutators ...func(*Options)) (here, there *Server) {
	t.Helper()
	here = newTestServer(t, &fakeService{}, mutators...)
	there = newTestServer(t, &fakeService{}, func(o *Options) { o.Token = otherToken })
	return here, there
}

func sendPeers(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	if srv.opts.Token == otherToken {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+otherToken)
		return servePeer(srv, req)
	}
	return sendNetwork(t, srv, method, path, body)
}

func decodePeers(t *testing.T, w *httptest.ResponseRecorder) peersView {
	t.Helper()
	var v peersView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

// connect adds there to here by address and token, and returns its id.
func connect(t *testing.T, here, there *Server) string {
	t.Helper()
	v := decodePeers(t, sendPeers(t, here, http.MethodPost, "/v1/peers",
		`{"address":"`+there.Addr()+`","token":"`+otherToken+`"}`))
	if len(v.Peers) != 1 || !v.Peers[0].Paired || !v.Peers[0].Online || v.Peers[0].Source != peers.SourceManual {
		t.Fatalf("peers = %+v", v.Peers)
	}
	return v.Peers[0].ID
}

func TestHelloSaysWhoThisGatewayIsWithoutAToken(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	res, err := http.Get(srv.BaseURL() + peers.HelloPath)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var h peers.Hello
	if json.NewDecoder(res.Body).Decode(&h) != nil || !h.Valid() || h.Version != "test" {
		t.Fatalf("hello = %+v", h)
	}
	if _, reachable := srv.peerHello(); reachable {
		t.Fatal("a loopback gateway says it is reachable")
	}
}

func TestAWildcardGatewayIsReachable(t *testing.T) {
	old := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = old })
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("fd00::7"), Mask: net.CIDRMask(64, 128)}}, nil
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Listen = "0.0.0.0:0" })
	h, reachable := srv.peerHello()
	if !reachable || len(h.Addrs) != 1 || h.Addrs[0] != "fd00::7" {
		t.Fatalf("hello = %+v reachable=%v", h, reachable)
	}
	if !decodePeers(t, sendPeers(t, srv, http.MethodGet, "/v1/peers", "")).Self.Reachable {
		t.Fatal("not reachable in the view")
	}
}

func TestThisComputerIsNamedAfterTheHostAndRenamed(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	host, _ := os.Hostname()
	v := decodePeers(t, sendPeers(t, srv, http.MethodGet, "/v1/peers", ""))
	if v.Self.Hostname != host || v.Discovery || len(v.Peers) != 0 {
		t.Fatalf("view = %+v", v)
	}
	v = decodePeers(t, sendPeers(t, srv, http.MethodPut, "/v1/peers/self", `{"name":"Taller"}`))
	if v.Self.Name != "Taller" {
		t.Fatalf("renamed = %+v", v.Self)
	}
	long := `{"name":"` + strings.Repeat("x", 65) + `"}`
	if w := sendPeers(t, srv, http.MethodPut, "/v1/peers/self", long); w.Code != http.StatusBadRequest {
		t.Fatalf("long name: %d", w.Code)
	}
	if w := sendPeers(t, srv, http.MethodPut, "/v1/peers/self", `nope`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", w.Code)
	}
}

func TestAddingAComputerChecksWhatAnswersAndItsToken(t *testing.T) {
	here, there := peerPair(t)
	for name, tc := range map[string]struct {
		body string
		code int
	}{
		"bad body":   {`nope`, http.StatusBadRequest},
		"no address": {`{"token":"x"}`, http.StatusBadRequest},
		"public":     {`{"address":"8.8.8.8","token":"x"}`, http.StatusBadGateway},
		"itself":     {`{"address":"` + here.Addr() + `","token":"x"}`, http.StatusBadRequest},
		"bad token":  {`{"address":"` + there.Addr() + `","token":"wrong"}`, http.StatusUnprocessableEntity},
		"no gateway": {`{"address":"127.0.0.1:1","token":"x"}`, http.StatusBadGateway},
	} {
		if w := sendPeers(t, here, http.MethodPost, "/v1/peers", tc.body); w.Code != tc.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	connect(t, here, there)
}

func TestATokenCheckThatFailsOtherwiseIsABadGateway(t *testing.T) {
	here := newTestServer(t, &fakeService{})
	reg, _ := peers.Open("", "x")
	hello := peers.Hello{Protocol: peers.Protocol, ID: "peer0001", Name: "x", Port: 1, Cert: reg.Cert()}
	broken := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == peers.HelloPath {
			_ = json.NewEncoder(w).Encode(hello)
			return
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	broken.Listener = peers.Sniff(broken.Listener, peers.ServerConfig(reg.Certificate()))
	broken.Start()
	t.Cleanup(broken.Close)
	addr := broken.Listener.Addr().String()
	w := sendPeers(t, here, http.MethodPost, "/v1/peers", `{"address":"`+addr+`","token":"x"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestAComputerThatCannotBeSavedIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")
	here, there := peerPair(t, func(o *Options) { o.PeerFile = path })
	// The file's place is taken by a directory that is not empty: the next save fails.
	_ = os.Remove(path)
	_ = os.MkdirAll(filepath.Join(path, "x"), 0o700)
	w := sendPeers(t, here, http.MethodPost, "/v1/peers", `{"address":"`+there.Addr()+`","token":"`+otherToken+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestAFoundComputerIsConnectedRenamedAndForgotten(t *testing.T) {
	here, there := peerPair(t)
	id := connect(t, here, there)
	longAlias := `{"alias":"` + strings.Repeat("x", 65) + `"}`
	for name, tc := range map[string]struct {
		method, path, body string
		code               int
	}{
		"bad body":       {http.MethodPut, "/v1/peers/" + id, `nope`, http.StatusBadRequest},
		"unknown token":  {http.MethodPut, "/v1/peers/nobody00", `{"token":"x"}`, http.StatusNotFound},
		"wrong token":    {http.MethodPut, "/v1/peers/" + id, `{"token":"wrong"}`, http.StatusUnprocessableEntity},
		"empty token":    {http.MethodPut, "/v1/peers/" + id, `{"token":" "}`, http.StatusUnprocessableEntity},
		"unknown alias":  {http.MethodPut, "/v1/peers/nobody00", `{"alias":"x"}`, http.StatusNotFound},
		"long alias":     {http.MethodPut, "/v1/peers/" + id, longAlias, http.StatusBadRequest},
		"unknown forget": {http.MethodDelete, "/v1/peers/nobody00", "", http.StatusNotFound},
	} {
		if w := sendPeers(t, here, tc.method, tc.path, tc.body); w.Code != tc.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	both := `{"token":"` + otherToken + `","alias":"Oficina"}`
	v := decodePeers(t, sendPeers(t, here, http.MethodPut, "/v1/peers/"+id, both))
	if v.Peers[0].Alias != "Oficina" {
		t.Fatalf("alias = %+v", v.Peers[0])
	}
	v = decodePeers(t, sendPeers(t, here, http.MethodDelete, "/v1/peers/"+id, ""))
	if len(v.Peers) != 0 {
		t.Fatalf("after forget: %+v", v.Peers)
	}
}

func TestAPairErrorOtherThanUnknownIsABadRequest(t *testing.T) {
	if peerStatus(errors.New("x")) != http.StatusBadRequest || peerStatus(peers.ErrUnknown) != http.StatusNotFound {
		t.Fatal("statuses")
	}
}

// proxied sends a request through here to the connected computer, as the page does: with the
// cookie, not the token.
func proxied(t *testing.T, here *Server, method, id, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, here.BaseURL()+"/v1/peers/"+id+"/proxy"+path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue(testToken)})
	req.Header.Set(browserHeader, "1")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestTheProxyOpensTheOtherComputersAPI(t *testing.T) {
	here, there := peerPair(t)
	id := connect(t, here, there)
	res := proxied(t, here, http.MethodGet, id, "/v1/sessions", "")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"sessions"`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	// A write reaches it too, as a bearer client: its own cookie and origin checks never see the
	// page's.
	res = proxied(t, here, http.MethodPut, id, "/v1/peers/self", `{"name":"x"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("chained to the other computer's own computers: %d", res.StatusCode)
	}
	// The other computer has no configuration file to save the language in, and says so: the
	// answer is its own, with this gateway's security headers once.
	res = proxied(t, here, http.MethodPut, id, "/v1/ui", `{"language":"es"}`)
	body, _ = io.ReadAll(res.Body)
	if res.StatusCode != http.StatusConflict || len(res.Header.Values("Content-Security-Policy")) != 1 {
		t.Fatalf("write: %d %s %v", res.StatusCode, body, res.Header)
	}
}

func TestTheProxyRefusesWhatIsNotConnectedOrNotAllowed(t *testing.T) {
	here, there := peerPair(t)
	if res := proxied(t, here, http.MethodGet, "nobody00", "/v1/sessions", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown: %d", res.StatusCode)
	}
	id := connect(t, here, there)
	for _, p := range []string{
		"/v1/webui/session", "/v1/shutdown", "/v1/peer/hello", "/v1/peers", "/other", "/v1/a/%2e%2e/peers",
	} {
		if res := proxied(t, here, http.MethodGet, id, p, ""); res.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
}

func TestPeerPathKeepsOnlyCleanAPIPaths(t *testing.T) {
	for in, want := range map[string]string{
		"v1/sessions":        "/v1/sessions",
		"v1/sessions/../x":   "",
		"v1/peers/x":         "",
		"v1/peersx":          "/v1/peersx",
		"v1/webui/session":   "",
		"v1/shutdown":        "",
		"health":             "",
		"v1/sessions/a/":     "",
		"v1/peer/hello":      "",
		"v1/peer-grants/x":   "",
		"v1/sessions/a/ws":   "/v1/sessions/a/ws",
		"v1/llm/providers/x": "/v1/llm/providers/x",
	} {
		if got := peerPath(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestAChangedTokenOrAnOfflineComputerIsABadGatewayNotALogout(t *testing.T) {
	here, there := peerPair(t)
	id := connect(t, here, there)
	if err := here.peers.Pair(id, "rotated", there.peers.Cert()); err != nil {
		t.Fatal(err)
	}
	res := proxied(t, here, http.MethodGet, id, "/v1/sessions", "")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "connect it again") {
		t.Fatalf("rotated: %d %s", res.StatusCode, body)
	}
	_ = there.Close(context.Background())
	res = proxied(t, here, http.MethodGet, id, "/v1/sessions", "")
	body, _ = io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "did not answer") {
		t.Fatalf("offline: %d %s", res.StatusCode, body)
	}
}

// tailnetScale is a Tailscale that also lists the tailnet's machines.
type tailnetScale struct {
	fakeTailscale
	listed atomic.Bool
}

func (f *tailnetScale) Peers(context.Context) []tailscale.Peer {
	f.listed.Store(true)
	return nil
}

func TestDiscoveryStartsWithTheTailnetWhenAsked(t *testing.T) {
	old := runDiscovery
	t.Cleanup(func() { runDiscovery = old })
	started := make(chan *peers.Discovery, 1)
	runDiscovery = func(d *peers.Discovery, _ context.Context) { started <- d }
	ts := &tailnetScale{}
	newTestServer(t, &fakeService{}, func(o *Options) {
		o.Discovery = true
		o.Tailscale = ts
	})
	d := <-started
	d.Tailnet(context.Background())
	if !ts.listed.Load() || d.Hello == nil {
		t.Fatal("the tailnet is not looked at")
	}
	newTestServer(t, &fakeService{}, func(o *Options) { o.Discovery = true })
	if d := <-started; d.Tailnet != nil {
		t.Fatal("a tailnet without Tailscale")
	}
}

func TestAnUnreadableComputersFileLeavesTheGatewayServing(t *testing.T) {
	dir := t.TempDir()
	srv, sink := startServer(t, Options{Listen: "127.0.0.1:0", Token: testToken, PeerFile: dir})
	if srv.peers == nil || srv.peers.Self().ID == "" {
		t.Fatal("no registry")
	}
	data, _ := os.ReadFile(sink.path)
	if !strings.Contains(string(data), "computers file") {
		t.Fatalf("log = %s", data)
	}
}

// tlsRequest is a request as another gateway sends it: over TLS, with srv's certificate, from addr.
func tlsRequest(t *testing.T, srv *Server, method, path, body, addr string) *http.Request {
	t.Helper()
	cert, err := x509.ParseCertificate(srv.peers.Certificate().Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	req.RemoteAddr = addr
	return req
}

func servePeer(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// helloOf is what the gateway srv would say if its id were id.
func helloOf(id string, srv *Server) peers.Hello {
	return peers.Hello{Protocol: peers.Protocol, ID: id, Name: "x", Port: int(netip.MustParseAddrPort(srv.Addr()).Port())}
}

// fastAsks makes a request to be let in checked on at once.
func fastAsks(t *testing.T, wait time.Duration) {
	t.Helper()
	oldEvery, oldWait := askEvery, askWait
	askEvery, askWait = 5*time.Millisecond, wait
	t.Cleanup(func() { askEvery, askWait = oldEvery, oldWait })
}

func eventuallyPeers(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// askAndAnswer has here ask there to be let in, checks both show the same code, and has there
// answer. It returns there's id here and here's own id.
func askAndAnswer(t *testing.T, here, there *Server, allow string) (id, self string) {
	t.Helper()
	v := decodePeers(t, sendPeers(t, here, http.MethodPost, "/v1/peers", `{"address":"`+there.Addr()+`"}`))
	if len(v.Peers) != 1 || v.Peers[0].Paired || v.Peers[0].Ask == nil || v.Peers[0].Ask.State != peers.AskWaiting {
		t.Fatalf("asking = %+v", v.Peers)
	}
	code, id := v.Peers[0].Ask.Code, v.Peers[0].ID
	other := decodePeers(t, sendPeers(t, there, http.MethodGet, "/v1/peers", ""))
	if len(other.Requests) != 1 || other.Requests[0].Code != code || other.Requests[0].ID != v.Self.ID {
		t.Fatalf("requests = %+v, code %q", other.Requests, code)
	}
	for name, tc := range map[string]struct{ path, body string }{
		"bad body": {"/v1/peer-requests/" + v.Self.ID, `nope`},
		"unknown":  {"/v1/peer-requests/nobody00", `{"allow":true}`},
	} {
		if w := sendPeers(t, there, http.MethodPost, tc.path, tc.body); w.Code == http.StatusOK {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	decodePeers(t, sendPeers(t, there, http.MethodPost, "/v1/peer-requests/"+v.Self.ID, `{"allow":`+allow+`}`))
	return id, v.Self.ID
}

func TestAComputerAsksAndIsLetInWithoutItsToken(t *testing.T) {
	fastAsks(t, time.Minute)
	here, there := peerPair(t)
	id, self := askAndAnswer(t, here, there, "true")
	eventuallyPeers(t, "let in", func() bool {
		p, _ := here.peers.Find(id)
		return p.Paired && p.Ask == nil
	})

	// Its own token, over its certificate: reads and writes go through as a bearer client's.
	res := proxied(t, here, http.MethodGet, id, "/v1/sessions", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("read: %d", res.StatusCode)
	}
	res = proxied(t, here, http.MethodPut, id, "/v1/ui", `{"language":"es"}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("write: %d", res.StatusCode)
	}
	g := decodePeers(t, sendPeers(t, there, http.MethodGet, "/v1/peers", "")).Grants
	if len(g) != 1 || g[0].ID != self {
		t.Fatalf("grants = %+v", g)
	}
	if w := sendPeers(t, there, http.MethodDelete, "/v1/peer-grants/nobody00", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown revoke: %d", w.Code)
	}
	decodePeers(t, sendPeers(t, there, http.MethodDelete, "/v1/peer-grants/"+self, ""))
	res = proxied(t, here, http.MethodGet, id, "/v1/sessions", "")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "connect it again") {
		t.Fatalf("revoked: %d %s", res.StatusCode, body)
	}

}

func TestAComputerAskedCanRefuse(t *testing.T) {
	fastAsks(t, time.Minute)
	here, there := peerPair(t)
	id, self := askAndAnswer(t, here, there, "false")
	// Asked again from its entry in the list: the request replaces the refused one.
	decodePeers(t, sendPeers(t, here, http.MethodPost, "/v1/peers/"+id+"/ask", ""))
	decodePeers(t, sendPeers(t, there, http.MethodPost, "/v1/peer-requests/"+self, `{"allow":false}`))
	eventuallyPeers(t, "denied", func() bool {
		p, _ := here.peers.Find(id)
		return p.Ask != nil && p.Ask.State == peers.AskDenied
	})
}

func TestARequestNobodyAnswersIsDropped(t *testing.T) {
	fastAsks(t, 50*time.Millisecond)
	here, there := peerPair(t)
	v := decodePeers(t, sendPeers(t, here, http.MethodPost, "/v1/peers", `{"address":"`+there.Addr()+`"}`))
	id := v.Peers[0].ID
	_ = there.Close(context.Background())
	eventuallyPeers(t, "dropped", func() bool {
		p, _ := here.peers.Find(id)
		return p.Ask == nil
	})
}

func TestAskingNeedsAComputerThatAnswers(t *testing.T) {
	here, there := peerPair(t)
	if w := sendPeers(t, here, http.MethodPost, "/v1/peers/nobody00/ask", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", w.Code)
	}
	// Found at an address where another computer answers now.
	here.peers.Saw(helloOf("peer0001", there),
		netip.MustParseAddrPort(there.Addr()), peers.SourceLAN)
	if w := sendPeers(t, here, http.MethodPost, "/v1/peers/peer0001/ask", ""); w.Code != http.StatusBadGateway {
		t.Fatalf("another computer: %d %s", w.Code, w.Body.String())
	}
	if w := sendPeers(t, here, http.MethodPut, "/v1/peers/peer0001", `{"token":"x"}`); w.Code != http.StatusBadGateway {
		t.Fatalf("another computer, token: %d %s", w.Code, w.Body.String())
	}
	// The other computer has too many requests waiting.
	for i := range peers.MaxRequests {
		if _, err := there.peers.Ask(fmt.Sprintf("peer%04d", i+10), "x", "", fmt.Sprintf("fp%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	w := sendPeers(t, here, http.MethodPost, "/v1/peers", `{"address":"`+there.Addr()+`"}`)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "too many") {
		t.Fatalf("busy: %d %s", w.Code, w.Body.String())
	}
}

func TestOnlyAGatewayOnTheNetworkCanAskToConnect(t *testing.T) {
	here, there := peerPair(t)
	asker := func(body, addr string) int {
		return servePeer(there, tlsRequest(t, here, http.MethodPost, peers.PairPath, body, addr)).Code
	}
	if c := asker(`{"id":"peer0001","name":"x"}`, "8.8.8.8:1"); c != http.StatusForbidden {
		t.Fatalf("public: %d", c)
	}
	if c := asker(`nope`, "192.168.1.5:1"); c != http.StatusBadRequest {
		t.Fatalf("bad body: %d", c)
	}
	if c := asker(`{"id":"x","name":"x"}`, "192.168.1.5:1"); c != http.StatusBadRequest {
		t.Fatalf("bad id: %d", c)
	}
	plain := httptest.NewRequest(http.MethodPost, peers.PairPath, strings.NewReader(`{}`))
	if c := servePeer(there, plain).Code; c != http.StatusForbidden {
		t.Fatalf("no certificate: %d", c)
	}
	answer := tlsRequest(t, here, http.MethodGet, peers.PairPath, "", "192.168.1.5:1")
	if c := servePeer(there, answer).Code; c != http.StatusNotFound {
		t.Fatalf("no request: %d", c)
	}
	if c := asker(`{"id":"peer0001","name":"x"}`, "192.168.1.5:1"); c != http.StatusAccepted {
		t.Fatalf("asked: %d", c)
	}
	_ = there.peers.Decide("peer0001", false)
	_, _ = there.peers.Answer(here.peers.Fingerprint())
	for i := range peers.MaxRequests {
		_, _ = there.peers.Ask(fmt.Sprintf("peer%04d", i+100), "x", "", fmt.Sprintf("fp%d", i))
	}
	if c := asker(`{"id":"peer0999","name":"y"}`, "192.168.1.6:1"); c != http.StatusTooManyRequests {
		t.Fatalf("busy: %d", c)
	}
}

func TestAnotherIdentityAtTheConnectedAddressIsRefused(t *testing.T) {
	here, there := peerPair(t)
	id := connect(t, here, there)
	if err := here.peers.Pair(id, otherToken, "another"); err != nil {
		t.Fatal(err)
	}
	res := proxied(t, here, http.MethodGet, id, "/v1/sessions", "")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "different identity") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func TestTheRunsInFlightAreCountedHereAndThere(t *testing.T) {
	old := runningEvery
	runningEvery = 0
	t.Cleanup(func() { runningEvery = old })
	here, there := peerPair(t)
	id := connect(t, here, there)
	// A found computer is not asked, and one whose token was refused keeps no count.
	here.peers.Saw(peers.Hello{Protocol: peers.Protocol, ID: "peer0002", Name: "x", Port: 1},
		netip.MustParseAddrPort("192.168.1.9:1"), peers.SourceLAN)
	conv, _ := there.lookup(DefaultSession)
	conv.stateMu.Lock()
	conv.running = true
	conv.stateMu.Unlock()
	t.Cleanup(func() {
		conv.stateMu.Lock()
		conv.running = false
		conv.stateMu.Unlock()
	})
	if v := decodePeers(t, sendPeers(t, there, http.MethodGet, "/v1/peers", "")); v.Self.Running != 1 {
		t.Fatalf("here = %+v", v.Self)
	}
	sendPeers(t, here, http.MethodGet, "/v1/peers", "")
	eventuallyPeers(t, "counted", func() bool {
		p, _ := here.peers.Find(id)
		return p.Running == 1
	})
	eventuallyPeers(t, "done", func() bool { return !here.runningBusy.Load() })
	_ = here.peers.Pair(id, "rotated", there.peers.Cert())
	here.peers.SetRunning(id, 5)
	here.readRunning(context.Background())
	if p, _ := here.peers.Find(id); p.Running != 5 {
		t.Fatalf("refused token changed the count: %+v", p)
	}
}
