package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/peers"
)

// THE OTHER COMPUTERS, FROM THE TERMINAL.
//
// A computer is let into this one by a person who compares the code both pages show. A computer
// with no screen - a server reached over SSH or a VPN - has no page to show it on, so these
// commands are that page: `computers` lists who is asking, with the code, and `allow` and `deny`
// answer. The code is typed, not picked from a list, because comparing it is the whole check.

// computerAddrs are the addresses another computer reaches this one at; a variable so a test sets
// them, as a test host has whichever interfaces it happens to have.
var computerAddrs = lanAddresses

// localGateway is the running gateway, or the exit code after saying there is none.
func (op Options) localGateway(ctx context.Context) (gateway.Found, int, bool) {
	found, ok, err := op.discover(ctx)
	switch {
	case err != nil:
		fmt.Fprintf(op.Err, "%v\n", err)
		return found, ConfigError, false
	case !ok:
		fmt.Fprintln(op.Err, "no gateway is running: start it with `motita gateway start`")
		return found, ConfigError, false
	default:
		return found, Success, true
	}
}

// gatewayComputers lists the computers asking to connect to this one.
func (op Options) gatewayComputers(ctx context.Context) int {
	found, code, ok := op.localGateway(ctx)
	if !ok {
		return code
	}
	view, err := gateway.ReadPeerRequests(ctx, found.BaseURL, found.Token)
	if err != nil {
		fmt.Fprintf(op.Err, "the computers could not be read: %v\n", err)
		return ConfigError
	}
	fmt.Fprintf(op.Out, "this computer: %s\n", view.Name)
	if !view.Reachable {
		fmt.Fprintln(op.Out, "no other computer can reach it: the gateway answers this machine only.")
		fmt.Fprintln(op.Out, "run `motita gateway setup`, choose the local network, and start it again.")
		return Success
	}
	if addrs := computerAddrs(); len(addrs) > 0 {
		fmt.Fprintf(op.Out, "its addresses: %s (port %s)\n", strings.Join(addrs, ", "), portOf(found.BaseURL))
	}
	for _, g := range view.Grants {
		fmt.Fprintf(op.Out, "let in: %s\n", g.Name)
	}
	if len(view.Requests) == 0 {
		fmt.Fprintln(op.Out, "no computer is asking to connect. On the other one, open Add a computer,")
		fmt.Fprintln(op.Out, "type one of the addresses above and leave the token empty.")
		return Success
	}
	fmt.Fprintln(op.Out, "\nASKING TO CONNECT")
	for _, r := range view.Requests {
		fmt.Fprintf(op.Out, "  %s  %s, from %s\n", r.Code, r.Name, r.Address)
	}
	fmt.Fprintln(op.Out, "\nlet one in only if its page shows the same code: motita gateway allow <code>")
	return Success
}

// gatewayDecide answers the computer that asks with code.
func (op Options) gatewayDecide(ctx context.Context, code string, allow bool) int {
	found, exit, ok := op.localGateway(ctx)
	if !ok {
		return exit
	}
	view, err := gateway.ReadPeerRequests(ctx, found.BaseURL, found.Token)
	if err != nil {
		fmt.Fprintf(op.Err, "the computers could not be read: %v\n", err)
		return ConfigError
	}
	r, ok := requestWithCode(view.Requests, code)
	if !ok {
		fmt.Fprintf(op.Err, "no computer asks to connect with the code %s: `motita gateway computers` lists them\n", code)
		return ConfigError
	}
	if err := gateway.DecidePeerRequest(ctx, found.BaseURL, found.Token, r.ID, allow); err != nil {
		fmt.Fprintf(op.Err, "%s could not be answered: %v\n", r.Name, err)
		return ConfigError
	}
	if allow {
		fmt.Fprintf(op.Out, "%s is let in: it connects in a few seconds.\n", r.Name)
	} else {
		fmt.Fprintf(op.Out, "%s was turned away.\n", r.Name)
	}
	return Success
}

// requestWithCode is the request whose code is code, spaces aside.
func requestWithCode(requests []peers.Request, code string) (peers.Request, bool) {
	want := digitsOf(code)
	for _, r := range requests {
		if want != "" && digitsOf(r.Code) == want {
			return r, true
		}
	}
	return peers.Request{}, false
}

// digitsOf is s without anything but its digits.
func digitsOf(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
