package tailscale

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fake is a tailscale command scripted per subcommand.
type fake struct {
	missing bool
	answers map[string]answer
	calls   []string
}

type answer struct {
	out string
	err error
}

func (f *fake) client() *Client {
	return &Client{
		LookPath: func(string) (string, error) {
			if f.missing {
				return "", exec.ErrNotFound
			}
			return "/usr/bin/tailscale", nil
		},
		Run: func(_ context.Context, bin string, args ...string) ([]byte, error) {
			call := strings.Join(args, " ")
			f.calls = append(f.calls, call)
			a := f.answers[call]
			return []byte(a.out), a.err
		},
	}
}

const running = `{"BackendState":"Running","Self":{"DNSName":"box.tail1234.ts.net."}}`

func TestNotInstalledIsAQuietAnswer(t *testing.T) {
	st := (&fake{missing: true}).client().Detect(context.Background(), "http://127.0.0.1:7477")
	if st != (Status{}) {
		t.Fatalf("status = %+v, want the zero value", st)
	}
}

func TestRunningAndServingIsDetected(t *testing.T) {
	f := &fake{answers: map[string]answer{
		"status --json": {out: running},
		"serve status --json": {
			out: `{"Web":{"box.tail1234.ts.net:443":{"Handlers":{"/":{"Proxy":"http://localhost:7477"}}}}}`,
		},
	}}
	st := f.client().Detect(context.Background(), "http://127.0.0.1:7477")
	want := Status{Installed: true, Running: true, State: "Running", DNSName: "box.tail1234.ts.net",
		Serving: true, URL: "https://box.tail1234.ts.net"}
	if st != want {
		t.Fatalf("status = %+v, want %+v", st, want)
	}
}

func TestServingAnotherTargetIsNotServingTheGateway(t *testing.T) {
	f := &fake{answers: map[string]answer{
		"status --json":       {out: running},
		"serve status --json": {out: `{"Web":{"x:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`},
	}}
	if f.client().Detect(context.Background(), "http://127.0.0.1:7477").Serving {
		t.Fatal("a proxy to another port was taken for the gateway")
	}
}

func TestAServeStatusThatFailsOrIsNotJSONMeansNotServing(t *testing.T) {
	for name, a := range map[string]answer{
		"fails":    {err: errors.New("exit 1")},
		"not json": {out: "nothing"},
	} {
		f := &fake{answers: map[string]answer{"status --json": {out: running}, "serve status --json": a}}
		if f.client().Detect(context.Background(), "http://127.0.0.1:7477").Serving {
			t.Errorf("%s: serving reported", name)
		}
	}
}

func TestAStoppedDaemonIsInstalledButNotRunning(t *testing.T) {
	f := &fake{answers: map[string]answer{
		"status --json": {out: `{"BackendState":"Stopped"}`, err: errors.New("exit status 1")},
	}}
	st := f.client().Detect(context.Background(), "http://127.0.0.1:7477")
	if !st.Installed || st.Running || st.State != "Stopped" {
		t.Fatalf("status = %+v", st)
	}
	if st.URL != "" || st.Error != "" {
		t.Fatalf("a stopped daemon has no URL nor error: %+v", st)
	}
	if len(f.calls) != 1 {
		t.Fatalf("serve status asked of a stopped daemon: %v", f.calls)
	}
}

func TestRunningWithoutANameHasNoURL(t *testing.T) {
	f := &fake{answers: map[string]answer{"status --json": {out: `{"BackendState":"Running"}`}}}
	st := f.client().Detect(context.Background(), "http://127.0.0.1:7477")
	if !st.Running || st.URL != "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestAnUnreadableStatusIsReportedInWords(t *testing.T) {
	cases := map[string]struct {
		a    answer
		want string
	}{
		"the command's own words": {answer{out: "failed to connect to local tailscaled", err: errors.New("exit 1")},
			"tailscale: failed to connect to local tailscaled"},
		"no words":     {answer{err: errors.New("exit 1")}, "tailscale: exit 1"},
		"not its json": {answer{out: "hello"}, "tailscale answered something that is not its status"},
	}
	for name, tc := range cases {
		f := &fake{answers: map[string]answer{"status --json": tc.a}}
		st := f.client().Detect(context.Background(), "")
		if !st.Installed || st.Running || !strings.HasPrefix(st.Error, tc.want) {
			t.Errorf("%s: status = %+v, want error starting %q", name, st, tc.want)
		}
	}
}

func TestServeAndUnserveRunTheDocumentedCommands(t *testing.T) {
	f := &fake{}
	c := f.client()
	if err := c.Serve(context.Background(), "http://127.0.0.1:7477"); err != nil {
		t.Fatal(err)
	}
	if err := c.Unserve(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"serve --bg --https=443 http://127.0.0.1:7477", "serve --https=443 off"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", f.calls, want)
	}
}

func TestServeReportsWhatTheCommandSaid(t *testing.T) {
	f := &fake{answers: map[string]answer{
		"serve --bg --https=443 x": {out: "Access denied: serve config denied", err: errors.New("exit 1")},
	}}
	err := f.client().Serve(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "Access denied") || !strings.HasSuffix(err.Error(), OperatorHint) {
		t.Fatalf("err = %v", err)
	}
	if err := (&fake{missing: true}).client().Unserve(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "not installed") {
		t.Fatalf("missing command: err = %v", err)
	}
}

func TestTargetIsTheLoopbackOfAWildcardOrLoopbackBind(t *testing.T) {
	for listen, want := range map[string]string{
		"127.0.0.1:7477":    "http://127.0.0.1:7477",
		"0.0.0.0:7477":      "http://127.0.0.1:7477",
		"[::]:7477":         "http://127.0.0.1:7477",
		":7477":             "http://127.0.0.1:7477",
		"localhost:8080":    "http://127.0.0.1:8080",
		"[::1]:7477":        "http://127.0.0.1:7477",
		"192.168.1.10:7477": "http://192.168.1.10:7477",
		"not an address":    "",
	} {
		if got := Target(listen); got != want {
			t.Errorf("Target(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestSameTargetComparesLoopbackSpellings(t *testing.T) {
	cases := []struct {
		proxy, target string
		want          bool
	}{
		{"http://127.0.0.1:7477", "http://127.0.0.1:7477", true},
		{"localhost:7477", "http://127.0.0.1:7477", true},
		{"http://[::1]:7477", "http://127.0.0.1:7477", true},
		{"http://127.0.0.1:7478", "http://127.0.0.1:7477", false},
		{"http://127.0.0.1", "http://127.0.0.1", false},
		{"http://%zz", "http://127.0.0.1:7477", false},
	}
	for _, tc := range cases {
		if got := sameTarget(tc.proxy, tc.target); got != tc.want {
			t.Errorf("sameTarget(%q, %q) = %v", tc.proxy, tc.target, got)
		}
	}
}

func TestNewRunsTheRealCommand(t *testing.T) {
	c := New()
	if c.LookPath == nil || c.Run == nil {
		t.Fatal("New left a seam empty")
	}
	out, err := runCommand(context.Background(), "go", "env", "GOOS")
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("runCommand: %q %v", out, err)
	}
}

func TestPeersListsTheOnlineMachinesIPv6First(t *testing.T) {
	f := &fake{answers: map[string]answer{"status --json": {out: `{"BackendState":"Running","Peer":{
		"k1":{"HostName":"zeta","TailscaleIPs":["100.64.0.2","fd7a:115c:a1e0::2"],"Online":true},
		"k2":{"HostName":"alpha","TailscaleIPs":["fd7a:115c:a1e0::3","100.64.0.3","bogus"],"Online":true},
		"k3":{"HostName":"off","TailscaleIPs":["100.64.0.4"],"Online":false},
		"k4":{"HostName":"noip","TailscaleIPs":["bogus"],"Online":true}}}`}}}
	peers := f.client().Peers(context.Background())
	if len(peers) != 2 || peers[0].HostName != "alpha" || peers[1].HostName != "zeta" {
		t.Fatalf("peers = %+v", peers)
	}
	for _, p := range peers {
		if !p.IPs[0].Is6() || len(p.IPs) != 2 {
			t.Fatalf("%s: IPs = %v, want IPv6 first", p.HostName, p.IPs)
		}
	}
}

func TestPeersIsEmptyWithoutTailscaleOrAReadableStatus(t *testing.T) {
	if p := (&fake{missing: true}).client().Peers(context.Background()); p != nil {
		t.Fatalf("missing: %v", p)
	}
	f := &fake{answers: map[string]answer{"status --json": {out: "nope", err: errors.New("exit 1")}}}
	if p := f.client().Peers(context.Background()); p != nil {
		t.Fatalf("unreadable: %v", p)
	}
}
