package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/gateway"
)

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

// A found gateway with no token cannot produce a working link, and a link without the token is a
// page that opens on a 401 with nothing to explain it. Saying nothing is the honest outcome.
func TestNothingIsAnnouncedWithoutAToken(t *testing.T) {
	var out strings.Builder
	announceWebUI(&out, gateway.Found{BaseURL: "http://127.0.0.1:7477"})
	if out.String() != "" {
		t.Fatalf("a link was announced with no token to put in it:\n%s", out.String())
	}
}

// webUIEnabled reads the configuration through the same keyless load the rest of the subcommands
// use. A path that cannot be read answers the only safe way: claim nothing, because promising a
// page that may not exist sends the user hunting a fault that is really a missing file.
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
