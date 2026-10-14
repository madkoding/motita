package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/tailscale"
)

// fakeTS is a scripted Tailscale for the command line's questions.
type fakeTS struct {
	status   tailscale.Status
	serveErr error
	served   []string
}

func (f *fakeTS) Detect(context.Context, string) tailscale.Status { return f.status }
func (f *fakeTS) Serve(_ context.Context, target string) error {
	f.served = append(f.served, target)
	return f.serveErr
}
func (f *fakeTS) Unserve(context.Context) error { return nil }

func netConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func netOptions(in string) (Options, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return Options{Out: out, Err: errOut, Stdin: strings.NewReader(in), Interactive: func() bool { return true }},
		out, errOut
}

func savedExposure(t *testing.T, path string) string {
	t.Helper()
	cfg, err := config.LoadWithoutKey(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Gateway.Exposure()
}

func TestTheFirstStartAsksAndSavesTheAnswer(t *testing.T) {
	path := netConfig(t, "gateway:\n  enabled: true\n")
	op, out, _ := netOptions("2\n")
	op.askExposureOnFirstStart(context.Background(), flags{configPath: path})
	if savedExposure(t, path) != config.ExposureLAN || !config.ListenChosen(path) {
		t.Fatalf("not saved; output:\n%s", out)
	}
	if !strings.Contains(out.String(), "plain HTTP") {
		t.Errorf("opening the gateway came without the warning:\n%s", out)
	}

	// Answered once: the next start asks nothing.
	op, out, _ = netOptions("3\n")
	op.askExposureOnFirstStart(context.Background(), flags{configPath: path})
	if out.Len() != 0 || savedExposure(t, path) != config.ExposureLAN {
		t.Fatalf("asked again: %s", out)
	}
}

func TestTheFirstStartAsksNothingWhenSomethingElseDecided(t *testing.T) {
	cases := map[string]func(*testing.T, *Options, *flags){
		"token only":    func(_ *testing.T, _ *Options, f *flags) { f.tokenOnly = true },
		"json":          func(_ *testing.T, _ *Options, f *flags) { f.jsonOut = true },
		"-gateway":      func(_ *testing.T, _ *Options, f *flags) { f.gateway = "0.0.0.0:7477" },
		"no file":       func(_ *testing.T, _ *Options, f *flags) { f.configPath = "" },
		"no terminal":   func(_ *testing.T, o *Options, _ *flags) { o.Interactive = nil },
		"said no human": func(_ *testing.T, o *Options, _ *flags) { o.Interactive = func() bool { return false } },
		"environment":   func(t *testing.T, _ *Options, _ *flags) { t.Setenv("MOTITA_GATEWAY_LISTEN", "0.0.0.0:7477") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			path := netConfig(t, "gateway:\n  enabled: true\n")
			op, out, _ := netOptions("3\n")
			fl := flags{configPath: path}
			mutate(t, &op, &fl)
			op.askExposureOnFirstStart(context.Background(), fl)
			if body, _ := os.ReadFile(path); out.Len() != 0 || string(body) != "gateway:\n  enabled: true\n" {
				t.Fatalf("asked: %s\nfile: %s", out, body)
			}
		})
	}
}

func TestAFirstStartWithoutAnAnswerKeepsTheDefault(t *testing.T) {
	path := netConfig(t, "gateway:\n  enabled: true\n")
	op, _, errOut := netOptions("")
	op.askExposureOnFirstStart(context.Background(), flags{configPath: path})
	if config.ListenChosen(path) || !strings.Contains(errOut.String(), "not saved") {
		t.Fatalf("stderr: %s", errOut)
	}
}

func TestGatewaySetupAsksAgainAndSaysTheRunningOneNeedsARestart(t *testing.T) {
	path := netConfig(t, "gateway:\n  enabled: true\n  listen: \"0.0.0.0:9000\"\n  allow: [\"10.0.0.0/8\"]\n")
	op, out, _ := netOptions("1\n")
	op.DiscoverGateway = func(context.Context, string) (gateway.Found, bool, error) {
		return gateway.Found{}, true, nil
	}
	if code := op.gatewaySetup(context.Background(), flags{configPath: path}); code != Success {
		t.Fatalf("exit %d", code)
	}
	cfg, _ := config.LoadWithoutKey(path)
	if cfg.Gateway.Listen != "127.0.0.1:9000" {
		t.Fatalf("listen = %q", cfg.Gateway.Listen)
	}
	for _, want := range []string{"set by hand (0.0.0.0:9000)", "motita gateway stop && motita gateway start"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestGatewaySetupRefusals(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MOTITA_HOME", t.TempDir())
	op, _, errOut := netOptions("1\n")
	if code := op.gatewaySetup(context.Background(), flags{}); code != ConfigError ||
		!strings.Contains(errOut.String(), "motita config") {
		t.Errorf("no file: %d %s", code, errOut)
	}
	op, _, errOut = netOptions("")
	if code := op.gatewaySetup(context.Background(), flags{configPath: netConfig(t, "")}); code != ConfigError ||
		!strings.Contains(errOut.String(), "no answer") {
		t.Errorf("no answer: %d %s", code, errOut)
	}
	op, _, _ = netOptions("1\n")
	op.DiscoverGateway = func(context.Context, string) (gateway.Found, bool, error) {
		return gateway.Found{}, false, nil
	}
	if code := op.gatewaySetup(context.Background(), flags{configPath: netConfig(t, "")}); code != Success {
		t.Errorf("no gateway running: %d", code)
	}
}

func TestAskExposureReportsWhatItCannotDo(t *testing.T) {
	op, _, _ := netOptions("1\n")
	in := bufio.NewReader(strings.NewReader("1\n"))
	if err := op.askExposure(context.Background(), in, netConfig(t, "gateway: [\n")); err == nil {
		t.Error("a file that does not parse was asked about")
	}
	in = bufio.NewReader(strings.NewReader("1\n"))
	if err := op.askExposure(context.Background(), in, netConfig(t, "gateway: {enabled: true}\n")); err == nil ||
		!strings.Contains(err.Error(), "inline") {
		t.Errorf("an inline block: %v", err)
	}
}

func TestReadExposureAsksUntilItGetsAnAnswer(t *testing.T) {
	cases := map[string]string{
		"\n":            config.ExposureLocal,
		"local\n":       config.ExposureLocal,
		"9\nLAN\n":      config.ExposureLAN,
		"3\n":           config.ExposureAll,
		"all":           config.ExposureAll,
		"what\n2\n":     config.ExposureLAN,
		"nope\nnope\n1": config.ExposureLocal,
	}
	for input, want := range cases {
		out := &bytes.Buffer{}
		got, err := readExposure(out, bufio.NewReader(strings.NewReader(input)))
		if err != nil || got != want {
			t.Errorf("%q = %q %v, want %q", input, got, err, want)
		}
	}
	if _, err := readExposure(&bytes.Buffer{}, bufio.NewReader(strings.NewReader("x\n"))); !errors.Is(err, errNoAnswer) {
		t.Errorf("input that ends without an answer: %v", err)
	}
}

func TestTailscaleIsOfferedForWhatItCanDo(t *testing.T) {
	cases := []struct {
		name, input string
		ts          *fakeTS
		want        string
		served      bool
		fails       bool
	}{
		{"not installed", "", &fakeTS{}, "tailscale.com/download", false, false},
		{"stopped", "", &fakeTS{status: tailscale.Status{Installed: true, State: "Stopped"}},
			"not connected (Stopped)", false, false},
		{"no state", "", &fakeTS{status: tailscale.Status{Installed: true}}, "not connected (unknown)", false, false},
		{"broken", "", &fakeTS{status: tailscale.Status{Installed: true, Error: "tailscale: boom"}},
			"(tailscale: boom)", false, false},
		{"already", "", &fakeTS{status: tailscale.Status{
			Installed: true, Running: true, Serving: true, URL: "https://b.ts.net",
		}},
			"already publishes the gateway in your tailnet: https://b.ts.net", false, false},
		{"declined", "\n", &fakeTS{status: tailscale.Status{Installed: true, Running: true, DNSName: "b.ts.net"}},
			"not published", false, false},
		{"accepted", "y\n", &fakeTS{status: tailscale.Status{
			Installed: true, Running: true, DNSName: "b.ts.net", URL: "https://b.ts.net",
		}},
			"published: https://b.ts.net", true, false},
		{"denied", "yes\n", &fakeTS{serveErr: errors.New("Access denied"),
			status: tailscale.Status{Installed: true, Running: true, DNSName: "b.ts.net"}}, "", true, true},
	}
	for _, tc := range cases {
		op, out, _ := netOptions("")
		op.Tailscale = tc.ts
		err := op.offerTailscale(context.Background(), bufio.NewReader(strings.NewReader(tc.input)), "0.0.0.0:7477")
		if (err != nil) != tc.fails || (tc.fails && !strings.Contains(err.Error(), "Access denied")) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s: output lacks %q:\n%s", tc.name, tc.want, out)
		}
		if served := len(tc.ts.served) == 1 && tc.ts.served[0] == "http://127.0.0.1:7477"; served != tc.served {
			t.Errorf("%s: served = %v", tc.name, tc.ts.served)
		}
	}
	op, out, _ := netOptions("")
	err := op.offerTailscale(context.Background(), bufio.NewReader(strings.NewReader("")), "x")
	if err != nil || out.Len() != 0 {
		t.Errorf("not looked for: %v %q", err, out)
	}
}

func TestTheStartAnnouncementShowsTheTailnet(t *testing.T) {
	op, _, _ := netOptions("")
	if st := op.tailscaleStatus(context.Background(), "http://127.0.0.1:7477"); st != (tailscale.Status{}) {
		t.Fatalf("not looked for: %+v", st)
	}
	op.Tailscale = &fakeTS{status: tailscale.Status{Running: true, Serving: true, URL: "https://b.ts.net"}}
	st := op.tailscaleStatus(context.Background(), "http://127.0.0.1:7477")
	out := &bytes.Buffer{}
	announceWebUI(out, gateway.Found{BaseURL: "http://127.0.0.1:7477", Token: "tok"}, tailnetRows(st)...)
	if !strings.Contains(out.String(), "tailscale     https://b.ts.net") {
		t.Fatalf("no tailnet row:\n%s", out)
	}
	if tailnetRows(tailscale.Status{Serving: true}) != nil {
		t.Error("a row without an address")
	}

	out.Reset()
	announceTailscale(out, st)
	announceTailscale(out, tailscale.Status{})
	if out.Len() != 0 {
		t.Fatalf("announced for nothing: %s", out)
	}
	announceTailscale(out, tailscale.Status{Running: true, DNSName: "b.ts.net"})
	if !strings.Contains(out.String(), "TAILSCALE is running (b.ts.net)") {
		t.Fatalf("no offer:\n%s", out)
	}
}

func TestTerminalSessionIsFalseWithoutATerminal(t *testing.T) {
	// Under `go test` the output is a pipe, so this is never a person.
	if TerminalSession() {
		t.Skip("running attached to a terminal")
	}
}

func TestTheSetupActionIsDispatched(t *testing.T) {
	path := netConfig(t, "")
	op, _, _ := netOptions("3\n")
	op.DiscoverGateway = func(context.Context, string) (gateway.Found, bool, error) {
		return gateway.Found{}, false, nil
	}
	if code := op.runGatewayCommand(context.Background(), " Setup ", flags{configPath: path}); code != Success ||
		savedExposure(t, path) != config.ExposureAll {
		t.Fatalf("exit %d", code)
	}
}
