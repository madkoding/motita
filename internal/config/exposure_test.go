package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExposureNamesTheThreeChoicesAndEverythingElseIsCustom(t *testing.T) {
	cases := []struct {
		listen string
		allow  []string
		want   string
	}{
		{"", nil, ExposureLocal},
		{"127.0.0.1:7477", []string{"any"}, ExposureLocal},
		{"localhost:9000", nil, ExposureLocal},
		{"0.0.0.0:7477", nil, ExposureAll},
		{"[::]:7477", []string{" "}, ExposureAll},
		{":7477", nil, ExposureAll},
		{"0.0.0.0:7477", []string{"LAN", " !any "}, ExposureLAN},
		{"0.0.0.0:7477", []string{"lan"}, ExposureCustom},
		{"192.168.1.10:7477", nil, ExposureCustom},
		{"box.example:7477", nil, ExposureCustom},
		{"not an address", nil, ExposureCustom},
	}
	for _, tc := range cases {
		g := Gateway{Listen: tc.listen, Allow: tc.allow}
		if got := g.Exposure(); got != tc.want {
			t.Errorf("Exposure(%q, %q) = %q, want %q", tc.listen, tc.allow, got, tc.want)
		}
	}
}

func TestPortKeepsTheConfiguredOneOrFallsBackToTheDefault(t *testing.T) {
	if got := (Gateway{Listen: "0.0.0.0:9000"}).Port(); got != "9000" {
		t.Errorf("Port = %q", got)
	}
	if got := (Gateway{Listen: "nonsense"}).Port(); got != "7477" {
		t.Errorf("Port of an unreadable address = %q", got)
	}
}

func TestExposureSettingsAreTheFixedPairs(t *testing.T) {
	cases := map[string]struct {
		listen string
		allow  []string
	}{
		ExposureLocal: {"127.0.0.1:7477", nil},
		ExposureLAN:   {"0.0.0.0:7477", []string{"lan", "!any"}},
		ExposureAll:   {"0.0.0.0:7477", []string{}},
	}
	for mode, want := range cases {
		listen, allow, err := ExposureSettings(mode, "7477")
		if err != nil || listen != want.listen || strings.Join(allow, ",") != strings.Join(want.allow, ",") ||
			(allow == nil) != (want.allow == nil) {
			t.Errorf("%s: %q %q %v", mode, listen, allow, err)
		}
		// Each pair reads back as its own mode: the question and the settings agree.
		if got := (Gateway{Listen: listen, Allow: allow}).Exposure(); got != mode {
			t.Errorf("%s reads back as %s", mode, got)
		}
	}
	if _, _, err := ExposureSettings(ExposureCustom, "7477"); err == nil {
		t.Error("custom was accepted as a choice")
	}
	if ValidExposure(ExposureCustom) || !ValidExposure(ExposureLAN) {
		t.Error("ValidExposure")
	}
}

func writeExposureConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetGatewayExposureEditsInPlaceAndKeepsThePort(t *testing.T) {
	body := `# my config
gateway:
  enabled: true
  # where it listens
  listen: "0.0.0.0:9000" # chosen by hand
  allow:
    - lan
    - 10.0.0.0/8
  tls:
    listen: "0.0.0.0:9443"
    cert: c.pem
    key: k.pem
ui:
  language: es
`
	path := writeExposureConfig(t, body)
	if err := SetGatewayExposure(path, ExposureLAN); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := `# my config
gateway:
  enabled: true
  # where it listens
  listen: "0.0.0.0:9000" # chosen by hand
  allow: ["lan", "!any"]
  tls:
    listen: "0.0.0.0:9443"
    cert: c.pem
    key: k.pem
ui:
  language: es
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil || cfg.Gateway.Exposure() != ExposureLAN || cfg.Gateway.TLS.Listen != "0.0.0.0:9443" {
		t.Fatalf("reloaded: %+v %v", cfg.Gateway, err)
	}
	if !ListenChosen(path) {
		t.Fatal("a written listen is not seen as chosen")
	}

	// Local keeps the rules for the day the gateway is opened again.
	if err := SetGatewayExposure(path, ExposureLocal); err != nil {
		t.Fatal(err)
	}
	cfg, _ = LoadWithoutKey(path)
	if cfg.Gateway.Listen != "127.0.0.1:9000" || strings.Join(cfg.Gateway.Allow, " ") != "lan !any" {
		t.Fatalf("local: %+v", cfg.Gateway)
	}
	if err := SetGatewayExposure(path, ExposureAll); err != nil {
		t.Fatal(err)
	}
	cfg, _ = LoadWithoutKey(path)
	if cfg.Gateway.Exposure() != ExposureAll {
		t.Fatalf("all: %+v", cfg.Gateway)
	}
}

func TestSetGatewayExposureAddsWhatIsMissing(t *testing.T) {
	path := writeExposureConfig(t, "gateway:\n\n    enabled: true\nui:\n  language: en\n")
	if err := SetGatewayExposure(path, ExposureAll); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "gateway:\n    allow: []\n    listen: \"0.0.0.0:7477\"\n") {
		t.Fatalf("got:\n%s", got)
	}

	path = writeExposureConfig(t, "ui:\n  language: en\n")
	if err := SetGatewayExposure(path, ExposureLocal); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "ui:\n  language: en\n\ngateway:\n  listen: \"127.0.0.1:7477\"\n" {
		t.Fatalf("got:\n%q", got)
	}

	path = writeExposureConfig(t, "gateway:\n")
	if err := SetGatewayExposure(path, ExposureLocal); err != nil {
		t.Fatal(err)
	}
	if !ListenChosen(path) {
		t.Fatal("listen under an empty block was not written")
	}
}

func TestSetBlockKeyTextOnAnEmptyFile(t *testing.T) {
	got, err := setBlockKeyText("", "gateway", "listen", `"x"`)
	if err != nil || got != "gateway:\n  listen: \"x\"\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestSetGatewayExposureRefusesWhatItCannotEdit(t *testing.T) {
	if err := SetGatewayExposure(writeExposureConfig(t, ""), "custom"); err == nil {
		t.Error("custom was written")
	}
	if err := SetGatewayExposure(filepath.Join(t.TempDir(), "absent.yaml"), ExposureLocal); err == nil {
		t.Error("a missing file was accepted")
	}
	if err := SetGatewayExposure(t.TempDir(), ExposureLocal); err == nil {
		t.Error("a directory was read as a file")
	}
	if err := SetGatewayExposure(writeExposureConfig(t, "gateway: [\n"), ExposureLocal); err == nil {
		t.Error("a file that does not parse was edited")
	}
	inline := writeExposureConfig(t, "gateway: {enabled: true}\n")
	err := SetGatewayExposure(inline, ExposureLocal)
	if err == nil || !strings.Contains(err.Error(), "inline") {
		t.Errorf("inline block: %v", err)
	}
	if ListenChosen(filepath.Join(t.TempDir(), "absent.yaml")) {
		t.Error("a missing file has a chosen listen")
	}
}

func TestSetGatewayExposureReportsAWriteFailure(t *testing.T) {
	path := writeExposureConfig(t, "gateway:\n  enabled: true\n")
	old := uiRename
	t.Cleanup(func() { uiRename = old })
	uiRename = func(string, string) error { return os.ErrPermission }
	if err := SetGatewayExposure(path, ExposureLocal); err == nil {
		t.Fatal("a failed write was reported as saved")
	}
}

func TestSetBlockKeyTextSkipsNestedBlocksAndStopsAtABlankLine(t *testing.T) {
	body := "gateway:\n  tls:\n    listen: a\n  allow:\n  - lan\n\n  listen: b\n"
	got, err := setBlockKeyText(body, "gateway", "allow", "[]")
	if err != nil || got != "gateway:\n  tls:\n    listen: a\n  allow: []\n\n  listen: b\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestListenAddressResolvesTheDefault(t *testing.T) {
	if got := (Gateway{}).ListenAddress(); got != "127.0.0.1:7477" {
		t.Fatalf("ListenAddress = %q", got)
	}
}
