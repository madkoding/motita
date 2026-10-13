package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// THE GATEWAY'S EXPOSURE, as one choice instead of two settings.
//
// How far the gateway reaches is gateway.listen (where the socket is) plus gateway.allow (who may
// use it). Those two are the precise model, and they stay the source of truth; but the question a
// person is actually asked, by `motita gateway setup` and by the settings page, is the short one:
// this machine, the local network, or everywhere. Each answer is a fixed pair of the two settings,
// and any other pair is "custom" - something written by hand, which is shown as it is and never
// rewritten behind the operator's back.

// The exposure modes.
const (
	// ExposureLocal binds loopback: nothing outside this machine can reach the gateway. The default.
	ExposureLocal = "local"
	// ExposureLAN binds every interface and lets in only the private and link-local ranges.
	ExposureLAN = "lan"
	// ExposureAll binds every interface and lets every origin in.
	ExposureAll = "all"
	// ExposureCustom is any listen/allow pair the three above do not describe.
	ExposureCustom = "custom"
)

// lanRules is the allow list of ExposureLAN: the local network in, everything else out.
var lanRules = []string{"lan", "!any"}

// ValidExposure reports whether mode is one a person can choose (custom is only ever observed).
func ValidExposure(mode string) bool {
	switch mode {
	case ExposureLocal, ExposureLAN, ExposureAll:
		return true
	}
	return false
}

// Exposure names the mode this gateway configuration amounts to.
func (g Gateway) Exposure() string {
	host, _, err := net.SplitHostPort(defaultListenFor(g))
	if err != nil {
		return ExposureCustom
	}
	if gatewayAddressIsLoopback(host) {
		return ExposureLocal
	}
	ip := net.ParseIP(host)
	if host != "" && (ip == nil || !ip.IsUnspecified()) {
		// One concrete address: a choice of interface nobody made through this question.
		return ExposureCustom
	}
	rules := make([]string, 0, len(g.Allow))
	for _, r := range g.Allow {
		if r = strings.ToLower(strings.TrimSpace(r)); r != "" {
			rules = append(rules, r)
		}
	}
	switch {
	case len(rules) == 0:
		return ExposureAll
	case strings.Join(rules, " ") == strings.Join(lanRules, " "):
		return ExposureLAN
	default:
		return ExposureCustom
	}
}

// Port is the port the gateway listens on, the default one when the address has none to read.
func (g Gateway) Port() string {
	if _, port, err := net.SplitHostPort(defaultListenFor(g)); err == nil && port != "" {
		return port
	}
	_, port, _ := net.SplitHostPort(defaultGatewayListen)
	return port
}

// ListenAddress is the address this gateway configuration binds, the default when none is written.
func (g Gateway) ListenAddress() string { return defaultListenFor(g) }

// ExposureSettings is the listen address and the allow list of a mode on a port. A nil allow
// list means "leave the rules as they are": loopback is unreachable from outside, so the rules
// have nothing to say and whatever the operator wrote is kept for the day they open it again.
func ExposureSettings(mode, port string) (listen string, allow []string, err error) {
	switch mode {
	case ExposureLocal:
		return net.JoinHostPort("127.0.0.1", port), nil, nil
	case ExposureLAN:
		return net.JoinHostPort("0.0.0.0", port), append([]string{}, lanRules...), nil
	case ExposureAll:
		return net.JoinHostPort("0.0.0.0", port), []string{}, nil
	}
	return "", nil, fmt.Errorf("the exposure %q is not one of local, lan, all", mode)
}

// ListenChosen reports whether the configuration file at path names a gateway.listen: whether the
// exposure question has an answer yet. A file that cannot be read has none.
func ListenChosen(path string) bool {
	cfg, err := LoadWithoutKey(path)
	return err == nil && strings.TrimSpace(cfg.Gateway.Listen) != ""
}

// SetGatewayExposure writes mode into the configuration file at path as gateway.listen and
// gateway.allow, IN PLACE and keeping the port already configured.
//
// Like SetUILanguage it edits lines rather than re-marshalling: the file is commented by hand and
// a YAML round trip would lose every comment to change two values. The running gateway is not
// touched; the new address is bound when it next starts.
func SetGatewayExposure(path, mode string) error {
	if !ValidExposure(mode) {
		return fmt.Errorf("the exposure %q is not one of local, lan, all", mode)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	// Loaded for the port only, and loaded whole: a file that does not parse is refused here
	// rather than edited into a second problem.
	cfg, err := LoadWithoutKey(path)
	if err != nil {
		return err
	}
	listen, allow, _ := ExposureSettings(mode, cfg.Gateway.Port())
	text, err := setBlockKeyText(string(body), "gateway", "listen", strconv.Quote(listen))
	if err == nil && allow != nil {
		text, err = setBlockKeyText(text, "gateway", "allow", flowList(allow))
	}
	if err != nil {
		return err
	}
	return writeConfigAtomic(path, []byte(text), info.Mode().Perm())
}

// flowList writes a list as a one-line YAML sequence of quoted strings ("!any" needs the quotes).
func flowList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

var (
	indentOf  = regexp.MustCompile(`^(\s*)`)
	keyOfLine = regexp.MustCompile(`^(\s*)([A-Za-z0-9_]+):(\s*[^#]*?)(\s*#.*)?$`)
)

// setBlockKeyText sets key to value (YAML text) directly under the top-level block, on the text.
//
// An existing `key:` at the block's own indent is replaced, together with the lines that belong to
// its old value (a block sequence under it); a missing key is added as the block's first entry, and
// a missing block is added at the end. Keys of nested blocks (gateway.tls.listen) are never
// touched, because only lines at the block's own indent are read as its keys.
func setBlockKeyText(body, block, key, value string) (string, error) {
	lines := strings.Split(body, "\n")
	header := regexp.MustCompile(`^` + regexp.QuoteMeta(block) + `:\s*(#.*)?$`)
	inline := regexp.MustCompile(`^` + regexp.QuoteMeta(block) + `:`)
	start := -1
	for i, l := range lines {
		if header.MatchString(l) {
			start = i
			break
		}
		if inline.MatchString(l) {
			return "", fmt.Errorf("the configuration writes %s as an inline value (%q): change %s.%s by hand",
				block, strings.TrimSpace(l), block, key)
		}
	}
	if start < 0 {
		trimmed := strings.TrimRight(body, "\n")
		added := block + ":\n  " + key + ": " + value + "\n"
		if trimmed == "" {
			return added, nil
		}
		return trimmed + "\n\n" + added, nil
	}
	indent := ""
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if isBlankOrComment(l) {
			continue
		}
		ind := indentOf.FindString(l)
		if ind == "" {
			break // the next top-level key
		}
		if indent == "" {
			indent = ind
		}
		if ind != indent {
			continue // inside a nested block
		}
		m := keyOfLine.FindStringSubmatch(l)
		if m == nil || m[2] != key {
			continue
		}
		end := i + 1
		for end < len(lines) && belongsToValue(lines[end], indent) {
			end++
		}
		out := append([]string{}, lines[:i]...)
		out = append(out, indent+key+": "+value+m[4])
		out = append(out, lines[end:]...)
		return strings.Join(out, "\n"), nil
	}
	if indent == "" {
		indent = "  "
	}
	out := append([]string{}, lines[:start+1]...)
	out = append(out, indent+key+": "+value)
	out = append(out, lines[start+1:]...)
	return strings.Join(out, "\n"), nil
}

// belongsToValue reports whether a line after `key:` is part of that key's value: more indented
// than the key, or a sequence item at the key's own indent (YAML allows both).
func belongsToValue(line, indent string) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	ind := indentOf.FindString(line)
	return len(ind) > len(indent) || (ind == indent && strings.HasPrefix(line[len(ind):], "- "))
}

// isBlankOrComment reports whether a line carries no key.
func isBlankOrComment(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "#")
}
