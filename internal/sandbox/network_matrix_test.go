package sandbox

import (
	"context"
	"flag"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// THE NETWORK ISOLATION, ON A REAL MACHINE (ci.yml, "network isolation").
//
// The tests next to this one choose the kernel's answer; this one asks the machine it runs on and
// checks the promise against it: a command that is not approved cannot reach the network, an
// approved one can, and where the machine cannot isolate, the request is said to be not applied
// rather than claimed. It runs only when MOTITA_NETISO_EXPECT says what this machine must do:
//
//	isolated     a command without approval has only a loopback of its own
//	unavailable  isolation is reported as not applied and commands keep the network
//
// so a runner image that changes under us (Ubuntu turning the user namespace restriction on, a new
// macOS) turns the job red instead of quietly changing what the setting means.

const (
	netIsoDialExit = 7 // the helper could not connect
	netIsoOKExit   = 0 // the helper connected
)

// TestNetIsoDial is the command the matrix runs inside the sandbox: not a test of its own. With
// "-- loopback" it listens on the loopback and dials itself; with "-- <host:port>" it dials that.
func TestNetIsoDial(t *testing.T) {
	arg := flag.Arg(0)
	if arg == "" {
		t.Skip("the helper of TestNetworkIsolationOnThisMachine")
	}
	target := arg
	if arg == "loopback" {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(netIsoDialExit)
		}
		defer func() { _ = l.Close() }()
		target = l.Addr().String()
	}
	c, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		os.Exit(netIsoDialExit)
	}
	_ = c.Close()
	os.Exit(netIsoOKExit)
}

// outboundAddress is an address of this machine that is not the loopback, which only a command
// with the machine's network can reach.
func outboundAddress(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() && !ip.IP.IsLinkLocalUnicast() {
			return ip.IP.String()
		}
	}
	t.Skip("this machine has no address besides the loopback")
	return ""
}

func TestNetworkIsolationOnThisMachine(t *testing.T) {
	expect := os.Getenv("MOTITA_NETISO_EXPECT")
	if expect == "" {
		t.Skip("MOTITA_NETISO_EXPECT is not set: this runs in the network isolation CI job")
	}
	if expect != "isolated" && expect != "unavailable" {
		t.Fatalf("MOTITA_NETISO_EXPECT = %q, want isolated or unavailable", expect)
	}

	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	remote := net.JoinHostPort(outboundAddress(t), port)

	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{NoNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	dial := func(approved bool, target string) int {
		t.Helper()
		out, _, code, err := s.Run(context.Background(), execx.Request{
			Command:  os.Args[0],
			Args:     []string{"-test.run=^TestNetIsoDial$", "--", target},
			Approved: approved,
			Timeout:  30 * time.Second,
		})
		if err != nil {
			t.Fatalf("running the helper: %v\n%s", err, out)
		}
		if code != netIsoOKExit && code != netIsoDialExit {
			t.Fatalf("the helper did not run (exit %d):\n%s", code, out)
		}
		return code
	}

	switch expect {
	case "isolated":
		if !NetworkIsolationAvailable() || !s.IsolatesNetwork() {
			t.Fatalf("expected isolation here, but it is unavailable: %v", networkIsolationError())
		}
		if got := dial(false, remote); got != netIsoDialExit {
			t.Error("a command that is not approved reached the machine's network")
		}
		if got := dial(false, "loopback"); got != netIsoOKExit {
			t.Error("a command without a network lost its own loopback")
		}
		if len(s.NotApplied()) != 0 {
			t.Errorf("isolation applied, yet reported as not: %v", s.NotApplied())
		}
	case "unavailable":
		if NetworkIsolationAvailable() || s.IsolatesNetwork() {
			t.Fatal("expected isolation to be unavailable here, but it is claimed")
		}
		if got := strings.Join(s.NotApplied(), "|"); !strings.Contains(got, "network isolation") {
			t.Errorf("isolation not applied, but not said: %q", got)
		}
		if err := s.SetIsolateNetwork(true); err == nil {
			t.Error("SetIsolateNetwork(true) succeeded where the machine cannot isolate")
		}
		if got := dial(false, remote); got != netIsoOKExit {
			t.Error("commands were cut off from the network although isolation is not applied")
		}
	}
	if got := dial(true, remote); got != netIsoOKExit {
		t.Error("an approved command did not keep the network")
	}
}
