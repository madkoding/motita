package dnsfix

import (
	"context"
	"errors"
	"net"
	"os"
	"reflect"
	"testing"
)

func TestNameserversKeepsOnlyValidEntries(t *testing.T) {
	conf := []byte("# comment\nnameserver 8.8.4.4\nnameserver\nnameserver not-an-ip\nsearch lan\nnameserver 2001:4860:4860::8888\n")
	got := Nameservers(conf)
	want := []string{"8.8.4.4:53", "[2001:4860:4860::8888]:53"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Nameservers = %v, want %v", got, want)
	}
}

func TestResolverDialsTheConfiguredServersInTurn(t *testing.T) {
	var seen []string
	for i := 0; i < 2; i++ {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		seen = append(seen, pc.LocalAddr().String())
	}
	r := Resolver(seen)
	for i := 0; i < 3; i++ {
		c, err := r.Dial(context.Background(), "udp", "127.0.0.1:53")
		if err != nil {
			t.Fatal(err)
		}
		if got := c.RemoteAddr().String(); got != seen[i%2] {
			t.Errorf("dial %d went to %s, want %s", i, got, seen[i%2])
		}
		c.Close()
	}
}

// install runs Install as if on goos, with files standing in for the file system.
func install(t *testing.T, os_ string, prefix string, files map[string]string) *net.Resolver {
	t.Helper()
	oldGoos, oldRead, oldEnv, oldDefault := goos, readFile, getenv, net.DefaultResolver
	t.Cleanup(func() { goos, readFile, getenv, net.DefaultResolver = oldGoos, oldRead, oldEnv, oldDefault })
	goos = os_
	getenv = func(string) string { return prefix }
	readFile = func(name string) ([]byte, error) {
		if s, ok := files[name]; ok {
			return []byte(s), nil
		}
		return nil, os.ErrNotExist
	}
	sentinel := &net.Resolver{}
	net.DefaultResolver = sentinel
	Install()
	if net.DefaultResolver == sentinel {
		return nil
	}
	return net.DefaultResolver
}

func TestInstallLeavesOtherSystemsAlone(t *testing.T) {
	if r := install(t, "linux", "", nil); r != nil {
		t.Fatal("Install changed the resolver on linux")
	}
}

func TestInstallLeavesAnAndroidWithResolvConfAlone(t *testing.T) {
	if r := install(t, "android", "", map[string]string{"/etc/resolv.conf": "nameserver 9.9.9.9\n"}); r != nil {
		t.Fatal("Install changed the resolver although /etc/resolv.conf exists")
	}
}

func TestInstallUsesTermuxResolvConf(t *testing.T) {
	const host = "127.0.0.1"
	r := install(t, "android", "/data/termux/usr", map[string]string{"/data/termux/usr/etc/resolv.conf": "nameserver " + host + "\n"})
	if r == nil || !r.PreferGo {
		t.Fatal("Install did not set a Go resolver on Android")
	}
	c, err := r.Dial(context.Background(), "udp", "ignored:53")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.RemoteAddr().String(); got != host+":53" {
		t.Errorf("dialled %s, want %s:53", got, host)
	}
}

func TestInstallFallsBackToPublicResolvers(t *testing.T) {
	for _, prefix := range []string{"", "/nowhere"} {
		r := install(t, "android", prefix, nil)
		if r == nil {
			t.Fatalf("prefix %q: Install did not set a resolver", prefix)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := r.Dial(ctx, "udp", "x:53")
		var op *net.OpError
		if !errors.As(err, &op) || op.Addr == nil || op.Addr.String() != Fallback[0] {
			t.Errorf("prefix %q: dial error %v, want one naming %s", prefix, err, Fallback[0])
		}
	}
}
