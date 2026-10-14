package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/tailscale"
	"github.com/madkoding/motita/internal/tui"
)

// WHERE THE GATEWAY ANSWERS, asked as one question.
//
// The first `motita gateway start` with a configuration file that names no gateway.listen asks
// it, and `motita gateway setup` asks it again whenever the person wants. The answer is written
// into the file, so it is asked once and the settings page shows the same choice afterwards.
// The default answer is the safe one, this machine only, and nothing is ever opened without the
// person choosing it. When Tailscale is running, the same moment offers the private alternative:
// publishing the gateway inside the tailnet with https while the socket stays on loopback.

// TerminalSession reports whether a person is at this process's terminal: input AND output are
// a terminal. Output too, because a service manager or a test can hand a process a character
// device such as /dev/null for input while nobody reads what it prints.
func TerminalSession() bool {
	return tui.IsTerminal(os.Stdin) && tui.IsTerminal(os.Stdout)
}

// interactive reports whether there is somebody to ask.
func (op Options) interactive() bool {
	return op.Interactive != nil && op.Interactive()
}

// tailscaleNotOffered says in one line why there is nothing to publish (Tailscale missing, not
// connected, or already serving the gateway) and reports whether it said so.
func (op Options) tailscaleNotOffered(st tailscale.Status) bool {
	switch {
	case !st.Installed:
		fmt.Fprintln(op.Out, "\ntip: Tailscale gives your own devices private https access to the gateway without")
		fmt.Fprintln(op.Out, "  opening it to a network: https://tailscale.com/download")
		return true
	case !st.Running:
		fmt.Fprintf(op.Out, "\nTailscale is installed but not connected (%s): run `tailscale up` to publish\n", stateOf(st))
		fmt.Fprintln(op.Out, "  the gateway privately in your tailnet with https.")
		return true
	case st.Serving:
		fmt.Fprintf(op.Out, "\nTailscale already publishes the gateway in your tailnet: %s\n", st.URL)
		return true
	default:
		return false
	}
}

// exposureDecided reports whether something already chose where the gateway answers: a flag, the
// environment or the configuration file. A script asking for -json or -token-only counts too.
func exposureDecided(fl flags, path string) bool {
	if fl.tokenOnly || fl.jsonOut || strings.TrimSpace(fl.gateway) != "" {
		return true
	}
	return path == "" || strings.TrimSpace(os.Getenv("MOTITA_GATEWAY_LISTEN")) != "" || config.ListenChosen(path)
}

// askExposureOnFirstStart asks where the gateway should answer when nothing has decided it yet:
// no -gateway address, no MOTITA_GATEWAY_LISTEN, no gateway.listen in the file, no script asking
// for -json or -token-only, and a person at the terminal.
func (op Options) askExposureOnFirstStart(ctx context.Context, fl flags) {
	path := resolvedConfigPath(fl)
	if exposureDecided(fl, path) || !op.interactive() {
		return
	}
	if err := op.askExposure(ctx, bufio.NewReader(op.Stdin), path); err != nil {
		// The question is a convenience in front of the start; failing it must not stop the
		// gateway, which then binds the safe default.
		fmt.Fprintf(op.Err, "the network choice was not saved: %v\n", err)
	}
}

// gatewaySetup is `motita gateway setup`: the same question, on demand.
func (op Options) gatewaySetup(ctx context.Context, fl flags) int {
	path := resolvedConfigPath(fl)
	if path == "" {
		fmt.Fprintln(op.Err, "there is no configuration file to save the choice in: run `motita config` first")
		return ConfigError
	}
	if err := op.askExposure(ctx, bufio.NewReader(op.Stdin), path); err != nil {
		fmt.Fprintf(op.Err, "the network choice was not saved: %v\n", err)
		return ConfigError
	}
	if _, running, _ := op.discover(ctx); running {
		fmt.Fprintln(op.Out, "\nthe running gateway keeps its address until it restarts:")
		fmt.Fprintln(op.Out, "  motita gateway stop && motita gateway start")
	}
	return Success
}

// errNoAnswer is the input ending before an answer was given.
var errNoAnswer = errors.New("no answer was given")

// askExposure asks the question, saves the answer in the file at path and offers Tailscale.
func (op Options) askExposure(ctx context.Context, in *bufio.Reader, path string) error {
	cfg, err := config.LoadWithoutKey(path)
	if err != nil {
		return err
	}
	current := cfg.Gateway.Exposure()
	fmt.Fprintln(op.Out, "\nNETWORK: where should the gateway answer?")
	fmt.Fprintln(op.Out, "  1) this machine only (127.0.0.1)                     safest, the default")
	fmt.Fprintln(op.Out, "  2) the local network (0.0.0.0, private addresses only)")
	fmt.Fprintln(op.Out, "  3) everywhere (0.0.0.0, every address may connect)")
	if current == config.ExposureCustom {
		fmt.Fprintf(op.Out, "  now: set by hand (%s); an answer replaces it\n", cfg.Gateway.ListenAddress())
	}
	mode, err := readExposure(op.Out, in)
	if err != nil {
		return err
	}
	if err := config.SetGatewayExposure(path, mode); err != nil {
		return err
	}
	listen, _, _ := config.ExposureSettings(mode, cfg.Gateway.Port())
	fmt.Fprintf(op.Out, "saved: the gateway listens on %s (gateway.listen in %s)\n", listen, path)
	if mode != config.ExposureLocal {
		fmt.Fprintln(op.Out, "  the gateway speaks plain HTTP: the token travels unencrypted on that network,")
		fmt.Fprintln(op.Out, "  and it is all that stands between it and a program that runs commands here.")
	}
	fmt.Fprintln(op.Out, "  change it with `motita gateway setup` or in the settings of the web interface.")
	return op.offerTailscale(ctx, in, listen)
}

// readExposure reads the answer, asking again until it is one of the choices.
func readExposure(out io.Writer, in *bufio.Reader) (string, error) {
	for {
		fmt.Fprint(out, "choice [1]: ")
		line, err := in.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "" && err != nil {
			fmt.Fprintln(out)
			return "", errNoAnswer
		}
		switch answer {
		case "", "1", config.ExposureLocal:
			return config.ExposureLocal, nil
		case "2", config.ExposureLAN:
			return config.ExposureLAN, nil
		case "3", config.ExposureAll:
			return config.ExposureAll, nil
		default:
			fmt.Fprintf(out, "%q is not one of 1, 2 or 3\n", answer)
		}
	}
}

// offerTailscale publishes the gateway in the tailnet when Tailscale is running and the person
// says yes, and otherwise says in one line what Tailscale would give them.
func (op Options) offerTailscale(ctx context.Context, in *bufio.Reader, listen string) error {
	if op.Tailscale == nil {
		return nil
	}
	target := tailscale.Target(listen)
	st := op.Tailscale.Detect(ctx, target)
	if op.tailscaleNotOffered(st) {
		return nil
	}
	fmt.Fprintf(op.Out, "\nTailscale is running here (%s). Publish the gateway in your tailnet with https?\n", st.DNSName)
	fmt.Fprintln(op.Out, "  only your tailnet's devices reach it (not the internet), through `tailscale serve`.")
	fmt.Fprint(op.Out, "publish [y/N]: ")
	line, _ := in.ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		fmt.Fprintln(op.Out, "not published.")
		return nil
	}
	if err := op.Tailscale.Serve(ctx, target); err != nil {
		return err
	}
	fmt.Fprintf(op.Out, "published: %s\n", st.URL)
	return nil
}

// stateOf is the daemon's state for a sentence, "unknown" when it gave none.
func stateOf(st tailscale.Status) string {
	if st.Error != "" {
		return st.Error
	}
	if st.State == "" {
		return "unknown"
	}
	return st.State
}

// tailscaleStatus is Tailscale as the running gateway at baseURL sees it, zero when not looked for.
func (op Options) tailscaleStatus(ctx context.Context, baseURL string) tailscale.Status {
	if op.Tailscale == nil {
		return tailscale.Status{}
	}
	return op.Tailscale.Detect(ctx, tailscale.Target(strings.TrimPrefix(baseURL, "http://")))
}

// tailnetRows is the row the address table gains while the gateway is published in the tailnet.
func tailnetRows(st tailscale.Status) []endpoint {
	if !st.Serving || st.URL == "" {
		return nil
	}
	return []endpoint{{Name: "tailscale", Addr: st.URL}}
}

// announceTailscale offers the tailnet when Tailscale is running and does not publish the gateway.
func announceTailscale(out io.Writer, st tailscale.Status) {
	if !st.Running || st.Serving {
		return
	}
	fmt.Fprintf(out, "\nTAILSCALE is running (%s) but does not publish the gateway: `motita gateway setup`\n", st.DNSName)
	fmt.Fprintln(out, "  or the settings page can publish it in your tailnet with https.")
}
