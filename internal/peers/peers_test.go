package peers

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clock is a settable time for the registry.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newReg(t *testing.T) (*Registry, *clock) {
	t.Helper()
	r, err := Open("", "box")
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Unix(1_000_000, 0)}
	r.Now = c.now
	return r, c
}

func hello(id, name string) Hello {
	return Hello{Protocol: Protocol, ID: id, Name: name, Version: "v1", Port: 7477}
}

func ap(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

func TestOpenInMemoryMakesAnIdentityNamedAfterTheHost(t *testing.T) {
	r, _ := newReg(t)
	self := r.Self()
	if !validID(self.ID) || self.Name != "box" || self.Hostname != "box" {
		t.Fatalf("self = %+v", self)
	}
	r2, _ := Open("", "  ")
	if r2.Self().Name != "motita" {
		t.Fatalf("no host name: %+v", r2.Self())
	}
}

func TestOpenKeepsTheIdentityInAPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers", "peers.json")
	r, err := Open(path, "box")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", info, err)
	}
	again, err := Open(path, "box")
	if err != nil || again.Self().ID != r.Self().ID {
		t.Fatalf("reopened: %v %v", again.Self(), err)
	}
}

func TestOpenRefusesAFileItCannotRead(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("{"), 0o600)
	if _, err := Open(bad, "box"); err == nil || !strings.Contains(err.Error(), "not a computers file") {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := Open(dir, "box"); err == nil {
		t.Fatal("a directory was read as the file")
	}
}

func TestSaveFailuresAreReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "peers.json")
	r, err := Open(path, "box")
	if err != nil {
		t.Fatal(err)
	}
	// The temporary file's place is taken by a directory.
	_ = os.Mkdir(path+".tmp", 0o700)
	_ = os.WriteFile(filepath.Join(path+".tmp", "x"), nil, 0o600)
	if err := r.SetName("a"); err == nil {
		t.Fatal("write over a directory succeeded")
	}
	_ = os.RemoveAll(path + ".tmp")
	// The file's own place is taken by a directory that is not empty.
	_ = os.Remove(path)
	_ = os.Mkdir(path, 0o700)
	_ = os.WriteFile(filepath.Join(path, "x"), nil, 0o600)
	if err := r.SetName("b"); err == nil {
		t.Fatal("rename over a directory succeeded")
	}
	// The directory itself cannot be made.
	_ = os.RemoveAll(filepath.Join(dir, "sub"))
	_ = os.WriteFile(filepath.Join(dir, "sub"), nil, 0o600)
	if err := r.SetName("c"); err == nil {
		t.Fatal("mkdir under a file succeeded")
	}
}

func TestHelloValidity(t *testing.T) {
	good := hello("abcdef12", "x")
	if !good.Valid() {
		t.Fatal("good refused")
	}
	for name, h := range map[string]Hello{
		"protocol": {Protocol: "other", ID: "abcdef12", Port: 1},
		"short id": {Protocol: Protocol, ID: "abc", Port: 1},
		"long id":  {Protocol: Protocol, ID: strings.Repeat("a", 65), Port: 1},
		"bad char": {Protocol: Protocol, ID: "abcdef/1", Port: 1},
		"no port":  {Protocol: Protocol, ID: "abcdef12"},
		"big port": {Protocol: Protocol, ID: "abcdef12", Port: 70000},
	} {
		if h.Valid() {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSetNameTrimsResetsAndRefusesALongName(t *testing.T) {
	r, _ := newReg(t)
	if err := r.SetName("  my   laptop "); err != nil || r.Self().Name != "my laptop" {
		t.Fatalf("%v %+v", err, r.Self())
	}
	if err := r.SetName(strings.Repeat("x", 65)); err == nil {
		t.Fatal("long name accepted")
	}
	_ = r.SetName("")
	if r.Self().Name != "box" {
		t.Fatalf("reset: %+v", r.Self())
	}
}

func TestSawIgnoresSelfMalformedAndPublicAddresses(t *testing.T) {
	r, _ := newReg(t)
	if r.Saw(hello(r.Self().ID, "me"), ap("192.168.1.2:7477"), SourceLAN) {
		t.Fatal("self listed")
	}
	if r.Saw(Hello{}, ap("192.168.1.2:7477"), SourceLAN) {
		t.Fatal("malformed listed")
	}
	if r.Saw(hello("peer0001", "x"), ap("8.8.8.8:7477"), SourceLAN) {
		t.Fatal("internet address listed")
	}
	if len(r.List()) != 0 {
		t.Fatalf("list = %+v", r.List())
	}
}

func TestSawPrefersIPv6AndForgetsAfterTheTTL(t *testing.T) {
	r, c := newReg(t)
	if !r.Saw(hello("peer0001", "desk"), ap("[::ffff:192.168.1.5]:7477"), SourceLAN) {
		t.Fatal("first sighting not new")
	}
	if r.Saw(hello("peer0001", "desk"), ap("[fe80::1%eth0]:7477"), SourceLAN) {
		t.Fatal("second sighting new")
	}
	if p, _ := r.Find("peer0001"); p.Address != "192.168.1.5:7477" || !p.Online {
		t.Fatalf("v4 over link-local: %+v", p)
	}
	r.Saw(hello("peer0001", "desk"), ap("[fd00::5]:7477"), SourceLAN)
	if p, _ := r.Find("peer0001"); p.Address != "[fd00::5]:7477" {
		t.Fatalf("v6 not preferred: %+v", p)
	}
	// Two of the same rank: the latest wins.
	c.t = c.t.Add(time.Second)
	r.Saw(hello("peer0001", "desk"), ap("[fd00::6]:7477"), SourceLAN)
	if p, _ := r.Find("peer0001"); p.Address != "[fd00::6]:7477" {
		t.Fatalf("latest not preferred: %+v", p)
	}
	c.t = c.t.Add(TTL + time.Second)
	if _, ok := r.Find("peer0001"); ok {
		t.Fatal("stale peer still listed")
	}
}

func TestTheLANNeverOverridesABetterSource(t *testing.T) {
	r, _ := newReg(t)
	r.Saw(hello("peer0001", "desk"), ap("192.168.1.5:7477"), SourceLAN)
	r.Saw(hello("peer0001", "desk"), ap("[fd7a:115c:a1e0::5]:7477"), SourceTailscale)
	r.Saw(hello("peer0001", "desk"), ap("192.168.1.5:7477"), SourceLAN)
	if r.SourceOf("peer0001") != SourceTailscale {
		t.Fatalf("source = %s", r.SourceOf("peer0001"))
	}
	if r.SourceOf("nobody00") != SourceManual {
		t.Fatal("unknown source")
	}
}

func TestPairRenameTargetAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	r, _ := Open(path, "box")
	if err := r.Pair("peer0001", "tok", "fp"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
	r.Saw(hello("peer0001", "desk"), ap("192.168.1.5:7477"), SourceLAN)
	if _, ok := r.Target("peer0001"); ok {
		t.Fatal("unpaired has a target")
	}
	if err := r.Pair("peer0001", " ", "fp"); err == nil {
		t.Fatal("empty token accepted")
	}
	if err := r.Pair("peer0001", "tok", "fp"); err != nil {
		t.Fatal(err)
	}
	if err := r.Pair("peer0001", "tok2", "fp"); err != nil {
		t.Fatal(err)
	}
	if tg, ok := r.Target("peer0001"); !ok || tg != (Target{Address: "192.168.1.5:7477", Token: "tok2", Cert: "fp"}) {
		t.Fatalf("target = %+v", tg)
	}
	if err := r.Rename("nobody00", "x"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if err := r.Rename("peer0001", strings.Repeat("x", 65)); err == nil {
		t.Fatal("long alias")
	}
	if err := r.Rename("peer0001", "Office"); err != nil {
		t.Fatal(err)
	}
	// A new name, version and address are written down.
	h := hello("peer0001", "desk2")
	h.Version = "v2"
	r.Saw(h, ap("192.168.1.6:7477"), SourceLAN)
	again, _ := Open(path, "box")
	p, ok := again.Find("peer0001")
	if !ok || p.Name != "desk2" || p.Alias != "Office" || p.Version != "v2" || !p.Paired || p.Online {
		t.Fatalf("kept = %+v", p)
	}
	if err := r.Forget("nobody00"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if err := r.Forget("peer0001"); err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 0 {
		t.Fatalf("after forget: %+v", r.List())
	}
}

func TestListOrdersPairedFirstThenByName(t *testing.T) {
	r, _ := newReg(t)
	r.Saw(hello("peer000b", "beta"), ap("192.168.1.2:1"), SourceLAN)
	r.Saw(hello("peer000a", "Alpha"), ap("192.168.1.3:1"), SourceLAN)
	r.Saw(hello("peer000c", "zulu"), ap("192.168.1.4:1"), SourceLAN)
	r.Saw(hello("peer000d", "zulu"), ap("192.168.1.5:1"), SourceLAN)
	_ = r.Pair("peer000c", "t", "fp")
	var got []string
	for _, p := range r.List() {
		got = append(got, p.ID)
	}
	if strings.Join(got, ",") != "peer000c,peer000a,peer000b,peer000d" {
		t.Fatalf("order = %v", got)
	}
}

func TestStaleListsConnectedPeersNotHeardFrom(t *testing.T) {
	r, c := newReg(t)
	r.Saw(hello("peer0001", "desk"), ap("192.168.1.5:7477"), SourceLAN)
	_ = r.Pair("peer0001", "t", "fp")
	r.Saw(hello("peer0002", "nas"), ap("192.168.1.6:7477"), SourceLAN)
	_ = r.Pair("peer0002", "t", "fp")
	r.mu.Lock()
	r.doc.Peers[1].Address = ""
	r.mu.Unlock()
	if s := r.Stale(); len(s) != 0 {
		t.Fatalf("fresh listed: %v", s)
	}
	c.t = c.t.Add(TTL/2 + time.Second)
	if s := r.Stale(); len(s) != 1 || s["peer0001"] != "192.168.1.5:7477" {
		t.Fatalf("stale = %v", s)
	}
}

func TestAllowedIsThePrivateNetworksOnly(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.1": true, "10.0.0.1": true, "172.16.0.1": true, "100.100.1.1": true,
		"fd7a:115c:a1e0::1": true, "fe80::1": true, "127.0.0.1": true, "::1": true,
		"::ffff:10.0.0.1": true, "8.8.8.8": false, "2001:4860::1": false, "100.128.0.1": false,
	} {
		if Allowed(netip.MustParseAddr(addr)) != want {
			t.Errorf("%s: want %v", addr, want)
		}
	}
}

func TestOpenMakesACertificateOnceAndReplacesABrokenOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	r, _ := Open(path, "box")
	if len(r.Fingerprint()) != 64 || len(r.Certificate().Certificate) != 1 {
		t.Fatalf("fingerprint %q", r.Fingerprint())
	}
	again, _ := Open(path, "box")
	if again.Fingerprint() != r.Fingerprint() {
		t.Fatal("the certificate changed on reopening")
	}
	again.mu.Lock()
	again.doc.SelfKey = "broken"
	_ = again.save()
	again.mu.Unlock()
	third, err := Open(path, "box")
	if err != nil || third.Fingerprint() == r.Fingerprint() || third.Self().ID != r.Self().ID {
		t.Fatalf("broken key: %v", err)
	}
}

func TestPairCodeIsTheSameOnBothSides(t *testing.T) {
	a, b := PairCode("aaa", "bbb"), PairCode("bbb", "aaa")
	if a != b || len(a) != 7 || a[3] != ' ' {
		t.Fatalf("%q %q", a, b)
	}
	if PairCode("aaa", "ccc") == a {
		t.Fatal("another certificate gives the same code")
	}
}

func TestRunningAndAsksShowInTheList(t *testing.T) {
	r, c := newReg(t)
	r.Saw(hello("peer0001", "desk"), ap("192.168.1.5:7477"), SourceLAN)
	r.Saw(hello("peer0002", "nas"), ap("192.168.1.6:7477"), SourceLAN)
	_ = r.Pair("peer0001", "t", "fp")
	r.SetRunning("peer0001", 3)
	r.SetAsk("peer0002", Ask{Code: "123 456", State: AskWaiting})
	if p, _ := r.Find("peer0001"); p.Running != 3 {
		t.Fatalf("running = %+v", p)
	}
	if p, _ := r.Find("peer0002"); p.Ask == nil || p.Ask.Code != "123 456" {
		t.Fatalf("ask = %+v", p)
	}
	r.SetAsk("peer0002", Ask{})
	if p, _ := r.Find("peer0002"); p.Ask != nil {
		t.Fatalf("ask kept: %+v", p)
	}
	// A computer gone quiet shows no runs: they cannot be read.
	c.t = c.t.Add(TTL + time.Second)
	if p, _ := r.Find("peer0001"); p.Running != 0 || p.Online {
		t.Fatalf("offline = %+v", p)
	}
	_ = r.Forget("peer0001")
}

func TestARequestToConnectIsAnsweredOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	r, _ := Open(path, "box")
	c := &clock{t: time.Unix(1_000_000, 0)}
	r.Now = c.now
	if _, err := r.Ask("bad", "x", "", "fp"); err == nil {
		t.Fatal("bad id accepted")
	}
	code, err := r.Ask("peer0001", "laptop", "192.168.1.5:5000", "fp1")
	if err != nil || code != PairCode(r.Fingerprint(), "fp1") {
		t.Fatalf("%q %v", code, err)
	}
	if st, _ := r.Answer("fp1"); st != AskWaiting {
		t.Fatalf("state = %q", st)
	}
	if st, _ := r.Answer("unknown"); st != "" {
		t.Fatalf("unknown = %q", st)
	}
	if q := r.Requests(); len(q) != 1 || q[0].Name != "laptop" || q[0].Code != code {
		t.Fatalf("requests = %+v", q)
	}
	if err := r.Decide("nobody00", true); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if err := r.Decide("peer0001", true); err != nil {
		t.Fatal(err)
	}
	st, token := r.Answer("fp1")
	if st != AskAllowed || len(token) != 64 {
		t.Fatalf("%q %q", st, token)
	}
	if st, _ := r.Answer("fp1"); st != "" {
		t.Fatal("the token was handed out twice")
	}
	if !r.Granted(token, "fp1") || r.Granted(token, "fp2") || r.Granted("", "fp1") || r.Granted(token, "") {
		t.Fatal("the token is not tied to its certificate")
	}
	again, _ := Open(path, "box")
	if g := again.Grants(); len(g) != 1 || g[0].ID != "peer0001" || g[0].Name != "laptop" {
		t.Fatalf("kept grants = %+v", g)
	}
	// Let in again: the new token replaces the old one.
	_, _ = r.Ask("peer0001", "laptop", "", "fp1")
	_ = r.Decide("peer0001", true)
	_, token = r.Answer("fp1")
	if len(r.Grants()) != 1 || !r.Granted(token, "fp1") {
		t.Fatalf("grants = %+v", r.Grants())
	}
	// Asked again and denied this time.
	_, _ = r.Ask("peer0001", "laptop", "", "fp1")
	_ = r.Decide("peer0001", false)
	if st, _ := r.Answer("fp1"); st != AskDenied {
		t.Fatalf("denied = %q", st)
	}
	if err := r.Revoke("nobody00"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if err := r.Revoke("peer0001"); err != nil || r.Granted(token, "fp1") {
		t.Fatalf("revoked: %v", err)
	}
}

func TestRequestsExpireAndAreBounded(t *testing.T) {
	r, c := newReg(t)
	_, _ = r.Ask("peer0001", "first", "", "fp1")
	c.t = c.t.Add(time.Second)
	_, _ = r.Ask("peer0002", "second", "", "fp2")
	if q := r.Requests(); len(q) != 2 || q[0].ID != "peer0001" {
		t.Fatalf("order = %+v", q)
	}
	for i := 3; i <= MaxRequests; i++ {
		if _, err := r.Ask(fmt.Sprintf("peer%04d", i), "x", "", fmt.Sprintf("fp%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Ask("peer0099", "x", "", "fp99"); !errors.Is(err, ErrBusy) {
		t.Fatalf("over the limit: %v", err)
	}
	// Asking again replaces the request rather than counting twice.
	if _, err := r.Ask("peer0001", "first", "", "fp1"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(AskTTL + time.Minute)
	if len(r.Requests()) != 0 {
		t.Fatal("expired requests listed")
	}
	if err := r.Decide("peer0001", true); !errors.Is(err, ErrUnknown) {
		t.Fatalf("expired decided: %v", err)
	}
	if st, _ := r.Answer("fp1"); st != "" {
		t.Fatalf("expired answered %q", st)
	}
	if _, err := r.Ask("peer0099", "x", "", "fp99"); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
}
