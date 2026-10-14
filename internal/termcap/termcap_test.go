package termcap

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// envOf is a lookup over a fixed map, so no test depends on the machine's own variables.
func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// charDevice is a character device standing in for a terminal: /dev/null is one on every Unix,
// and it is what IsTerminal can actually tell apart from a regular file.
func charDevice(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no character device: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Skip("the null device is not a character device here")
	}
	return f
}

// onPlatform plays another platform for one test.
func onPlatform(t *testing.T, name string, vt bool) *int {
	t.Helper()
	oldGoos, oldVT := goos, enableVTFn
	calls := new(int)
	goos = name
	enableVTFn = func(*os.File) bool { *calls++; return vt }
	t.Cleanup(func() { goos, enableVTFn = oldGoos, oldVT })
	return calls
}

func TestLevelNames(t *testing.T) {
	for l, want := range map[Level]string{None: "none", ANSI16: "16", ANSI256: "256", TrueColor: "truecolor", Level(9): "none"} {
		if got := l.String(); got != want {
			t.Errorf("Level(%d) = %q, want %q", int(l), got, want)
		}
	}
	if (Profile{}).Color() || !(Profile{Level: ANSI16}).Color() {
		t.Error("Color must be true exactly when there is a level")
	}
}

// TestUnixTerminalDepths: the depth a Unix terminal is assumed to have, from what it says about
// itself.
func TestUnixTerminalDepths(t *testing.T) {
	onPlatform(t, "linux", true)
	tty := charDevice(t)
	cases := []struct {
		env  map[string]string
		want Level
	}{
		{map[string]string{"TERM": "xterm"}, ANSI16},
		{map[string]string{"TERM": "xterm-256color"}, ANSI256},
		{map[string]string{"TERM": "xterm-direct"}, TrueColor},
		{map[string]string{"TERM": "foot-truecolor"}, TrueColor},
		{map[string]string{"TERM": "konsole-24bit"}, TrueColor},
		{map[string]string{"TERM": "xterm", "COLORTERM": "truecolor"}, TrueColor},
		{map[string]string{"TERM": "xterm", "COLORTERM": "24BIT"}, TrueColor},
		{map[string]string{"TERM": "xterm", "WT_SESSION": "abc"}, TrueColor},
		{map[string]string{"TERM": "xterm", "TERM_PROGRAM": "vscode"}, TrueColor},
		{map[string]string{"TERM": "xterm", "TERM_PROGRAM": "Apple_Terminal"}, ANSI16},
		{map[string]string{}, None},
		{map[string]string{"TERM": " DUMB "}, None},
	}
	for _, c := range cases {
		p := Detect(envOf(c.env), tty)
		if p.Level != c.want {
			t.Errorf("%v: level %v, want %v", c.env, p.Level, c.want)
		}
		if p.ASCII {
			t.Errorf("%v: a Unix terminal must keep its glyphs", c.env)
		}
	}
	if p := Detect(envOf(map[string]string{"TERM": "dumb"}), tty); p.Escapes {
		t.Error("TERM=dumb cannot interpret escapes")
	}
	if p := Detect(envOf(map[string]string{"TERM": "xterm"}), tty); !p.Escapes {
		t.Error("a described terminal interprets escapes")
	}
}

// TestWindowsConsoles: the case this package exists for. Windows sets no TERM, so a console that
// took virtual terminal mode gets colour, and one that refused it gets neither colour nor escapes,
// and draws in ASCII.
func TestWindowsConsoles(t *testing.T) {
	tty := charDevice(t)

	calls := onPlatform(t, "windows", true)
	p := Detect(envOf(nil), tty)
	if p != (Profile{Level: TrueColor, Escapes: true}) {
		t.Errorf("a modern console: %+v", p)
	}
	if *calls != 1 {
		t.Errorf("virtual terminal mode must be asked for once, was %d", *calls)
	}

	onPlatform(t, "windows", false)
	p = Detect(envOf(nil), tty)
	if p != (Profile{Level: None, Escapes: false, ASCII: true}) {
		t.Errorf("a legacy console: %+v", p)
	}

	// A pipe is not a console: nothing is switched and nothing is drawn in ASCII.
	calls = onPlatform(t, "windows", true)
	if p := Detect(envOf(nil), &bytes.Buffer{}); p != (Profile{}) || *calls != 0 {
		t.Errorf("a pipe on Windows: %+v, %d calls", p, *calls)
	}
}

// TestOverrides: what a user sets beats what is detected, in the documented order.
func TestOverrides(t *testing.T) {
	onPlatform(t, "linux", true)
	tty := charDevice(t)
	var pipe bytes.Buffer
	cases := []struct {
		name string
		out  io.Writer
		env  map[string]string
		want Level
	}{
		{"MOTITA_COLOR=never", tty, map[string]string{"TERM": "xterm", "MOTITA_COLOR": "never", "FORCE_COLOR": "3"}, None},
		{"MOTITA_COLOR=always into a pipe", &pipe, map[string]string{"MOTITA_COLOR": "Always", "NO_COLOR": "1"}, ANSI16},
		{"MOTITA_COLOR=always keeps a deeper level", tty, map[string]string{"TERM": "xterm-256color", "MOTITA_COLOR": "1"}, ANSI256},
		{"MOTITA_COLOR=16", tty, map[string]string{"TERM": "xterm-256color", "MOTITA_COLOR": "16"}, ANSI16},
		{"MOTITA_COLOR=256", &pipe, map[string]string{"MOTITA_COLOR": "256"}, ANSI256},
		{"MOTITA_COLOR=truecolor", &pipe, map[string]string{"MOTITA_COLOR": "24bit"}, TrueColor},
		{"MOTITA_COLOR=auto defers", tty, map[string]string{"TERM": "xterm", "MOTITA_COLOR": "auto", "NO_COLOR": "1"}, None},
		{"NO_COLOR", tty, map[string]string{"TERM": "xterm", "NO_COLOR": "0"}, None},
		{"empty NO_COLOR is not set", tty, map[string]string{"TERM": "xterm", "NO_COLOR": ""}, ANSI16},
		{"NO_COLOR beats FORCE_COLOR", tty, map[string]string{"TERM": "xterm", "NO_COLOR": "1", "FORCE_COLOR": "1"}, None},
		{"FORCE_COLOR=0", tty, map[string]string{"TERM": "xterm", "FORCE_COLOR": "0"}, None},
		{"FORCE_COLOR empty into a pipe", &pipe, map[string]string{"FORCE_COLOR": ""}, ANSI16},
		{"FORCE_COLOR=1 keeps a deeper level", tty, map[string]string{"TERM": "xterm-256color", "FORCE_COLOR": "1"}, ANSI256},
		{"FORCE_COLOR=2", &pipe, map[string]string{"FORCE_COLOR": "2"}, ANSI256},
		{"FORCE_COLOR=3", &pipe, map[string]string{"FORCE_COLOR": "3"}, TrueColor},
	}
	for _, c := range cases {
		if got := Detect(envOf(c.env), c.out).Level; got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestASCIISwitch: MOTITA_ASCII forces the stand-ins either way; an unknown value leaves the
// detection alone.
func TestASCIISwitch(t *testing.T) {
	tty := charDevice(t)
	onPlatform(t, "linux", true)
	if !Detect(envOf(map[string]string{"TERM": "xterm", "MOTITA_ASCII": "yes"}), tty).ASCII {
		t.Error("MOTITA_ASCII=yes must force ASCII")
	}
	onPlatform(t, "windows", false)
	if Detect(envOf(map[string]string{"MOTITA_ASCII": "off"}), tty).ASCII {
		t.Error("MOTITA_ASCII=off must forbid ASCII")
	}
	if !Detect(envOf(map[string]string{"MOTITA_ASCII": "maybe"}), tty).ASCII {
		t.Error("an unknown MOTITA_ASCII must leave the detection alone")
	}
}

// TestIsTerminal: only a character device counts, seen through the filter; a buffer, a regular
// file, a closed file and nil do not.
func TestIsTerminal(t *testing.T) {
	tty := charDevice(t)
	if !IsTerminal(tty) {
		t.Error("a character device is a terminal")
	}
	if !IsTerminal(Profile{ASCII: true}.Writer(tty)) {
		t.Error("the filter must not hide the terminal behind it")
	}
	if IsTerminal(&bytes.Buffer{}) || IsTerminal(nil) {
		t.Error("a buffer or nothing is not a terminal")
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	if IsTerminal(f) {
		t.Error("a regular file is not a terminal")
	}
	f.Close()
	if IsTerminal(f) {
		t.Error("a file that cannot be inspected is not a terminal")
	}
}

// TestEnableVTOutsideWindows: there is nothing to switch on a Unix terminal.
func TestEnableVTOutsideWindows(t *testing.T) {
	if !enableVT(nil) {
		t.Error("a Unix terminal interprets escapes without being asked")
	}
}
