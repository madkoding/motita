package devbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake stands in for the machine: the runtime's answers, the HTTP service and the browser.
type fake struct {
	mu     sync.Mutex
	calls  [][]string
	answer func(args []string) ([]byte, error)
	http   func(url string) (*http.Response, error)
}

func (f *fake) install(t *testing.T) {
	t.Helper()
	oldRun, oldGet, oldLook, oldPoll := runCmd, getURL, lookPath, pollEvery
	t.Cleanup(func() { runCmd, getURL, lookPath, pollEvery = oldRun, oldGet, oldLook, oldPoll })
	pollEvery = time.Millisecond
	lookPath = func(n string) (string, error) { return "/usr/bin/" + n, nil }
	runCmd = func(_ context.Context, bin string, args ...string) ([]byte, error) {
		f.mu.Lock()
		f.calls = append(f.calls, append([]string{bin}, args...))
		f.mu.Unlock()
		if f.answer != nil {
			return f.answer(args)
		}
		return nil, nil
	}
	getURL = func(_ context.Context, url string) (*http.Response, error) {
		if f.http != nil {
			return f.http(url)
		}
		return reply(200, "text/html", "<h1>hi</h1>"), nil
	}
}

func (f *fake) ran(verb string) int {
	n := 0
	for _, c := range f.calls {
		if len(c) > 1 && c[1] == verb {
			n++
		}
	}
	return n
}

func reply(code int, ctype, body string) *http.Response {
	h := http.Header{}
	h.Set("Content-Type", ctype)
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: h,
		Body: io.NopCloser(strings.NewReader(body))}
}

func runtimeThatPublishes(port string) func([]string) ([]byte, error) {
	return func(args []string) ([]byte, error) {
		if args[0] == "port" {
			return []byte(port + "\n"), nil
		}
		return nil, nil
	}
}

func TestDetectPrefersPodmanThenDocker(t *testing.T) {
	f := &fake{}
	f.install(t)
	rt, err := Detect(context.Background())
	if err != nil || rt.Bin != "podman" {
		t.Fatalf("podman first: %v %v", rt, err)
	}
	f.answer = func([]string) ([]byte, error) { return nil, nil }
	runCmd2 := runCmd
	runCmd = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		if bin == "podman" {
			return nil, errors.New("rootless setup unfinished")
		}
		return runCmd2(ctx, bin, args...)
	}
	if rt, err = Detect(context.Background()); err != nil || rt.Bin != "docker" {
		t.Fatalf("podman does not answer, docker does: %v %v", rt, err)
	}
	runCmd = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("down") }
	if _, err = Detect(context.Background()); err == nil || !strings.Contains(err.Error(), "podman") {
		t.Fatalf("neither answers: %v", err)
	}
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	if _, err = Detect(context.Background()); err == nil {
		t.Fatal("neither is installed")
	}
}

func TestSpecValidation(t *testing.T) {
	for name, s := range map[string]Spec{
		"neither":   {Port: 80},
		"both":      {Image: "nginx", Dir: "/x", Port: 80},
		"port low":  {Image: "nginx"},
		"port high": {Image: "nginx", Port: 70000},
		"flag":      {Image: "--privileged", Port: 80},
		"space":     {Image: "nginx --net=host", Port: 80},
	} {
		if s.Validate() == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	good := []Spec{
		{Image: "nginx:alpine", Port: 80},
		{Image: "ghcr.io/a/b@sha256:ab12", Port: 1},
		{Dir: "/w/app", Port: 3000},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
}

func TestUpPublishesOnLoopbackOnlyAndIsHardened(t *testing.T) {
	f := &fake{answer: runtimeThatPublishes("0.0.0.0:49000\n127.0.0.1:49153")}
	f.install(t)
	m := New(Runtime{Bin: "podman"}, "ab12")
	b, err := m.Up(context.Background(), Spec{Image: "nginx:alpine", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	if b.URL != "http://127.0.0.1:49153" || m.Runtime() != "podman" {
		t.Fatalf("box %+v", b)
	}
	var run []string
	for _, c := range f.calls {
		if c[1] == "run" {
			run = c
		}
	}
	joined := strings.Join(run, " ")
	wants := []string{"-p 127.0.0.1::80", "no-new-privileges", "--pids-limit", "--memory", "motita.box=1",
		"motita.owner=ab12", "nginx:alpine"}
	for _, want := range wants {
		if !strings.Contains(joined, want) {
			t.Errorf("run lacks %q: %s", want, joined)
		}
	}
	for _, bad := range []string{"--privileged", "-v ", "--volume", "--network host", "--net=host"} {
		if strings.Contains(joined, bad) {
			t.Errorf("run must not contain %q: %s", bad, joined)
		}
	}
	if got := m.List(); len(got) != 1 || got[0] != b.Name {
		t.Fatalf("list %v", got)
	}
}

func TestUpBuildsADirectoryAndRemovesTheImageWithTheBox(t *testing.T) {
	f := &fake{answer: runtimeThatPublishes("127.0.0.1:50000")}
	f.install(t)
	m := New(Runtime{Bin: "docker"}, "cd34")
	b, err := m.Up(context.Background(), Spec{Dir: "/w/app", Port: 3000})
	if err != nil {
		t.Fatal(err)
	}
	if f.ran("build") != 1 || !strings.Contains(strings.Join(f.calls[0], " "), "build --label motita.box=1") {
		t.Fatalf("calls %v", f.calls)
	}
	if err := m.Down(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if f.ran("rm") != 1 || f.ran("rmi") != 1 || len(m.List()) != 0 {
		t.Fatalf("down: %v", f.calls)
	}
	if err := m.Down(context.Background(), b.Name); err == nil {
		t.Fatal("already down")
	}
}

func failing(verb, out string) func([]string) ([]byte, error) {
	return func(a []string) ([]byte, error) {
		if a[0] == verb {
			return []byte(out), errors.New("exit 1")
		}
		return nil, nil
	}
}

// upFailure is a start that goes wrong, and what must be left of it.
type upFailure struct {
	name       string
	spec       Spec
	answer     func([]string) ([]byte, error)
	http       func(string) (*http.Response, error)
	want       string
	rm, rmi    int
	timeoutMs  int
	wantNoRuns bool
}

func (c upFailure) check(t *testing.T) {
	f := &fake{answer: c.answer, http: c.http}
	f.install(t)
	ctx := context.Background()
	if c.timeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(c.timeoutMs)*time.Millisecond)
		defer cancel()
	}
	_, err := New(Runtime{Bin: "podman"}, "x").Up(ctx, c.spec)
	if err == nil || !strings.Contains(err.Error(), c.want) {
		t.Fatalf("error %v, want %q", err, c.want)
	}
	if c.wantNoRuns && f.ran("run") != 0 {
		t.Fatalf("nothing may start: %v", f.calls)
	}
	if f.ran("rm") != c.rm || f.ran("rmi") != c.rmi {
		t.Fatalf("rm %d rmi %d, calls %v", f.ran("rm"), f.ran("rmi"), f.calls)
	}
}

func portAnswers(port, logs string) func([]string) ([]byte, error) {
	return func(a []string) ([]byte, error) {
		switch a[0] {
		case "port":
			if port == "" {
				return nil, errors.New("no mapping")
			}
			return []byte(port), nil
		case "logs":
			return []byte(logs), nil
		default:
			return nil, nil
		}
	}
}

func TestUpFailures(t *testing.T) {
	refused := func(string) (*http.Response, error) { return nil, errors.New("refused") }
	app := Spec{Image: "app", Port: 80}
	dir := Spec{Dir: "/w", Port: 80}
	noImage := failing("run", "no such image")
	cases := []upFailure{
		{name: "invalid spec", spec: Spec{}, want: "exactly one", wantNoRuns: true},
		{name: "build fails", spec: dir, answer: failing("build", "step 3 failed"), want: "step 3 failed", wantNoRuns: true},
		{name: "run fails", spec: dir, answer: noImage, want: "no such image", rm: 1, rmi: 1},
		{name: "a pulled image is not removed", spec: Spec{Image: "nope", Port: 80}, answer: noImage,
			want: "no such image", rm: 1},
		{name: "port unknown", spec: app, answer: portAnswers("", "listening on 3001"), want: "listening on 3001", rm: 1},
		{name: "published wider than loopback", spec: app, answer: runtimeThatPublishes("0.0.0.0:49000"),
			want: "127.0.0.1 only", rm: 1},
		{name: "port is not a number", spec: app, answer: runtimeThatPublishes("127.0.0.1:abc"),
			want: "127.0.0.1 only", rm: 1},
		{name: "never answers", spec: app, answer: runtimeThatPublishes("127.0.0.1:49000"), http: refused,
			want: "did not answer", rm: 1, timeoutMs: 30},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}

func TestOnlyAFewBoxesAtOnce(t *testing.T) {
	ctx := context.Background()
	f := &fake{answer: runtimeThatPublishes("127.0.0.1:49000")}
	f.install(t)
	m := New(Runtime{Bin: "podman"}, "x")
	for i := 0; i < MaxBoxes; i++ {
		if _, err := m.Up(ctx, Spec{Image: "app", Port: 80}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Up(ctx, Spec{Image: "app", Port: 80}); err == nil || !strings.Contains(err.Error(), "take one down") {
		t.Fatalf("%v", err)
	}
	m.DownAll()
	if len(m.List()) != 0 || f.ran("rm") != MaxBoxes {
		t.Fatalf("down all: %v", f.calls)
	}
}

func upOne(t *testing.T, f *fake) *Manager {
	t.Helper()
	f.answer = runtimeThatPublishes("127.0.0.1:49000")
	f.install(t)
	m := New(Runtime{Bin: "podman"}, "x")
	if _, err := m.Up(context.Background(), Spec{Image: "app", Port: 80}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGetOnlyReachesTheBox(t *testing.T) {
	var asked string
	f := &fake{http: func(u string) (*http.Response, error) {
		asked = u
		return reply(200, "application/json", strings.Repeat("a", bodyLimit+10)), nil
	}}
	m := upOne(t, f)
	out, err := m.Get(context.Background(), "", "/api?x=1")
	if err != nil || asked != "http://127.0.0.1:49000/api?x=1" {
		t.Fatalf("%q %v", asked, err)
	}
	if !strings.Contains(out, "application/json") || !strings.Contains(out, "[body cut at 4096 bytes]") {
		t.Fatalf("%s", out)
	}
	if _, err = m.Get(context.Background(), "", ""); err != nil || asked != "http://127.0.0.1:49000/" {
		t.Fatalf("empty path is the root: %q %v", asked, err)
	}
	evil := []string{"http://evil.example/", "//evil.example/x", "x", "/a b", `/a\b`, "/@evil.example", "/a\nb"}
	for _, p := range evil {
		if _, err := m.Get(context.Background(), "", p); err == nil {
			t.Errorf("%q must not be accepted", p)
		}
	}
	if _, err := m.Get(context.Background(), "nope", "/"); err == nil || !strings.Contains(err.Error(), "no box named") {
		t.Fatalf("%v", err)
	}
	f.http = func(string) (*http.Response, error) { return nil, errors.New("reset") }
	if _, err := m.Get(context.Background(), "", "/"); err == nil {
		t.Fatal("transport error")
	}
}

func TestBoxPicksTheOnlyOneOnly(t *testing.T) {
	f := &fake{}
	m := upOne(t, f)
	if _, err := m.Up(context.Background(), Spec{Image: "app", Port: 80}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.box(""); err == nil {
		t.Fatal("two boxes: a name is needed")
	}
	if b, err := m.box(m.List()[1]); err != nil || b.Name != m.List()[1] {
		t.Fatalf("by name: %v", err)
	}
}

// shotFixture is a box that is up and a browser on this machine whose behaviour a test sets.
type shotFixture struct {
	m    *Manager
	out  string
	args []string
	// act is what the browser does when it runs.
	act func(out string) ([]byte, error)
}

func newShotFixture(t *testing.T) *shotFixture {
	t.Helper()
	sf := &shotFixture{m: upOne(t, &fake{}), out: filepath.Join(t.TempDir(), "p", "home.png")}
	sf.act = func(out string) ([]byte, error) { return nil, os.WriteFile(out, []byte("png"), 0o644) }
	oldEuid, oldTemp := euid, tempDir
	t.Cleanup(func() { euid, tempDir = oldEuid, oldTemp })
	base := runCmd
	runCmd = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		if !strings.HasSuffix(bin, "chromium") {
			return base(ctx, bin, args...)
		}
		sf.args = args
		return sf.act(sf.out)
	}
	euid = func() int { return 1000 }
	return sf
}

func (sf *shotFixture) shoot(name, path string, w, h int) error {
	return sf.m.Shot(context.Background(), name, path, sf.out, w, h)
}

func TestShotRunsTheBrowserOnTheBoxAddress(t *testing.T) {
	sf := newShotFixture(t)
	euid = func() int { return 0 }
	if err := sf.shoot("", "/", 1280, 800); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(sf.args, " ")
	if !strings.Contains(joined, "--no-sandbox") || !strings.Contains(joined, "--window-size=1280,800") ||
		sf.args[len(sf.args)-1] != "http://127.0.0.1:49000/" {
		t.Fatalf("%s", joined)
	}
	euid = func() int { return 1000 }
	if err := sf.shoot("", "/", 1280, 800); err != nil || strings.Contains(strings.Join(sf.args, " "), "--no-sandbox") {
		t.Fatalf("not root keeps the browser's sandbox: %v %v", err, sf.args)
	}
}

func TestShotRefusesWhatIsNotAPictureOfTheBox(t *testing.T) {
	sf := newShotFixture(t)
	for name, err := range map[string]error{
		"window out of range": sf.shoot("", "/", 10, 800),
		"not a path":          sf.shoot("", "http://x/", 1280, 800),
		"unknown box":         sf.shoot("zz", "/", 1280, 800),
	} {
		if err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if sf.args != nil {
		t.Fatalf("the browser never ran: %v", sf.args)
	}
}

func TestShotFailures(t *testing.T) {
	type failure struct {
		act  func(out string) ([]byte, error)
		temp func(string, string) (string, error)
		out  func(sf *shotFixture)
		want string
	}
	crash := func(string) ([]byte, error) { return []byte("crashed"), errors.New("signal") }
	nothing := func(string) ([]byte, error) { return nil, nil }
	noTemp := func(string, string) (string, error) { return "", errors.New("no temp") }
	underAFile := func(sf *shotFixture) { sf.out = filepath.Join(sf.out, "x", "y.png") }
	cases := map[string]failure{
		"the browser crashes":      {act: crash, want: "crashed"},
		"no picture":               {act: nothing, want: "no picture"},
		"no profile folder":        {temp: noTemp, want: "no temp"},
		"cannot create the folder": {out: underAFile, want: "not a directory"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			sf := newShotFixture(t)
			if c.act != nil {
				sf.act = c.act
			}
			if c.temp != nil {
				tempDir = c.temp
			}
			if c.out != nil {
				sf.blockOutputFolder(t)
				c.out(sf)
			}
			if err := sf.shoot("", "/", 1280, 800); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v, want %q", err, c.want)
			}
		})
	}
}

// blockOutputFolder puts a file where the picture's folder would be.
func (sf *shotFixture) blockOutputFolder(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sf.out), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sf.out, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestShotWithoutABrowser(t *testing.T) {
	sf := newShotFixture(t)
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", t.TempDir())
	t.Setenv("MOTITA_BROWSER", "")
	if err := sf.shoot("", "/", 1280, 800); err == nil || !strings.Contains(err.Error(), "MOTITA_BROWSER") {
		t.Errorf("no browser: %v", err)
	}
}

func TestBrowserLookup(t *testing.T) {
	f := &fake{}
	f.install(t)
	t.Setenv("MOTITA_BROWSER", "/opt/mine/chrome")
	if b, _ := Browser(); b != "/opt/mine/chrome" {
		t.Fatalf("env wins: %s", b)
	}
	t.Setenv("MOTITA_BROWSER", "")
	if b, _ := Browser(); b != "/usr/bin/chromium" {
		t.Fatalf("PATH: %s", b)
	}
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	root := t.TempDir()
	want := filepath.Join(root, "chromium-1200", "chrome-linux64", "chrome")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", root)
	if b, _ := Browser(); b != want {
		t.Fatalf("playwright: %s", b)
	}
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", "")
	oldGlob := globFiles
	t.Cleanup(func() { globFiles = oldGlob })
	globFiles = func(p string) ([]string, error) {
		if !strings.HasPrefix(p, "/opt/pw-browsers/") {
			t.Errorf("default root: %s", p)
		}
		return nil, nil
	}
	if _, err := Browser(); err == nil {
		t.Fatal("none anywhere")
	}
}

func TestShotName(t *testing.T) {
	if n, err := ShotName(" Home-After.PNG "); err != nil || n != "home-after.png" {
		t.Fatalf("%q %v", n, err)
	}
	for _, bad := range []string{"", "../x", "a/b", ".hidden", "a b", strings.Repeat("a", 70)} {
		if _, err := ShotName(bad); err == nil {
			t.Errorf("%q", bad)
		}
	}
}

func TestTail(t *testing.T) {
	if tail(" abc ", 10) != "abc" || tail("abcdef", 3) != "…def" {
		t.Fatal("tail")
	}
}

func TestRealSeamsAreWired(t *testing.T) {
	out, err := runCmd(context.Background(), "go", "version")
	if err != nil || !strings.Contains(string(out), "go version") {
		t.Fatalf("runCmd: %v %s", err, out)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer ts.Close()
	resp, err := getURL(context.Background(), ts.URL)
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("getURL: %v", err)
	}
	resp.Body.Close()
	if _, err := getURL(context.Background(), "http://127.0.0.1:1/"); err == nil {
		t.Fatal("nothing listens on port 1")
	}
	if _, err := getURL(context.Background(), "http://bad host/"); err == nil {
		t.Fatal("bad url")
	}
}
