package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gateway"
)

// portOf reads the port out of a base URL for the exposure sentence.
//
// The fallback is unreachable through a real server, since BaseURL is built from the listener's own
// address - and that is exactly why it is asserted here rather than left as a branch nothing runs.
// The failure mode of guessing instead is a message that reads as if the gateway were on a port it
// is not, which is worse than saying nothing.
func TestPortOfReadsThePortAndSaysNothingWhenThereIsNone(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:7477": "7477",
		"http://[::1]:7477":     "7477",
		"http://localhost:9000": "9000",
		"http://127.0.0.1":      "",
		"not a url":             "",
		"":                      "",
	}
	for url, want := range cases {
		if got := portOf(url); got != want {
			t.Errorf("portOf(%q) = %q, want %q", url, got, want)
		}
	}
}

// The interface URL carries the token, so it belongs on the terminal of whoever ran the command
// - and NOT in the log file. A log is kept, copied and pasted into issues, and this URL IS the
// credential. `status` must therefore never print it: it is the command a user runs in front of
// someone else while asking "is it up?".
func TestStatusNeverPrintsTheToken(t *testing.T) {
	out := &syncBuffer{}
	_, address := fakeGatewayProcess(t, nil)

	op := gatewayTestOptions(t, out, "", "gateway", "status")
	op.ServiceFile = writeServiceFileFor(t, address)

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (output: %s)", code, Success, out.String())
	}
	if !strings.Contains(out.String(), "running") {
		t.Fatalf("status did not report a running gateway: %s", out.String())
	}
	if strings.Contains(out.String(), testToken) {
		t.Fatalf("`gateway status` printed the token:\n%s", out.String())
	}
}

// The announced URL must carry the token in its FRAGMENT.
//
// A fragment and not a query string, and that is the whole reason for this shape: a query is sent
// to the server, written into every intermediate log, and kept in the browser's history in full,
// while a fragment is stripped before the request is made and never appears in a Referer. It is
// the only way to put a secret in a URL without it travelling.
func TestTheAnnouncedURLCarriesTheTokenInItsFragment(t *testing.T) {
	var out strings.Builder
	announceWebUI(&out, gateway.Found{BaseURL: "http://127.0.0.1:7477", Token: testToken})

	got := out.String()
	want := "http://127.0.0.1:7477/#t=" + testToken
	if !strings.Contains(got, want) {
		t.Fatalf("the announced URL does not carry the fragment:\n%s\nwant to contain: %s", got, want)
	}
	if strings.Contains(got, "?t=") {
		t.Fatalf("the token is in a QUERY string, which travels to the server and into logs:\n%s", got)
	}
	// It must also say what the link is and what to do with it: a URL with a token in it is not
	// obviously something to open exactly once.
	if !strings.Contains(got, "interface") {
		t.Fatalf("the announcement does not say what the URL is for:\n%s", got)
	}
	if !strings.Contains(got, "open") {
		t.Fatalf("the announcement does not say what to do with the link:\n%s", got)
	}
}

// A gateway with no interface must not be announced as having one: promising a page that answers
// 404 sends the user hunting a fault that is really a setting.
func TestTheInterfaceIsAnnouncedOnlyWhenItIsOn(t *testing.T) {
	if !config.Default().Gateway.WebUI {
		t.Fatal("the interface is off by default, so the announce path would never run")
	}
	cfg := config.Default()
	cfg.Gateway.WebUI = false
	if cfg.Gateway.WebUI {
		t.Fatal("the interface cannot be turned off")
	}
}

// A network-reachable gateway must SAY SO, and say WHOM it serves.
//
// The message matters here for a reason that is easy to miss: the link above it is a LOOPBACK
// address (the wildcard has to be normalised to something callable), so a reader who is not told
// otherwise concludes the gateway is private - the exact opposite of what is true.
//
// The two facts are announced by TWO functions because they are two answers: announceExposure says
// how far it reaches and who may connect, announceWebUI hands over the link and redirects a reader
// who is on another machine to the address that works for them. This test covers the pair.
func TestTheAnnouncementSaysWhenTheGatewayIsReachableFromTheNetwork(t *testing.T) {
	var out strings.Builder
	found := gateway.Found{
		BaseURL:   "http://127.0.0.1:7477",
		Token:     testToken,
		Reachable: true,
		Allow:     "every origin",
	}
	announceExposure(&out, found)
	announceWebUI(&out, found)

	got := out.String()
	if !strings.Contains(got, "every interface") {
		t.Fatalf("a network-reachable gateway did not say where it is listening:\n%s", got)
	}
	// The rules are reported because the operator cannot otherwise tell a gateway that serves the
	// whole network from one narrowed to a single machine.
	if !strings.Contains(got, "gateway.allow") {
		t.Fatalf("the announcement does not say who may connect:\n%s", got)
	}
	if !strings.Contains(got, "EVERY origin") {
		t.Fatalf("the open default is not called out, which is the case the operator must notice:\n%s", got)
	}
	// The absence of TLS is stated where it is actionable rather than buried in the docs: this is
	// the moment the operator decides whether to keep it open.
	if !strings.Contains(got, "TLS") {
		t.Fatalf("the exposure is not qualified with the lack of TLS:\n%s", got)
	}
	// And it still hands over the working link, which is the point of the announcement.
	if !strings.Contains(got, "http://127.0.0.1:7477/#t="+testToken) {
		t.Fatalf("the link was lost while adding the warning:\n%s", got)
	}
	// The reader is redirected from the local link, and handed one that works from where they are.
	// Both halves matter: without the first they try the loopback address and blame the network,
	// and without the second they have to work the address out themselves - which is how a page
	// ends up reporting "not connected" with nothing to explain it.
	if !strings.Contains(got, "only works on this machine") {
		t.Fatalf("the loopback link is not qualified for a remote reader:\n%s", got)
	}
	if !strings.Contains(got, "from another machine") {
		t.Fatalf("a reachable gateway does not tell a remote reader where to go:\n%s", got)
	}
}

// A LOOPBACK gateway announces nothing about exposure: over-reporting is what gets a firewall rule
// opened for no reason, and it would make the warning meaningless where it counts.
func TestALoopbackAnnouncementCarriesNoExposureWarning(t *testing.T) {
	var out strings.Builder
	found := gateway.Found{BaseURL: "http://127.0.0.1:7477", Token: testToken}
	announceExposure(&out, found)
	announceWebUI(&out, found)

	if out.String() != "" && strings.Contains(out.String(), "every interface") {
		t.Fatalf("a loopback gateway announced an exposure it does not have:\n%s", out.String())
	}
	if strings.Contains(out.String(), "TLS") {
		t.Fatalf("a loopback gateway was qualified with a TLS warning it does not need:\n%s", out.String())
	}
	// The link is still handed over: it is the credential's handoff and works locally.
	if !strings.Contains(out.String(), "http://127.0.0.1:7477/#t="+testToken) {
		t.Fatalf("the link is missing:\n%s", out.String())
	}
}

// A RULE SET that restricts origins is reported as itself, and not through the open-default text.
//
// This is the assertion that would catch a gateway reporting "every origin" while enforcing
// something else, which is the failure an operator would never think to check for.
func TestARestrictedAnnouncementNamesTheRules(t *testing.T) {
	var out strings.Builder
	announceExposure(&out, gateway.Found{
		BaseURL:   "http://127.0.0.1:7477",
		Token:     testToken,
		Reachable: true,
		Allow:     "lan, !any",
	})

	got := out.String()
	if !strings.Contains(got, "gateway.allow: lan, !any") {
		t.Fatalf("the effective rules are not reported:\n%s", got)
	}
	if strings.Contains(got, "EVERY origin") {
		t.Fatalf("a restricted gateway announced itself as open:\n%s", got)
	}
}

// A found gateway with no token cannot produce a working link, and a link without the token is a
// page that opens on a 401 with nothing to explain it. Saying nothing is the honest outcome.
func TestNothingIsAnnouncedWithoutAToken(t *testing.T) {
	var out strings.Builder
	announceWebUI(&out, gateway.Found{BaseURL: "http://127.0.0.1:7477"})
	if out.String() != "" {
		t.Fatalf("a link was announced with no token to put in it:\n%s", out.String())
	}
}

// A network-reachable gateway hands over a link that WORKS from another machine.
//
// This is the assertion the old announcement failed: it printed a loopback URL plus a sentence
// telling the reader to substitute the host's address themselves. That is a networking question
// asked at the moment the operator is least equipped to answer it, and the symptom is a page that
// reports "not connected" with nothing to explain why - the token was in a link nobody opened.
func TestANetworkReachableAnnouncementOffersALANLink(t *testing.T) {
	var out strings.Builder
	found := gateway.Found{
		BaseURL:   "http://127.0.0.1:7477",
		Token:     testToken,
		Reachable: true,
		Allow:     "every origin",
	}
	announceExposure(&out, found)
	announceWebUI(&out, found)

	got := out.String()
	// The loopback link is still printed: it is the one a browser ON this machine needs.
	if !strings.Contains(got, "http://127.0.0.1:7477/#t="+testToken) {
		t.Fatalf("the local link was lost:\n%s", got)
	}
	lan := lanAddresses()
	if len(lan) == 0 {
		// This host may genuinely have no network address. The branch that says so is asserted
		// instead, so the test is meaningful on either kind of machine.
		if !strings.Contains(got, "no network address to offer") {
			t.Fatalf("with no address to offer, the announcement does not say so:\n%s", got)
		}
		return
	}
	for _, addr := range lan {
		want := "http://" + addr + ":7477/#t=" + testToken
		if !strings.Contains(got, want) {
			t.Errorf("the announcement does not offer %s:\n%s", want, got)
		}
	}
	// And it no longer asks the operator to work the address out for themselves.
	if strings.Contains(got, "same fragment - the link above") {
		t.Errorf("the announcement still asks the operator to derive the address:\n%s", got)
	}
}

// A LOOPBACK gateway offers NO network address, because there is none that would answer.
//
// Offering one would send the reader to an address the gateway is not bound to, which reads as a
// broken machine rather than a deliberately private one.
func TestALoopbackAnnouncementOffersNoLANLink(t *testing.T) {
	var out strings.Builder
	found := gateway.Found{BaseURL: "http://127.0.0.1:7477", Token: testToken}
	announceExposure(&out, found)
	announceWebUI(&out, found)

	got := out.String()
	for _, addr := range lanAddresses() {
		if strings.Contains(got, "http://"+addr+":7477") {
			t.Errorf("a loopback-only gateway offered the network address %s:\n%s", addr, got)
		}
	}
	if strings.Contains(got, "from another machine") {
		t.Errorf("a loopback-only gateway invited another machine:\n%s", got)
	}
}

// lanAddresses answers addresses a client could actually use, and leaves out the ones that do not
// work as written.
//
// Asserted against the REAL interfaces rather than a fixture, because that is the property: whatever
// this host has, the list must never carry loopback, the unspecified address, or a link-local one
// that needs a zone a URL cannot hold.
func TestLANAddressesAreUsableAsWritten(t *testing.T) {
	for _, addr := range lanAddresses() {
		switch {
		case strings.HasPrefix(addr, "127."), addr == "[::1]", addr == "::1":
			t.Errorf("lanAddresses returned loopback (%s): every machine resolves that to itself", addr)
		case addr == "0.0.0.0", addr == "[::]", addr == "::":
			t.Errorf("lanAddresses returned the unspecified address (%s), which is not one to type", addr)
		case strings.HasPrefix(addr, "169.254."), strings.HasPrefix(addr, "[fe80:"):
			t.Errorf("lanAddresses returned a link-local address (%s), which needs a zone a URL cannot carry", addr)
		case strings.Contains(addr, ":") && !strings.HasPrefix(addr, "["):
			t.Errorf("lanAddresses returned a bare IPv6 address (%s): a URL needs it bracketed", addr)
		}
	}
}

// The list is STABLE and IPv4 LEADS.
//
// Stability so two runs on one machine print the same thing: an address list that reorders itself
// looks like the machine changed networks. IPv4 first because that is what a LAN almost always uses,
// and a list whose first entry is an IPv6 address reads as if that were the answer to type.
func TestLANAddressesAreOrderedWithIPv4First(t *testing.T) {
	got := lanAddresses()
	seenV6 := false
	for _, addr := range got {
		isV4 := !strings.HasPrefix(addr, "[")
		if !isV4 {
			seenV6 = true
			continue
		}
		if seenV6 {
			t.Fatalf("lanAddresses returned %v: an IPv4 address follows an IPv6 one", got)
		}
	}
	// And it is a real ordering, not an accident of this host having only one family.
	if !sort.SliceIsSorted(got, func(i, j int) bool {
		i4, j4 := !strings.HasPrefix(got[i], "["), !strings.HasPrefix(got[j], "[")
		if i4 != j4 {
			return i4
		}
		return got[i] < got[j]
	}) {
		t.Fatalf("lanAddresses returned %v, which is not in the announced order", got)
	}
}

// A bridge or a veth pair is NOT offered, because nothing outside this machine can reach it.
//
// This is the difference between a helpful list and a misleading one: on a host running containers
// the docker bridges outnumber the real interfaces, and an operator picking the wrong entry sees a
// page that will not load from an address that "obviously" is the host's.
func TestVirtualInterfacesAreNotOffered(t *testing.T) {
	original := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = original })
	interfaceAddrs = func(iface net.Interface) ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("172.17.0.1"), Mask: net.CIDRMask(16, 32)}}, nil
	}

	got := lanAddressesOf([]net.Interface{
		{Name: "docker0", Flags: net.FlagUp},
		{Name: "br-1a2b3c", Flags: net.FlagUp},
		{Name: "veth9f8e", Flags: net.FlagUp},
		{Name: "virbr0", Flags: net.FlagUp},
		{Name: "lxcbr0", Flags: net.FlagUp},
		{Name: "tun0", Flags: net.FlagUp},
		{Name: "vnet3", Flags: net.FlagUp},
		{Name: "tap7", Flags: net.FlagUp},
	})
	if len(got) != 0 {
		t.Fatalf("lanAddressesOf offered %v, which no client outside this machine can reach", got)
	}

	// And a physical interface of the same shape still comes through, so the filter is a name test
	// and not a blanket refusal.
	got = lanAddressesOf([]net.Interface{{Name: "enp3s0", Flags: net.FlagUp}})
	if len(got) != 1 || got[0] != "172.17.0.1" {
		t.Fatalf("a physical interface was dropped: %v", got)
	}
}

// The interface list is read through an injectable seam, so the branches a healthy machine cannot
// reach are asserted instead of assumed.
//
// Every case below is one an operator would meet: a machine with no network at all, an interface
// that is down, and the loopback and link-local entries every machine has.
func TestLANAddressesSkipWhatCannotBeUsed(t *testing.T) {
	// An interface list that cannot be read is no address to offer, not an error to report.
	original := listInterfaces
	t.Cleanup(func() { listInterfaces = original })
	listInterfaces = func() ([]net.Interface, error) { return nil, errors.New("no interfaces") }
	if got := lanAddresses(); got != nil {
		t.Fatalf("lanAddresses returned %v when the interfaces could not be read", got)
	}
	listInterfaces = original

	// The real filters, driven with a hand-built list. Loopback and down are the two every machine
	// has, and link-local is the one that looks usable but needs a zone a URL cannot carry.
	got := lanAddressesOf([]net.Interface{
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
		{Name: "down0", Flags: 0},
		{Name: "lan0", Flags: net.FlagUp},
	})
	for _, addr := range got {
		if strings.HasPrefix(addr, "127.") || strings.HasPrefix(addr, "169.254.") || strings.HasPrefix(addr, "[fe80:") {
			t.Errorf("lanAddressesOf returned %s, which is not usable as written", addr)
		}
	}

	// And an empty interface list is an empty address list rather than a panic.
	if got := lanAddressesOf(nil); len(got) != 0 {
		t.Fatalf("lanAddressesOf(nil) = %v, want no addresses", got)
	}
}

// A gateway that reaches the network but has NO address to offer says so, instead of leaving a
// reader with a loopback link and no way to work out what to type.
//
// Reachable with an empty address list is the real case of a machine whose only interface is
// loopback but which binds the wildcard: nothing can reach it from outside anyway, so a sentence is
// the honest answer rather than an invented address.
func TestAReachableGatewayWithNoAddressSaysSo(t *testing.T) {
	original := listInterfaces
	t.Cleanup(func() { listInterfaces = original })
	// Loopback only, which is every machine behind a network that has not come up yet.
	listInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: "lo", Flags: net.FlagUp | net.FlagLoopback}}, nil
	}

	var out strings.Builder
	announceWebUI(&out, gateway.Found{
		BaseURL:   "http://127.0.0.1:7477",
		Token:     testToken,
		Reachable: true,
	})

	got := out.String()
	if !strings.Contains(got, "no network address to offer") {
		t.Fatalf("a reachable gateway with no usable address did not say so:\n%s", got)
	}
	// And it still hands over the local link, which is the one thing that does work.
	if !strings.Contains(got, "http://127.0.0.1:7477/#t="+testToken) {
		t.Fatalf("the local link was lost:\n%s", got)
	}
}

// usableAddressesOf is asserted against addresses built by hand, which is the only reliable way to
// reach the entries that must be REFUSED: a test host has whichever interfaces it happens to have,
// and on a healthy machine none of the bad shapes are present.
func TestUsableAddressesFilterOutWhatAURLCannotCarry(t *testing.T) {
	// A net.Addr that is not an IPNet carries no address, and net.Interface.Addrs can return one.
	other := net.Addr(&net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 80})

	got := usableAddressesOf([]net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.1.10"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("10.0.0.5"), Mask: net.CIDRMask(8, 32)},
		// Every one of these must be dropped: each is an address that does not work as written.
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
		&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("fd00::1"), Mask: net.CIDRMask(64, 128)},
		other,
	})

	want := []string{"192.168.1.10", "10.0.0.5", "[fd00::1]"}
	if len(got) != len(want) {
		t.Fatalf("usableAddressesOf = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("usableAddressesOf = %v, want %v", got, want)
		}
	}
	// The IPv6 entry is BRACKETED, which is what keeps its colons from reading as a port separator.
	if !strings.Contains(strings.Join(got, " "), "[fd00::1]") {
		t.Errorf("a usable IPv6 address is not bracketed: %v", got)
	}
}

// An interface whose addresses cannot be read is SKIPPED, not fatal, and the ones that work are
// still offered.
//
// The branch cannot be provoked on a healthy machine, so it is injected - the same reason the
// service-file writer's syscalls are variables.
func TestAnInterfaceThatCannotBeReadIsSkipped(t *testing.T) {
	originalInterfaces, originalAddrs := listInterfaces, interfaceAddrs
	t.Cleanup(func() { listInterfaces, interfaceAddrs = originalInterfaces, originalAddrs })

	listInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Name: "broken0", Flags: net.FlagUp},
			{Name: "lan0", Flags: net.FlagUp},
		}, nil
	}
	// Only the broken one fails; the walk must carry on past it.
	interfaceAddrs = func(iface net.Interface) ([]net.Addr, error) {
		if iface.Name == "broken0" {
			return nil, errors.New("the interface went away")
		}
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.10"), Mask: net.CIDRMask(24, 32)}}, nil
	}

	got := lanAddresses()
	if len(got) != 1 || got[0] != "192.168.1.10" {
		t.Fatalf("lanAddresses() = %v, want the one readable address", got)
	}
}

func TestTheInterfaceSettingSurvivesAnUnreadableConfiguration(t *testing.T) {
	op := Options{}
	if op.webUIEnabled(flags{configPath: filepath.Join(t.TempDir(), "not-there.yaml")}) {
		t.Fatal("an unreadable configuration was read as having an interface")
	}

	// A configuration file that turns the interface off must be believed, or the command would
	// hand out a link to a page its own gateway answers 404 on.
	off := filepath.Join(t.TempDir(), "off.yaml")
	if err := os.WriteFile(off, []byte("gateway:\n  webui: false\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if op.webUIEnabled(flags{configPath: off}) {
		t.Fatal("a configuration with webui: false was ignored")
	}

	// And a configuration that does not mention it keeps the default, which is on: the interface
	// arriving with the gateway is the point of it, and silence in a file must not turn it off.
	on := filepath.Join(t.TempDir(), "on.yaml")
	if err := os.WriteFile(on, []byte("agent:\n  log_level: info\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !op.webUIEnabled(flags{configPath: on}) {
		t.Fatal("a configuration that does not mention the interface turned it off")
	}

	// The seam is consulted first, which is what the announcement tests rely on.
	op.InterfaceEnabled = func() bool { return false }
	if op.webUIEnabled(flags{}) {
		t.Fatal("the seam was ignored")
	}
}

// The interface setting is resolved from the SAME file the gateway will read, and this is the
// whole point of the test.
//
// `gateway start` with no -config used to ask this question of fl.configPath alone - an empty
// string - so the answer came from the DEFAULTS, where the interface is on, while the service it
// spawned read ~/.motita/motita.yaml, where the operator had turned it off. The command
// handed out a link and the gateway answered 404 on it. The symptom is a user hunting a fault that
// is really a setting, so the setting has to be read from the file the child inherits.
func TestTheInterfaceSettingIsReadFromTheFileTheGatewayWillRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The environment is cleared so what is under test is the FILE: a developer with
	// MOTITA_GATEWAY_WEBUI exported would otherwise decide this test's outcome.
	t.Setenv("MOTITA_GATEWAY_WEBUI", "")

	writeHomeConfig(t, home, "gateway:\n  webui: false\n")

	op := Options{}
	if op.webUIEnabled(flags{}) {
		t.Fatal("the home configuration turned the interface off and it was read as on: " +
			"`gateway start` would hand out a link to a page its own gateway answers 404 on")
	}
}

// And a home file that says nothing keeps the default, which is on: the interface arriving with
// the gateway is the point of it, and silence in a file must not turn it off.
func TestTheDefaultIsAnnouncedWhenTheHomeFileSaysNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOTITA_GATEWAY_WEBUI", "")

	writeHomeConfig(t, home, "agent:\n  log_level: info\n")

	if op := (Options{}); !op.webUIEnabled(flags{}) {
		t.Fatal("a configuration that does not mention the interface turned it off")
	}
}

// The environment applies to the service being spawned, so it applies here too. spawnDetached
// leaves cmd.Env nil, which means the child inherits this process's environment: a resolution that
// skipped the variable would announce a page the child was told to stop serving.
func TestTheEnvironmentReachesTheInterfaceAnnouncement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOTITA_GATEWAY_WEBUI", "false")

	if op := (Options{}); op.webUIEnabled(flags{}) {
		t.Fatal("MOTITA_GATEWAY_WEBUI=false was ignored when deciding whether to announce")
	}

	// And it reaches it with no configuration file at all, which is the container case: the
	// variable is the only thing that says anything.
	t.Setenv("MOTITA_GATEWAY_WEBUI", "true")
	if op := (Options{}); !op.webUIEnabled(flags{}) {
		t.Fatal("MOTITA_GATEWAY_WEBUI=true did not turn the announcement on")
	}
}

// A malformed value answers the safe way - claim nothing - rather than guessing or panicking. The
// gateway that the child brings up will refuse it loudly; this question only decides whether to
// print a link.
func TestAMalformedInterfaceSettingAnnouncesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MOTITA_GATEWAY_WEBUI", "maybe")

	if op := (Options{}); op.webUIEnabled(flags{}) {
		t.Fatal("a malformed boolean was read as \"on\": nothing should be announced when the " +
			"setting cannot be understood")
	}
}

// An explicit -config still wins over the home, because naming a file is the most specific
// instruction a user can give. The resolution is shared with `run`, so this is asserted here as
// well as there: the two must not drift.
func TestAnExplicitConfigDecidesTheAnnouncementOverTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOTITA_GATEWAY_WEBUI", "")

	// The home says OFF and the named file says ON.
	writeHomeConfig(t, home, "gateway:\n  webui: false\n")

	named := filepath.Join(t.TempDir(), "elegido.yaml")
	if err := os.WriteFile(named, []byte("gateway:\n  webui: true\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if op := (Options{}); !op.webUIEnabled(flags{configPath: named}) {
		t.Fatal("the named configuration did not decide the announcement")
	}
}

// The user-facing shape of the bug, end to end: `gateway start` with a home configuration that
// turns the interface off must not print the link.
//
// Asserted through the real command rather than through webUIEnabled alone, because the failure was
// never in the predicate - it was in which file the predicate was asked about - and only the
// command's output shows the two halves reading the same one.
func TestStartDoesNotAnnounceAnInterfaceItsGatewayWillNotServe(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	_, address := fakeGatewayProcess(t, nil)

	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.SpawnGateway = func(context.Context, spawnSpec) error {
		return gateway.WriteServiceFile(servicePath, gateway.ServiceFile{
			Address: address, Token: testToken, PID: 4242, Owned: false,
		})
	}

	// gatewayTestOptions pins HOME to a temporary directory, so the file written here is the one
	// the command resolves - the same one the spawned service would read.
	t.Setenv("MOTITA_GATEWAY_WEBUI", "")
	writeHomeConfig(t, os.Getenv("HOME"), "gateway:\n  webui: false\n")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (output: %s)", code, Success, out.String())
	}
	if strings.Contains(out.String(), "the interface is at") {
		t.Fatalf("a gateway configured with webui: false was announced with an interface link:\n%s",
			out.String())
	}
	// The gateway itself is still reported: turning the interface off is a setting, not a failure.
	if !strings.Contains(out.String(), "the gateway is running") {
		t.Fatalf("the gateway was not reported:\n%s", out.String())
	}
}

// And the other half of the same property: with the interface on - the default - the link IS
// printed, so this test cannot pass by never announcing anything.
func TestStartAnnouncesTheInterfaceItsGatewayWillServe(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	_, address := fakeGatewayProcess(t, nil)

	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.SpawnGateway = func(context.Context, spawnSpec) error {
		return gateway.WriteServiceFile(servicePath, gateway.ServiceFile{
			Address: address, Token: testToken, PID: 4242, Owned: false,
		})
	}
	t.Setenv("MOTITA_GATEWAY_WEBUI", "")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (output: %s)", code, Success, out.String())
	}
	if !strings.Contains(out.String(), "the interface is at") {
		t.Fatalf("the interface was not announced for a gateway that serves it:\n%s", out.String())
	}
}

// writeHomeConfig writes the motita home's own configuration file, creating the home directory.
func writeHomeConfig(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(home, ".motita", "motita.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeServiceFileFor publishes a service file naming an already-running fake gateway.
func writeServiceFileFor(t *testing.T, address string) string {
	t.Helper()
	path := t.TempDir() + "/gateway.json"
	if err := gateway.WriteServiceFile(path, gateway.ServiceFile{
		Address: address, Token: testToken, PID: 4242, Owned: false,
	}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}
	return path
}
