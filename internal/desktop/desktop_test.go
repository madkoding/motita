package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

// fake records the programs a Desktop runs and answers for them.
type fake struct {
	ran     []cmd
	answer  string
	fail    string // the program that fails
	missing string // the program that is not installed
	png     []byte // what a screenshot program writes
}

func (f *fake) run(_ context.Context, c cmd) (string, error) {
	f.ran = append(f.ran, c)
	if c.name == f.fail {
		return "", errors.New(c.name + " failed")
	}
	if f.png != nil && len(c.args) > 0 && strings.HasSuffix(c.args[len(c.args)-1], "screen.png") {
		if err := os.WriteFile(c.args[len(c.args)-1], f.png, 0o600); err != nil {
			return "", err
		}
	}
	return f.answer, nil
}

func (f *fake) look(name string) (string, error) {
	if name == f.missing {
		return "", errors.New("not found")
	}
	return "/usr/bin/" + name, nil
}

// pngOf is a w x h PNG.
func pngOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func desktopWith(be backend, f *fake) *Desktop {
	return &Desktop{be: be, run: f.run, look: f.look}
}

func TestDetectPicksTheSessionsBackend(t *testing.T) {
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	none := func(string) (string, error) { return "", errors.New("no") }
	scrot := func(n string) (string, error) {
		if n == "scrot" {
			return "/usr/bin/scrot", nil
		}
		return "", errors.New("no")
	}
	cases := []struct {
		goos string
		env  map[string]string
		look func(string) (string, error)
		want string
	}{
		{"windows", nil, none, "Windows"},
		{"darwin", nil, none, "macOS"},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, none, "Linux, Wayland"},
		{"linux", map[string]string{"DISPLAY": ":0"}, scrot, "Linux, X11"},
	}
	for _, c := range cases {
		be, err := detect(c.goos, env(c.env), c.look)
		if err != nil || be.label() != c.want {
			t.Errorf("%s %v: %v %v", c.goos, c.env, be, err)
		}
	}
	if be, _ := detect("linux", env(map[string]string{"DISPLAY": ":0"}), scrot); be.(x11Backend).shooter != "scrot" {
		t.Error("the installed shooter is not the one used")
	}
	if be, _ := detect("linux", env(map[string]string{"DISPLAY": ":0"}), none); be.(x11Backend).shooter != "maim" {
		t.Error("with none installed, maim is named so the hint says what to install")
	}
	if _, err := detect("freebsd", env(nil), none); !errors.Is(err, ErrNoDisplay) {
		t.Errorf("no display: %v", err)
	}
}

func TestNewFollowsTheEnvironment(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	if _, err := New(); !errors.Is(err, ErrNoDisplay) {
		t.Fatalf("no display: %v", err)
	}
	t.Setenv("DISPLAY", ":0")
	d, err := New()
	if err != nil || d.Label() != "Linux, X11" {
		t.Fatalf("x11: %v %v", d, err)
	}
}

func TestRunCmdAnswersWhatTheProgramPrinted(t *testing.T) {
	out, err := runCmd(context.Background(), cmd{"sh", []string{"-c", "echo hi"}})
	if err != nil || out != "hi\n" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := runCmd(context.Background(), cmd{"sh", []string{"-c", "echo bad; exit 3"}}); err == nil ||
		!strings.Contains(err.Error(), "bad") {
		t.Fatalf("a failure carries what it printed: %v", err)
	}
}

func TestShotShrinksTheScreenAndMapsClicksBack(t *testing.T) {
	f := &fake{png: pngOf(2560, 1600)}
	d := desktopWith(x11Backend{shooter: "maim"}, f)
	if _, err := d.Do(context.Background(), Request{Kind: Click, X: 1, Y: 1}); err == nil {
		t.Fatal("a click before any picture has no coordinates to map")
	}
	res, err := d.Do(context.Background(), Request{Kind: Shot})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Picture
	if p.Width != 1280 || p.Height != 800 || p.ScreenW != 2560 || len(p.JPEG) == 0 {
		t.Fatalf("picture %dx%d of %dx%d", p.Width, p.Height, p.ScreenW, p.ScreenH)
	}
	if !strings.Contains(res.Text, "2560x1600") {
		t.Errorf("text: %s", res.Text)
	}
	if _, err := d.Do(context.Background(), Request{Kind: Click, X: 100, Y: 50, Double: true}); err != nil {
		t.Fatal(err)
	}
	last := f.ran[len(f.ran)-1]
	if !slices.Equal(last.args, []string{"mousemove", "200", "100", "click", "--repeat", "2", "1"}) {
		t.Fatalf("click ran %v", last.args)
	}
}

func TestShotReadsTheSizeInPointsWhereTheDesktopHasOne(t *testing.T) {
	f := &fake{png: pngOf(400, 300), answer: "0, 0, 200, 150\n"}
	d := desktopWith(macBackend{}, f)
	res, err := d.Do(context.Background(), Request{Kind: Shot})
	if err != nil || res.Picture.ScreenW != 200 || res.Picture.Width != 400 {
		t.Fatalf("%+v %v", res.Picture, err)
	}
	if _, err := d.Do(context.Background(), Request{Kind: Move, X: 100, Y: 100}); err != nil {
		t.Fatal(err)
	}
	if got := f.ran[len(f.ran)-1].args[0]; got != "m:50,50" {
		t.Fatalf("move ran %s", got)
	}
}

func TestShotFailures(t *testing.T) {
	ctx := context.Background()
	cases := map[string]*fake{
		"not installed": {missing: "maim"},
		"not written":   {},
		"not a png":     {png: []byte("nope")},
	}
	for name, f := range cases {
		if _, err := desktopWith(x11Backend{shooter: "maim"}, f).Do(ctx, Request{Kind: Shot}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	sizes := map[string]*fake{
		"size fails":   {png: pngOf(4, 4), fail: "osascript"},
		"no size":      {png: pngOf(4, 4), answer: "nothing"},
		"size of zero": {png: pngOf(4, 4), answer: "0, 0, 0, 0"},
	}
	for name, f := range sizes {
		if _, err := desktopWith(macBackend{}, f).Do(ctx, Request{Kind: Shot}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	t.Setenv("TMPDIR", "/nonexistent/motita")
	if _, err := desktopWith(macBackend{}, &fake{}).Do(ctx, Request{Kind: Shot}); err == nil {
		t.Error("no temporary folder: no error")
	}
}

// shotDesktop is a Desktop that has taken a 1:1 picture.
func shotDesktop(be backend, f *fake) *Desktop {
	d := desktopWith(be, f)
	d.scale = 1
	return d
}

func TestEveryStepRunsOrSaysWhy(t *testing.T) {
	ctx := context.Background()
	f := &fake{answer: "1 term\n"}
	d := shotDesktop(x11Backend{}, f)
	ok := []Request{
		{Kind: Click, X: 1, Y: 2, Button: "Right"}, {Kind: Move, X: 3, Y: 4}, {Kind: Scroll, X: 1, Y: 1, Amount: -2},
		{Kind: Type, Text: "hola"}, {Kind: Key, Keys: "ctrl+c"}, {Kind: Windows}, {Kind: Focus, Window: "term"},
	}
	for _, r := range ok {
		if res, err := d.Do(ctx, r); err != nil || res.Text == "" {
			t.Errorf("%+v: %q %v", r, res.Text, err)
		}
	}
	bad := []Request{
		{Kind: Click, X: 1, Y: 1, Button: "fourth"}, {Kind: Click, X: -1, Y: 0}, {Kind: Scroll, X: 1, Y: 1},
		{Kind: Type}, {Kind: Key, Keys: "ctrl+nope"}, {Kind: Focus, Window: " "}, {Kind: "dance", X: 1, Y: 1},
	}
	for _, r := range bad {
		if _, err := d.Do(ctx, r); err == nil {
			t.Errorf("%+v: no error", r)
		}
	}
	if _, err := shotDesktop(waylandBackend{}, f).Do(ctx, Request{Kind: Windows}); err == nil ||
		!strings.Contains(err.Error(), "Wayland") {
		t.Errorf("a step the desktop cannot do: %v", err)
	}
	if _, err := shotDesktop(winBackend{}, f).Do(ctx, Request{Kind: Key, Keys: "super+e"}); err == nil {
		t.Error("a combination the desktop cannot press: no error")
	}
	if _, err := shotDesktop(x11Backend{}, &fake{fail: "xdotool"}).Do(ctx, Request{Kind: Move}); err == nil {
		t.Error("a program that fails: no error")
	}
	if _, err := shotDesktop(x11Backend{}, &fake{missing: "xdotool"}).Do(ctx, Request{Kind: Move}); err == nil ||
		!strings.Contains(err.Error(), "apt install xdotool") {
		t.Errorf("a program that is missing is named with its install: %v", err)
	}
	if res, _ := shotDesktop(x11Backend{}, &fake{}).Do(ctx, Request{Kind: Windows}); res.Text != "(no windows)" {
		t.Errorf("empty listing: %q", res.Text)
	}
}

func TestInstallHints(t *testing.T) {
	if installHint("cliclick") != "install cliclick (brew install cliclick)" || installHint("zz") != "install zz" {
		t.Fatal(installHint("zz"))
	}
}

func TestParseKeys(t *testing.T) {
	good := map[string]string{
		"ctrl+c": "ctrl+c", "Ctrl + Shift + T": "ctrl+shift+t", "cmd+space": "super+space", "Return": "enter",
		"super": "super", "+": "+", "ctrl++": "ctrl++", "F5": "f5", "alt+tab": "alt+tab",
	}
	for in, want := range good {
		k, err := parseKeys(in)
		if err != nil || k.String() != want {
			t.Errorf("%q: %q %v", in, k.String(), err)
		}
	}
	for _, in := range []string{"", "c+ctrl", "ctrl+bogus"} {
		if _, err := parseKeys(in); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

// pressed is what a backend runs for a combination.
func pressed(t *testing.T, be backend, combo string) []cmd {
	t.Helper()
	k, err := parseKeys(combo)
	if err != nil {
		t.Fatal(err)
	}
	cmds, err := be.press(k)
	if err != nil {
		t.Fatalf("%s %s: %v", be.label(), combo, err)
	}
	return cmds
}

func TestX11Commands(t *testing.T) {
	b := x11Backend{}
	for shooter, want := range map[string]string{"scrot": "scrot", "import": "import", "maim": "maim", "": "maim"} {
		if got := (x11Backend{shooter: shooter}).shot("f.png")[0].name; got != want {
			t.Errorf("%s: %s", shooter, got)
		}
	}
	checks := map[string][]string{
		"key":      pressed(t, b, "ctrl+pagedown")[0].args,
		"symbol":   pressed(t, b, "ctrl+/")[0].args,
		"function": pressed(t, b, "f11")[0].args,
		"letter":   pressed(t, b, "a")[0].args,
		"alone":    pressed(t, b, "super")[0].args,
		"scroll":   b.scroll(1, 2, 3)[0].args,
		"click":    b.click(1, 2, "middle", false)[0].args,
	}
	want := map[string][]string{
		"key":      {"key", "--clearmodifiers", "--", "ctrl+Next"},
		"symbol":   {"key", "--clearmodifiers", "--", "ctrl+slash"},
		"function": {"key", "--clearmodifiers", "--", "F11"},
		"letter":   {"key", "--clearmodifiers", "--", "a"},
		"alone":    {"key", "--clearmodifiers", "--", "super"},
		"scroll":   {"mousemove", "1", "2", "click", "--repeat", "3", "5"},
		"click":    {"mousemove", "1", "2", "click", "2"},
	}
	for name, got := range checks {
		if !slices.Equal(got, want[name]) {
			t.Errorf("%s: %v", name, got)
		}
	}
	if b.size() != nil || b.write("x")[0].args[3] != "--" || b.windows()[0].name != "wmctrl" ||
		b.focus("w")[0].args[1] != "w" {
		t.Error("x11 steps")
	}
}

func TestWaylandCommands(t *testing.T) {
	b := waylandBackend{}
	if got := pressed(t, b, "ctrl+shift+t")[0].args; !slices.Equal(got,
		[]string{"key", "29:1", "42:1", "20:1", "20:0", "42:0", "29:0"}) {
		t.Errorf("key: %v", got)
	}
	k, _ := parseKeys("ctrl++")
	if _, err := b.press(k); err == nil {
		t.Error("+ has no key of its own")
	}
	click := b.click(5, 6, "left", true)
	if len(click) != 2 || !slices.Equal(click[1].args, []string{"click", "--repeat", "2", "--next-delay", "80", "0xC0"}) {
		t.Errorf("click: %v", click)
	}
	if got := b.scroll(0, 0, 2)[1].args; got[len(got)-1] != "-2" {
		t.Errorf("scroll down is a negative wheel: %v", got)
	}
	if b.shot("f")[0].name != "grim" || b.size() != nil || b.write("x")[0].name != "ydotool" || b.focus("w") != nil {
		t.Error("wayland steps")
	}
}

func TestMacCommands(t *testing.T) {
	b := macBackend{}
	if got := pressed(t, b, "cmd+shift+left")[0].args[3]; got !=
		`tell application "System Events" to key code 123 using {command down, shift down}` {
		t.Errorf("named: %s", got)
	}
	char := pressed(t, b, "c")[0].args
	if char[3] != `tell application "System Events" to keystroke (item 1 of argv)` || char[len(char)-1] != "c" {
		t.Errorf("char: %v", char)
	}
	if _, err := b.press(keys{mods: []string{"super"}}); err == nil {
		t.Error("a modifier alone: no error")
	}
	if b.click(1, 2, "middle", false) != nil || b.click(1, 2, "left", true)[0].args[0] != "dc:1,2" ||
		b.click(1, 2, "right", true)[0].args[0] != "rc:1,2" {
		t.Error("mac clicks")
	}
	if b.scroll(1, 1, 1) != nil || b.shot("f")[0].name != "screencapture" || b.write("x")[0].name != "osascript" {
		t.Error("mac steps")
	}
	focus := b.focus("Safari")[0].args
	if b.windows()[0].name != "osascript" || focus[len(focus)-1] != "Safari" {
		t.Error("mac windows")
	}
}

// decoded is the script a PowerShell step runs.
func decoded(t *testing.T, c cmd) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(c.args[len(c.args)-1])
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

func TestWindowsCommands(t *testing.T) {
	b := winBackend{}
	scripts := map[string]string{
		"shot":   decoded(t, b.shot(`C:\a'b.png`)[0]),
		"click":  decoded(t, b.click(1, 2, "right", true)[0]),
		"move":   decoded(t, b.move(3, 4)[0]),
		"type":   decoded(t, b.write("a+b's")[0]),
		"named":  decoded(t, pressed(t, b, "ctrl+alt+delete")[0]),
		"fkey":   decoded(t, pressed(t, b, "shift+f4")[0]),
		"char":   decoded(t, pressed(t, b, "ctrl+%")[0]),
		"scroll": decoded(t, b.scroll(5, 6, 2)[0]),
		"list":   decoded(t, b.windows()[0]),
		"focus":  decoded(t, b.focus("Bloc de notas")[0]),
	}
	want := map[string]string{
		"shot": `'C:\a''b.png'`, "click": "1..2|%{[M]::mouse_event(0x8,", "move": "SetCursorPos(3,4)",
		"type": "SendWait('a{+}b''s')", "named": "SendWait('^%{DELETE}')", "fkey": "SendWait('+{F4}')",
		"char": "SendWait('^{%}')", "scroll": "mouse_event(0x800,0,0,-240,", "list": "MainWindowTitle",
		"focus": "AppActivate('Bloc de notas')",
	}
	for name, s := range scripts {
		if !strings.HasPrefix(s, winPrelude) || !strings.Contains(s, want[name]) {
			t.Errorf("%s: %s", name, strings.TrimPrefix(s, winPrelude))
		}
	}
	if _, err := b.press(keys{mods: []string{"ctrl"}}); err == nil {
		t.Error("a modifier alone: no error")
	}
	if b.size() != nil || b.label() != "Windows" {
		t.Error("windows size")
	}
}

func TestShrinkKeepsTheShape(t *testing.T) {
	small := image.NewRGBA(image.Rect(0, 0, 10, 20))
	if got := shrink(small, 100).Bounds(); got.Dx() != 10 || got.Dy() != 20 {
		t.Errorf("small: %v", got)
	}
	tall := image.NewRGBA(image.Rect(0, 0, 300, 3000))
	if got := shrink(tall, 1000).Bounds(); got.Dx() != 100 || got.Dy() != 1000 {
		t.Errorf("tall: %v", got)
	}
	if r, g, b, a := (colorRGBA{1, 2, 3}).RGBA(); r != 0x101 || g != 0x202 || b != 0x303 || a != 0xffff {
		t.Error("colour")
	}
}
