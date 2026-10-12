// Package dnsfix gives the pure-Go resolver name servers on Android.
//
// The release binaries are built with CGO_ENABLED=0, so name resolution is Go's own
// resolver, which reads /etc/resolv.conf. Android has no such file and does its lookups
// through a system daemon only libc can reach, so without this every lookup goes to
// 127.0.0.1:53, where nothing answers, and every request to the model fails. Termux
// keeps a resolv.conf of its own under $PREFIX/etc; failing that, public resolvers are
// used.
package dnsfix

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// Fallback is used when no resolv.conf names a server.
var Fallback = []string{"8.8.8.8:53", "1.1.1.1:53"}

// Seams, so the Android branch can run in a test on any system.
var (
	goos     = runtime.GOOS
	readFile = os.ReadFile
	getenv   = os.Getenv
)

// Install replaces net.DefaultResolver on Android, and does nothing elsewhere or when the
// system does have an /etc/resolv.conf. It must run before the first network call.
func Install() {
	if servers, _ := plan(); servers != nil {
		net.DefaultResolver = Resolver(servers)
	}
}

// Describe says where name servers come from on this system, for `motita doctor`.
func Describe() string {
	_, source := plan()
	return source
}

// resolvConf is the resolver configuration a Unix system keeps its name servers in.
const resolvConf = "/etc/resolv.conf"

// plan decides which servers Install points the resolver at, and says why. Nil servers mean
// the system resolver is left as it is.
func plan() ([]string, string) {
	if _, err := readFile(resolvConf); err == nil {
		return nil, resolvConf
	}
	if goos != "android" {
		return nil, "no /etc/resolv.conf: Go falls back to 127.0.0.1:53"
	}
	if prefix := getenv("PREFIX"); prefix != "" {
		path := prefix + resolvConf
		if data, err := readFile(path); err == nil {
			if servers := Nameservers(data); len(servers) > 0 {
				return servers, path + " (" + strings.Join(servers, ", ") + ")"
			}
		}
	}
	return Fallback, "public resolvers (" + strings.Join(Fallback, ", ") + "): Termux has no resolv.conf"
}

// Nameservers returns the "nameserver" entries of a resolv.conf as host:port addresses.
func Nameservers(resolvConf []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(resolvConf))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "nameserver" && net.ParseIP(f[1]) != nil {
			out = append(out, net.JoinHostPort(f[1], "53"))
		}
	}
	return out
}

// Resolver is a pure-Go resolver that sends every query to servers, rotating to the next
// one on each dial so a dead server costs one attempt rather than all of them.
func Resolver(servers []string) *net.Resolver {
	var next atomic.Uint32
	d := net.Dialer{Timeout: 5 * time.Second}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			addr := servers[int(next.Add(1)-1)%len(servers)]
			return d.DialContext(ctx, network, addr)
		},
	}
}
