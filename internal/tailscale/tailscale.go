// Package tailscale finds out whether Tailscale is on this machine and publishes the gateway
// inside the tailnet with `tailscale serve`.
//
// It is the private alternative to opening the gateway to a network. `tailscale serve` puts an
// https proxy on this machine's tailnet name that only the operator's own devices can reach, and
// forwards to the gateway on loopback: the socket stays on 127.0.0.1, the token travels encrypted,
// and the browser gets the secure origin it needs for the microphone and notifications. Funnel,
// which publishes to the whole internet, is deliberately not offered.
//
// Everything goes through the `tailscale` command line rather than the local API socket: the
// command is the documented interface, it carries the operator's own permissions, and its errors
// ("Access denied", "not logged in") are already written for a person.
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// Status is what this machine's Tailscale looks like, as far as the gateway cares.
type Status struct {
	// Installed is true when the tailscale command was found.
	Installed bool `json:"installed"`
	// Running is true when the daemon is up and logged in (BackendState "Running").
	Running bool `json:"running"`
	// State is the daemon's BackendState ("Running", "NeedsLogin", "Stopped"...), empty when the
	// command is missing or could not answer.
	State string `json:"state"`
	// DNSName is this machine's name in the tailnet, without the trailing dot.
	DNSName string `json:"dns_name,omitempty"`
	// Serving is true when `tailscale serve` already forwards to the target that was asked about.
	Serving bool `json:"serving"`
	// URL is the https address the tailnet reaches this machine at, when it is running.
	URL string `json:"url,omitempty"`
	// Error is why the status could not be read, for a person to see.
	Error string `json:"error,omitempty"`
}

// HTTPSPort is the tailnet port the gateway is published on: the https default, so the address is
// the bare machine name.
const HTTPSPort = "443"

// timeout bounds every call to the command. Detection runs while a gateway starts and while a
// settings page loads, and a wedged daemon must not hold either of them.
const timeout = 5 * time.Second

// Client runs the tailscale command. The zero value is not usable; New returns the real one.
type Client struct {
	// LookPath finds the command, exec.LookPath in production.
	LookPath func(string) (string, error)
	// Run executes the command and returns its combined output.
	Run func(ctx context.Context, bin string, args ...string) ([]byte, error)
}

// New returns a client that runs the real command.
func New() *Client {
	return &Client{LookPath: exec.LookPath, Run: runCommand}
}

// runCommand is exec.CommandContext with the output collected, out of Client so a test replaces it.
func runCommand(ctx context.Context, bin string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, bin, args...).CombinedOutput()
}

// statusDoc is the part of `tailscale status --json` that is read.
type statusDoc struct {
	BackendState string `json:"BackendState"`
	Self         *struct {
		DNSName string `json:"DNSName"`
	} `json:"Self"`
}

// serveDoc is the part of `tailscale serve status --json` that is read.
type serveDoc struct {
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

// Detect reports whether Tailscale is installed and running, and whether it already serves target
// (an http URL, see Target). It never fails: what it could not find out is in Status.Error.
func (c *Client) Detect(ctx context.Context, target string) Status {
	bin, err := c.LookPath("tailscale")
	if err != nil {
		return Status{}
	}
	st := Status{Installed: true}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := c.Run(ctx, bin, "status", "--json")
	var doc statusDoc
	// `status` exits non-zero when the daemon is stopped or logged out but still prints the JSON,
	// so the document is read whatever the exit code, and the error only matters without one.
	if jerr := json.Unmarshal(out, &doc); jerr != nil {
		st.Error = commandError(err, out, jerr)
		return st
	}
	st.State = doc.BackendState
	if doc.Self != nil {
		st.DNSName = strings.TrimSuffix(doc.Self.DNSName, ".")
	}
	st.Running = st.State == "Running"
	if !st.Running {
		return st
	}
	if st.DNSName != "" {
		st.URL = "https://" + st.DNSName
	}
	st.Serving = c.serving(ctx, bin, target)
	return st
}

// serving reports whether the serve configuration already forwards to target.
func (c *Client) serving(ctx context.Context, bin, target string) bool {
	out, err := c.Run(ctx, bin, "serve", "status", "--json")
	if err != nil {
		return false
	}
	var doc serveDoc
	if json.Unmarshal(out, &doc) != nil {
		return false
	}
	for _, web := range doc.Web {
		for _, h := range web.Handlers {
			if sameTarget(h.Proxy, target) {
				return true
			}
		}
	}
	return false
}

// Serve publishes target inside the tailnet on https, in the background (it survives this process
// and a reboot, because tailscaled keeps the configuration).
func (c *Client) Serve(ctx context.Context, target string) error {
	return c.do(ctx, "serve", "--bg", "--https="+HTTPSPort, target)
}

// Unserve removes the https publication again.
func (c *Client) Unserve(ctx context.Context) error {
	return c.do(ctx, "serve", "--https="+HTTPSPort, "off")
}

// do runs one command that changes the serve configuration.
func (c *Client) do(ctx context.Context, args ...string) error {
	bin, err := c.LookPath("tailscale")
	if err != nil {
		return errors.New("tailscale is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := c.Run(ctx, bin, args...)
	if err != nil {
		msg := commandError(err, out, nil)
		if strings.Contains(strings.ToLower(msg), "access denied") {
			// The usual first failure on Linux: the daemon only takes serve changes from root or
			// from the user named as its operator, and the fix is one command, once.
			msg += OperatorHint
		}
		return errors.New(msg)
	}
	return nil
}

// OperatorHint is the fix for "Access denied" from tailscale serve, appended to that error.
const OperatorHint = ". Allow your user to change it, once: sudo tailscale set --operator=$USER"

// commandError is the sentence for a failed call: the command's own words when it said any,
// which is where "Access denied" and the fix for it are.
func commandError(err error, out []byte, parse error) string {
	if msg := strings.TrimSpace(string(out)); msg != "" && err != nil {
		return "tailscale: " + msg
	}
	if err != nil {
		return "tailscale: " + err.Error()
	}
	return fmt.Sprintf("tailscale answered something that is not its status: %v", parse)
}

// Target is the URL `tailscale serve` should forward to for a gateway bound at listen.
//
// A wildcard or loopback bind is reached on 127.0.0.1, which keeps the proxy's traffic on this
// machine; a gateway bound to one concrete address is only reachable there.
func Target(listen string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return ""
	}
	if reachedOnLoopback(host) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// reachedOnLoopback reports whether a gateway bound to host answers on 127.0.0.1: no host,
// localhost, a wildcard or a loopback address.
func reachedOnLoopback(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsUnspecified() || ip.IsLoopback())
}

// sameTarget reports whether a serve handler's proxy forwards to target. Tailscale writes the
// proxy back as it understood it ("http://127.0.0.1:7477", or "http://localhost:7477" when that is
// what was typed), so loopback spellings compare equal.
func sameTarget(proxy, target string) bool {
	a, b := hostPort(proxy), hostPort(target)
	return a != "" && a == b
}

// hostPort reduces a proxy target to host:port with every loopback name written as 127.0.0.1.
func hostPort(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Port() == "" {
		return ""
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, u.Port())
}
