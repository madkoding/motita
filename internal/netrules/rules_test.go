package netrules

import (
	"net/netip"
	"strings"
	"testing"
)

// mustPolicy parses a rule set the test expects to be valid.
func mustPolicy(t *testing.T, specs ...string) *Policy {
	t.Helper()
	p, err := Parse(specs)
	if err != nil {
		t.Fatalf("Parse(%v): %v", specs, err)
	}
	return p
}

// --- the default: accept everything ------------------------------------------

// With NO rules the gateway serves everyone, and this is the documented default rather than an
// accident of the implementation: it is the shape of a machine with a fresh, empty firewall table.
// The operator restricts it by ADDING a rule, not by finding a switch first.
func TestNoRulesAcceptsEveryOrigin(t *testing.T) {
	p := mustPolicy(t)
	if !p.Open() {
		t.Fatal("a policy with no rules must report itself as open")
	}
	for _, origin := range []string{
		"127.0.0.1", "192.168.1.10", "10.0.0.5", "8.8.8.8", "203.0.113.9", "::1", "2606:4700::1",
	} {
		if !p.Allows(netip.MustParseAddr(origin)) {
			t.Fatalf("an open policy refused %s: a gateway with no rules serves everyone", origin)
		}
	}
}

// An empty YAML list is the default, not an error: refusing it would make the shipped
// configuration invalid.
func TestAnEmptyListIsTheDefaultAndNotAnError(t *testing.T) {
	for _, specs := range [][]string{nil, {}} {
		p, err := Parse(specs)
		if err != nil {
			t.Fatalf("Parse(%v): %v", specs, err)
		}
		if !p.Open() {
			t.Fatalf("Parse(%v) produced a closed policy", specs)
		}
	}
}

// --- loopback is always allowed ----------------------------------------------

// Loopback is allowed whatever the rules say, and it is the one unconditional rule.
//
// It is not a convenience: the local interface and the local browser reach the gateway over
// loopback, so a rule set that locked it out would leave the operator unable to use - or repair -
// the program they just configured. `!any` is how somebody says "this machine only", and the
// honest reading of that intent is what this test pins.
func TestLoopbackIsAlwaysAllowedEvenWhenEverythingElseIsDenied(t *testing.T) {
	p := mustPolicy(t, "!any")
	for _, origin := range []string{"127.0.0.1", "127.0.0.53", "::1", "::ffff:127.0.0.1"} {
		if !p.Allows(netip.MustParseAddr(origin)) {
			t.Fatalf("loopback (%s) was refused: the operator would lose their own interface", origin)
		}
	}
	// And everything else IS refused, so this cannot pass by denying nothing.
	for _, origin := range []string{"192.168.1.10", "8.8.8.8"} {
		if p.Allows(netip.MustParseAddr(origin)) {
			t.Fatalf("%s was allowed under !any", origin)
		}
	}
}

// Loopback wins even when a deny rule names it explicitly, because the rules are consulted AFTER
// it. This is the documented precedence and the reason is the same as above.
func TestLoopbackSurvivesADenyRuleThatNamesIt(t *testing.T) {
	p := mustPolicy(t, "!127.0.0.1")
	if !p.Allows(netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("loopback was refused by a rule naming it: it is allowed before any rule is read")
	}
}

// --- the first match decides --------------------------------------------------

// Rules are ORDERED and the first one that matches decides. That is what makes "allow one address,
// deny everything else" expressible at all, and it is how a firewall's ordered rule list behaves.
func TestTheFirstMatchingRuleDecides(t *testing.T) {
	p := mustPolicy(t, "192.168.1.10", "!any")

	if !p.Allows(netip.MustParseAddr("192.168.1.10")) {
		t.Fatal("the allow rule came first and was not honoured")
	}
	if p.Allows(netip.MustParseAddr("192.168.1.11")) {
		t.Fatal("an origin no rule matched before !any should have been denied by it")
	}
}

// The reverse order gives the reverse answer, which is what proves the order is real and not an
// accident of how allow and deny are stored.
func TestTheOrderIsTheContract(t *testing.T) {
	p := mustPolicy(t, "!any", "192.168.1.10")

	if p.Allows(netip.MustParseAddr("192.168.1.10")) {
		t.Fatal("!any matched first and must decide: the allow rule below it is unreachable")
	}
}

// An origin NO rule matches is allowed, because the default policy is accept. A deny list that is
// expected to lock everything else out would be a misunderstanding of the model, and the docs say
// so; this is the behaviour itself.
func TestAnOriginNoRuleMatchesIsAllowed(t *testing.T) {
	p := mustPolicy(t, "!192.168.1.10")
	if p.Allows(netip.MustParseAddr("192.168.1.10")) {
		t.Fatal("the deny rule did not deny")
	}
	if !p.Allows(netip.MustParseAddr("203.0.113.7")) {
		t.Fatal("an origin no rule mentions must be allowed: the default policy is accept")
	}
}

// --- lan ----------------------------------------------------------------------

// `lan` matches the private and link-local ranges in both families, which is the set an operator
// means by "my network": the machine's own neighbours, and nothing routed off it.
//
// The link-local cases are the ones worth stating: netip reports them as NOT private, so a `lan`
// built on IsPrivate alone would silently exclude a phone or printer that fell back to a 169.254
// address - a matching problem the operator would go looking for in their firewall.
func TestLANCoversThePrivateAndLinkLocalRanges(t *testing.T) {
	// Denied, not allowed: `lan` on its own is an ALLOW rule and everything it does not match
	// still falls through to the default accept, so the rule that actually restricts to a local
	// network is the negated one - and that is the shape an operator writes when they mean
	// "my network only".
	p := mustPolicy(t, "!lan", "lan")

	for _, origin := range []string{
		"10.0.0.1", "172.16.0.1", "172.31.255.254", "192.168.0.1", "192.168.100.72",
		"169.254.10.20",      // IPv4 link-local
		"fd00::1", "fc00::1", // IPv6 unique-local
		"fe80::1", // IPv6 link-local
	} {
		// `!lan` comes first, so these are refused; the trailing `lan` is unreachable for them.
		if p.Allows(netip.MustParseAddr(origin)) {
			t.Fatalf("!lan did not match %s, which is a private or link-local address", origin)
		}
	}

	// And it does NOT cover the public internet: a `lan` rule that quietly allowed everything
	// would be the worst possible failure of this feature. These match neither rule, so the
	// default accept lets them through - which is exactly what proves `!lan` is narrow.
	for _, origin := range []string{
		"8.8.8.8", "203.0.113.9", "100.64.0.1", // 100.64/10 is carrier-grade NAT, not private
		"2606:4700::1111",
	} {
		if !p.Allows(netip.MustParseAddr(origin)) {
			t.Fatalf("!lan matched %s, which is not on a local network", origin)
		}
	}
}

// The two spellings of the same rule mean the same thing, because each is what somebody types.
func TestLANSpellingsAreEquivalent(t *testing.T) {
	for _, spec := range []string{"lan", "LAN", "Local_Network"} {
		p := mustPolicy(t, spec)
		if !p.Allows(netip.MustParseAddr("192.168.1.5")) {
			t.Fatalf("%q did not behave as the lan rule", spec)
		}
	}
}

// --- addresses and networks ----------------------------------------------------

// A bare address, the explicit `ip:` form and a CIDR all work, because an operator will reach for
// whichever one they have in front of them.
func TestAddressesAndNetworks(t *testing.T) {
	cases := []struct {
		spec   string
		inside string
		out    string
	}{
		{"192.168.1.10", "192.168.1.10", "192.168.1.11"},
		{"ip:192.168.1.10", "192.168.1.10", "192.168.1.11"},
		{"192.168.0.0/16", "192.168.42.7", "192.169.0.1"},
		{"10.0.0.0/8", "10.255.255.1", "11.0.0.1"},
		{"2001:db8::/32", "2001:db8::5", "2001:db9::5"},
		// Not ::1: loopback is allowed before any rule is read, so a rule naming it can never be
		// observed to match and asserting on it would pin the wrong thing. See the loopback tests.
		{"2001:db8::1", "2001:db8::1", "2001:db8::2"},
	}

	for _, tc := range cases {
		// The prefix is the only rule, so anything outside it is still allowed by the default
		// accept - which means the assertion has to be on the DENY side to be meaningful.
		p := mustPolicy(t, "!"+tc.spec)
		if p.Allows(netip.MustParseAddr(tc.inside)) {
			t.Errorf("!%s did not match %s", tc.spec, tc.inside)
		}
		if !p.Allows(netip.MustParseAddr(tc.out)) {
			t.Errorf("!%s matched %s, which is outside it", tc.spec, tc.out)
		}
	}
}

// A network written with host bits set is MASKED, so the rule means the network the operator
// obviously intended instead of matching almost nothing.
func TestANetworkWithHostBitsIsMasked(t *testing.T) {
	p := mustPolicy(t, "!192.168.1.130/24")
	// .130 has host bits inside a /24; masked, the rule covers the whole /24.
	if p.Allows(netip.MustParseAddr("192.168.1.5")) {
		t.Fatal("192.168.1.130/24 was not masked to 192.168.1.0/24")
	}
}

// --- IPv4-mapped IPv6: the trap a wildcard bind walks straight into ---------------

// A dual-stack listener reports an IPv4 client as an IPv4-mapped IPv6 address, and NOTHING matches
// it in that form: Prefix.Contains answers false against every IPv4 network.
//
// This is the bug that would have shipped silently. The default bind is the wildcard, so on the
// default configuration every IPv4 client arrives as `::ffff:…`; without the unmap, a `lan` rule or
// any IPv4 network would refuse every one of them, and the gateway would look like it was
// misconfigured while the rules read correctly.
func TestAnIPv4MappedOriginMatchesIPv4Rules(t *testing.T) {
	for _, spec := range []string{"!lan", "!192.168.0.0/16", "!192.168.1.10"} {
		p := mustPolicy(t, spec)
		mapped := netip.MustParseAddr("::ffff:192.168.1.10")
		if p.Allows(mapped) {
			t.Errorf("%s did not match the same origin arriving as an IPv4-mapped address (%s)", spec, mapped)
		}
		// A mapped address outside the rule is still allowed, so the match is specific.
		if !p.Allows(netip.MustParseAddr("::ffff:8.8.8.8")) {
			t.Errorf("%s matched a mapped address outside it", spec)
		}
	}
}

// The loopback check sees through the mapping too: otherwise a local client on a wildcard bind
// would be treated as a stranger and could be refused by a rule that was meant for the network.
func TestMappedLoopbackIsRecognisedAsLoopback(t *testing.T) {
	p := mustPolicy(t, "!any")
	if !p.Allows(netip.MustParseAddr("::ffff:127.0.0.1")) {
		t.Fatal("an IPv4-mapped loopback address was not recognised as loopback")
	}
}

// --- zones are dropped ---------------------------------------------------------

// A link-local address arrives with the interface it was seen on (`fe80::1%eth0`). A rule is
// written against the ADDRESS, so the zone is dropped: keeping it would make the same rule match
// or not depending on which interface the packet came in by, which none of these rules mean.
func TestAZonedAddressMatchesTheSameRuleWithAndWithoutTheZone(t *testing.T) {
	p := mustPolicy(t, "!fe80::1")
	if p.Allows(netip.MustParseAddr("fe80::1%eth0")) {
		t.Fatal("a zoned link-local address did not match the rule written without a zone")
	}
	// And it still does not match a DIFFERENT address that happens to be zoned.
	if !p.Allows(netip.MustParseAddr("fe80::2%eth0")) {
		t.Fatal("the zone was treated as part of the address and matched the wrong rule")
	}
}

// --- parse failures name the offender -------------------------------------------

// Every rejection names the spec that caused it. A rule set is enforced by a middleware, so a typo
// in it fails silently at request time - the gateway just refuses an origin the operator believed
// they had allowed - and reading the configuration is the only moment the mistake can be caught
// and explained.
func TestBadRulesAreRejectedWithTheOffendingSpec(t *testing.T) {
	cases := []struct {
		spec  string
		names string
	}{
		{"", "empty"},
		{"   ", "empty"},
		{"!", "\"!\""},
		{"!   ", "\"!\""},
		{"banana", "banana"},
		{"ip:not-an-ip", "not-an-ip"},
		{"192.168.1.0/99", "99"},
		{"300.1.1.1", "300.1.1.1"},
	}

	for _, tc := range cases {
		_, err := Parse([]string{tc.spec})
		if err == nil {
			t.Errorf("Parse(%q) was accepted", tc.spec)
			continue
		}
		if !strings.Contains(err.Error(), tc.names) {
			t.Errorf("Parse(%q) said %q, which does not name %q", tc.spec, err, tc.names)
		}
	}
}

// A malformed rule is refused with the list of what a rule may be, so the fix is in the message.
func TestABadRuleSaysWhatARuleMayBe(t *testing.T) {
	_, err := Parse([]string{"nonsense"})
	if err == nil {
		t.Fatal("a nonsense rule was accepted")
	}
	for _, want := range []string{"address", "network", "lan", "any"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q does not mention %q", err, want)
		}
	}
}

// A blank entry in the middle of a list is refused rather than skipped: one line losing its value
// is a real editing accident, and dropping it silently would apply a rule set nobody wrote.
func TestABlankEntryInTheMiddleIsRefused(t *testing.T) {
	if _, err := Parse([]string{"lan", "", "!any"}); err == nil {
		t.Fatal("a blank entry among valid rules was accepted")
	}
}

// --- the defensive branches ------------------------------------------------------

// An address this package cannot understand is not one to grant access to, and it is refused
// through the rules rather than by the default accept.
//
// Unreachable through a real listener - netip only fails to unmap an address that was never valid -
// and that is exactly why it is asserted directly instead of left as a branch nobody has run.
func TestAnInvalidOriginIsRefusedThroughTheRules(t *testing.T) {
	if mustPolicy(t).Allows(netip.Addr{}) {
		t.Fatal("an invalid origin was allowed: an address that cannot be read is not one to grant")
	}
}

// The unknown-kind branch cannot be reached through Parse, which builds only the three known kinds.
// False is the safe reading and it is asserted so that adding a kind cannot silently start
// matching everything.
func TestAnUnknownRuleKindMatchesNothing(t *testing.T) {
	r := Rule{Kind: Kind("invented"), Text: "invented"}
	if r.matches(netip.MustParseAddr("192.168.1.10")) {
		t.Fatal("a rule of an unknown kind matched an origin")
	}
}

// --- reporting -------------------------------------------------------------------

// Open is the fact an announcement depends on, so it is asserted for both states.
func TestOpenReportsWhetherAnythingIsRestricted(t *testing.T) {
	if !mustPolicy(t).Open() {
		t.Fatal("no rules must report open")
	}
	if mustPolicy(t, "lan").Open() {
		t.Fatal("a policy with a rule must not report open")
	}
}

// Describe renders the policy for a human: the plain fact when nothing is restricted, and the
// rules as written otherwise - including the "!" that makes a deny a deny.
func TestDescribeRendersWhatTheOperatorWrote(t *testing.T) {
	cases := []struct {
		specs []string
		want  string
	}{
		{nil, "every origin"},
		{[]string{"!any"}, "!any"},
		{[]string{"lan", "ip:10.0.0.5", "!any"}, "lan, ip:10.0.0.5, !any"},
	}

	for _, tc := range cases {
		if got := mustPolicy(t, tc.specs...).Describe(); got != tc.want {
			t.Errorf("Describe() = %q, want %q", got, tc.want)
		}
	}
}

// A rule renders back the way it was written, which is what makes a log line or an error quoting
// it recognisable to the person who typed it.
//
// Asserted through Describe and not through the rule list: the rendering is reached the way the
// program reaches it (the refusal message and the announcement), so a rule that only renders
// correctly when read directly is not covered.
func TestARuleRendersBackAsWritten(t *testing.T) {
	// The keyword is canonicalised, because the rule IS that keyword; an address is kept verbatim.
	if got := mustPolicy(t, "LAN").Describe(); got != "lan" {
		t.Errorf("a LAN rule describes itself as %q, want the canonical %q", got, "lan")
	}
	if got := mustPolicy(t, "!192.168.1.10").Describe(); got != "!192.168.1.10" {
		t.Errorf("an address rule describes itself as %q, want it verbatim with its %q", got, "!")
	}
	// The "all"/"everyone" spellings read back as the canonical "any", so every spelling of the
	// same rule is reported identically.
	for _, spelling := range []string{"any", "ALL", "everyone"} {
		if got := mustPolicy(t, spelling).Describe(); got != "any" {
			t.Errorf("%q describes itself as %q, want the canonical %q", spelling, got, "any")
		}
	}
	// An alias for lan reads back as lan too.
	if got := mustPolicy(t, "local_network").Describe(); got != "lan" {
		t.Errorf("local_network describes itself as %q, want the canonical %q", got, "lan")
	}
}
