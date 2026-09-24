// Package netrules decides which origins may reach the gateway.
//
// It is a package of its own because TWO packages need the same answer and neither may import the
// other: config validates the rules, and gateway applies them. config cannot import gateway (the
// cycle would not compile), and a rule set validated in one place and enforced in another is
// exactly the pair that drifts - the configuration accepted as "LAN only" while the middleware
// matches something else. The same reasoning already forced gatewayAddressIsLoopback to be written
// twice; one implementation is the version that cannot disagree with itself.
//
// The model is a firewall's, which is what an operator already knows how to reason about:
//
//   - the DEFAULT POLICY IS ACCEPT. A gateway with no rules serves everyone, the way a machine
//     with a fresh, empty firewall accepts everything;
//   - rules are ORDERED and the FIRST ONE THAT MATCHES decides. Adding a restriction is adding a
//     rule, not flipping a switch;
//   - loopback is ALWAYS allowed, whatever the rules say.
//
// That last one is not a convenience. The local interface and the local browser reach the gateway
// over loopback, so a rule set that locked loopback out would leave the operator unable to use the
// program they just configured - and, worse, unable to reach the gateway to fix it. An operator who
// writes `["!any"]` means "this machine only", not "break my terminal"; the honest reading of that
// intent is the rule below, and it is stated rather than implied.
package netrules

import (
	"fmt"
	"net/netip"
	"strings"
)

// Kind is how a rule matches an origin.
type Kind string

const (
	// KindAny matches every origin.
	KindAny Kind = "any"
	// KindLAN matches the private and link-local ranges: the addresses a machine on the same
	// network has. It is a named set rather than a list the operator has to look up, because
	// "let my phone in" should not require knowing what a /16 is.
	KindLAN Kind = "lan"
	// KindPrefix matches one address or one network.
	KindPrefix Kind = "prefix"
)

// Rule is one entry of the ordered rule set.
type Rule struct {
	// Deny says this rule refuses what it matches. An allow rule accepts it.
	Deny bool
	// Kind is how the rule matches.
	Kind Kind
	// Prefix is the network this rule matches, for KindPrefix.
	Prefix netip.Prefix
	// Text is the rule as the operator wrote it, trimmed of the leading "!". It is kept so that
	// every message about a rule - a log line, an announcement, an error - names back what the
	// person typed instead of a re-spelling of it.
	Text string
}

// String renders the rule the way it would be written in the configuration.
func (r Rule) String() string {
	if r.Deny {
		return "!" + r.Text
	}
	return r.Text
}

// Policy is an ordered set of rules. The zero value is an OPEN policy: no rules, so the default
// accept applies and every origin may connect.
type Policy struct {
	rules []Rule
}

// Open reports whether this policy restricts anything at all.
//
// It is what an announcement needs to tell the operator, and it is deliberately not derived from
// the rules by a caller: "no rules" and "rules that happen to allow everything" are the same
// outcome and should read the same everywhere.
func (p *Policy) Open() bool { return len(p.rules) == 0 }

// Describe renders the policy for a human: the words "every origin" when nothing is restricted,
// and the rule list otherwise, joined with commas.
func (p *Policy) Describe() string {
	if p.Open() {
		return "every origin"
	}
	parts := make([]string, 0, len(p.rules))
	for _, r := range p.rules {
		parts = append(parts, r.String())
	}
	return strings.Join(parts, ", ")
}

// privatePrefixes is NOT here on purpose, and the reason is worth keeping.
//
// The obvious implementation of `lan` is a table of prefixes to loop over, and an earlier draft of
// this file had one. It was removed because it duplicated an answer Go already gives: netip's
// IsPrivate covers 10/8, 172.16/12, 192.168/16 and the IPv6 unique-local range, and
// IsLinkLocalUnicast covers 169.254/16 and fe80::/10. A second table is a second place to be wrong,
// and the measured failure mode is the quiet one: a rule that stops matching an address the operator
// can see on their own network.
//
// The set `lan` means is therefore defined by matches(), and the tests assert it against real
// addresses rather than against a copy of the list.

// Parse reads the rule set out of the configuration, in order.
//
// An empty list is an OPEN policy and NOT an error: it is the documented default, and refusing it
// would make the shipped configuration invalid.
//
// Every failure names the offending spec. A rule set is applied by a middleware, so a typo in it
// fails silently at request time - the gateway simply refuses an origin the operator thought they
// had allowed - and the only moment the mistake can be caught and explained is here, while the
// configuration is being read.
func Parse(specs []string) (*Policy, error) {
	p := &Policy{}
	for _, spec := range specs {
		text := strings.TrimSpace(spec)
		if text == "" {
			// A blank entry is refused rather than skipped. A list in YAML where one line lost
			// its value is a real editing accident, and silently dropping it would apply a rule
			// set the operator never wrote.
			return nil, fmt.Errorf("an origin rule is empty: write one of any, lan, an address like 192.168.1.10, or a network like 192.168.0.0/16")
		}
		deny := strings.HasPrefix(text, "!")
		body := strings.TrimSpace(strings.TrimPrefix(text, "!"))
		if body == "" {
			return nil, fmt.Errorf("the origin rule %q has nothing after the \"!\": write !any, !lan, !192.168.1.10 or !192.168.0.0/16", text)
		}
		rule, err := parseOne(deny, body)
		if err != nil {
			return nil, err
		}
		p.rules = append(p.rules, rule)
	}
	return p, nil
}

// parseOne builds a single rule from its body.
func parseOne(deny bool, body string) (Rule, error) {
	switch strings.ToLower(body) {
	case "any", "all", "everyone":
		// "all" and "everyone" are accepted because each is what somebody types when they mean
		// this, and a refusal would be a spelling lesson rather than a fix.
		return Rule{Deny: deny, Kind: KindAny, Text: "any"}, nil
	case "lan", "local_network":
		return Rule{Deny: deny, Kind: KindLAN, Text: "lan"}, nil
	}

	// A network, when the spec carries a slash: a prefix is unambiguous and needs no keyword.
	if strings.Contains(body, "/") {
		prefix, err := netip.ParsePrefix(body)
		if err != nil {
			return Rule{}, fmt.Errorf("the origin rule %q is not a network: %v", body, err)
		}
		return Rule{Deny: deny, Kind: KindPrefix, Prefix: prefix.Masked(), Text: body}, nil
	}

	// An address, bare or with the explicit `ip:` prefix.
	addr, err := netip.ParseAddr(strings.TrimSpace(strings.TrimPrefix(body, "ip:")))
	if err != nil {
		return Rule{}, fmt.Errorf("the origin rule %q is not an address, a network, \"lan\" or \"any\"", body)
	}
	// A single address is carried as a host prefix so that one matching path serves both.
	return Rule{Deny: deny, Kind: KindPrefix, Prefix: netip.PrefixFrom(addr, addr.BitLen()), Text: body}, nil
}

// Allows reports whether an origin may connect.
//
// The order is the whole contract: the first rule that matches decides, and an origin no rule
// matches is allowed, because the default policy is accept.
func (p *Policy) Allows(addr netip.Addr) bool {
	// An unmappable address is treated as NOT allowed through the rules, so the loopback and
	// default checks below stay the only way in. An address this function cannot understand is
	// not one to grant access to, and netip only fails to unmap something that was never a real
	// address.
	origin, ok := unmap(addr)
	if !ok {
		return false
	}
	// Loopback is allowed before any rule is consulted: the local interface and the local browser
	// reach the gateway this way, and a rule set that locked it out would leave the operator
	// unable to use - or repair - the program they just configured.
	if origin.IsLoopback() {
		return true
	}
	for _, r := range p.rules {
		if r.matches(origin) {
			return !r.Deny
		}
	}
	// No rule matched: the default policy is accept, the way a machine with an empty firewall
	// table accepts everything.
	return true
}

// matches reports whether one rule covers an origin.
func (r Rule) matches(origin netip.Addr) bool {
	switch r.Kind {
	case KindAny:
		return true
	case KindLAN:
		if origin.IsPrivate() {
			// netip answers for both families' private ranges, including the IPv6 unique-local
			// one. The link-local check is separate because IsPrivate reports those as FALSE:
			// 169.254/16 and fe80::/10 are what a machine falls back to when nothing handed it
			// an address, and "let my phone in" has to include the phone that did.
			return true
		}
		return origin.IsLinkLocalUnicast()
	case KindPrefix:
		return r.Prefix.Contains(origin)
	default:
		// Unreachable: Parse builds only the three kinds above. False is the safe reading, and it
		// is here rather than a panic so that a rule set can never take the gateway down.
		return false
	}
}

// unmap reduces an address to the form the rules are written in.
//
// Two normalisations, and each one is a bug that was measured rather than imagined:
//
//   - the 4-in-6 form. A dual-stack listener reports an IPv4 client as an IPv4-mapped IPv6 address
//     (`::ffff:192.168.1.10`), and NOTHING matches it in that form: Prefix.Contains reports false
//     against every IPv4 network, so a `lan` or `192.168.0.0/16` rule would silently refuse every
//     IPv4 client on a machine that bound the wildcard - which is the default;
//   - the ZONE. `fe80::1%eth0` is a link-local address plus the interface it was seen on, and a
//     rule is written against the address. Keeping the zone would make one rule match or not
//     depending on which interface a packet arrived by, which is not what any of these rules mean.
func unmap(addr netip.Addr) (netip.Addr, bool) {
	if !addr.IsValid() {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}
