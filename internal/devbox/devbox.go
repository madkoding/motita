// Package devbox runs a project's service in a container that only motita can reach, so the agent
// can test it and take screenshots of it as evidence.
//
// The container is the same kind of thing a person would start with `podman run`, with these
// differences, which are the whole point:
//
//   - by default its port is published on 127.0.0.1 and nowhere else (never 0.0.0.0, never the
//     network), and motita is the one that talks to it: the agent's own commands run without a
//     network and cannot reach it, so the only way in is the get and shot actions, which build the
//     address themselves from the box they started;
//   - in the internal mode (Spec.Internal, and always for compose) NO port reaches the host at all:
//     the service sits on a network the runtime created with --internal, and motita reaches it from
//     short-lived helper containers on that network (a curl for get, a headless browser for shot);
//   - nothing of the host is mounted into it and it is never privileged, so what runs inside
//     (the project's code, its dependencies) cannot read the user's files;
//   - it belongs to the run that started it and is removed when the run ends, and a box left by
//     a process that died is swept at the next start (see Sweep); Active and Remove let a person
//     see and stop the boxes that exist.
//
// The model never writes the runtime's command line: it names an image, a folder or a compose
// project and a port, and this package builds the argv. No shell is involved anywhere.
package devbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Label marks every container this package starts, so the sweep finds them and nothing else.
	Label = "motita.box"
	// OwnerLabel names the run that started a container.
	OwnerLabel = "motita.owner"

	// MaxBoxes bounds how many boxes one run keeps up at once.
	MaxBoxes = 3

	// bodyLimit is how much of a response get returns.
	bodyLimit = 4096

	readyTimeout = 90 * time.Second
	buildTimeout = 15 * time.Minute
	shotTimeout  = 60 * time.Second
)

var (
	imageRef = regexp.MustCompile(`^[a-z0-9][a-zA-Z0-9._/:@-]{0,254}$`)
	shotName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// serviceName is a compose service: it ends up in an address and in argv.
	serviceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
)

// Seams: what the machine does, as variables so a test says what the machine has.
var (
	lookPath = exec.LookPath
	// runCmd runs the runtime with args and returns what it printed (both streams).
	runCmd = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, bin, args...).CombinedOutput()
	}
	getURL = func(ctx context.Context, url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		return http.DefaultClient.Do(req)
	}
	pollEvery = 500 * time.Millisecond
	euid      = os.Geteuid
	globFiles = filepath.Glob
	getenv    = os.Getenv
	tempDir   = os.MkdirTemp
)

// Runtime is the container tool the box runs with.
type Runtime struct{ Bin string }

// Detect finds the runtime: podman first (it needs no daemon and runs rootless), then docker.
// Each one counts only if it answers `info`, because a binary whose setup was never finished is
// there and cannot run anything.
func Detect(ctx context.Context) (Runtime, error) {
	for _, bin := range []string{"podman", "docker"} {
		if _, err := lookPath(bin); err != nil {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, err := runCmd(c, bin, "info")
		cancel()
		if err == nil {
			return Runtime{Bin: bin}, nil
		}
	}
	return Runtime{}, errors.New("no container runtime answers: install podman (preferred, rootless) or docker " +
		"and check that `podman info` or `docker info` works")
}

// Spec says what to start. Exactly one of Image, Dir and Compose is set.
type Spec struct {
	// Image is a reference to pull or find locally.
	Image string
	// Dir is an absolute folder holding a Dockerfile or Containerfile, built into an image.
	Dir string
	// Compose is an absolute folder holding a compose file; Service is the one motita reaches.
	// A compose project always runs in the internal mode.
	Compose string
	Service string
	// Port is the port the service listens on inside the container.
	Port int
	// Internal publishes nothing on the host: see the package comment.
	Internal bool
}

// Box is a service that is up.
type Box struct {
	Name string
	// URL is the address motita reaches it at: http://127.0.0.1:<port>, or, in the internal mode,
	// http://<container or service>:<port>, which only exists inside the box's network.
	URL string

	internal bool
	network  string // the network helpers join; created by Up unless compose made it
	ownsNet  bool
	image    string // the image built for it, "" when it was pulled
	service  string
	compose  []string // the compose invocation up to the verb, nil for a plain container
	scratch  string   // a folder of ours to remove with the box
}

// Internal reports whether the box has no port on the host.
func (b *Box) Internal() bool { return b.internal }

// Manager keeps the boxes of one run.
type Manager struct {
	rt    Runtime
	owner string
	mu    sync.Mutex
	seq   int
	boxes map[string]*Box
	have  map[string]bool // helper images known to be present
}

// New returns a manager for the run named owner (a short random id).
func New(rt Runtime, owner string) *Manager {
	return &Manager{rt: rt, owner: owner, boxes: map[string]*Box{}, have: map[string]bool{}}
}

// Runtime is the tool the manager uses.
func (m *Manager) Runtime() string { return m.rt.Bin }

// List returns the names of the boxes that are up, sorted.
func (m *Manager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sortedKeys(m.boxes)
}

func sortedNames(names []string) []string {
	sort.Strings(names)
	return names
}

// Validate checks a spec before anything runs.
func (s Spec) Validate() error {
	sources := 0
	for _, v := range []string{s.Image, s.Dir, s.Compose} {
		if v != "" {
			sources++
		}
	}
	switch {
	case sources != 1:
		return errors.New("name exactly one of image, dir and compose")
	case s.Port < 1 || s.Port > 65535:
		return fmt.Errorf("port %d is not a port", s.Port)
	case s.Image != "" && !imageRef.MatchString(s.Image):
		return fmt.Errorf("%q is not an image reference", s.Image)
	case s.Compose != "" && !serviceName.MatchString(s.Service):
		return fmt.Errorf("compose needs \"service:\", the name of the service to reach (%q is not one)", s.Service)
	case s.Compose == "" && s.Service != "":
		return errors.New(`"service:" belongs to compose`)
	default:
		return nil
	}
}

// runFlags are the limits every container of ours starts with.
func (m *Manager) labels() []string {
	return []string{"--label", Label + "=1", "--label", OwnerLabel + "=" + m.owner}
}

// Up starts the service and waits until it answers over HTTP. A box that never answers is removed
// and the error carries the tail of its log, which is what the model needs to fix it.
func (m *Manager) Up(ctx context.Context, s Spec) (*Box, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if len(m.boxes) >= MaxBoxes {
		m.mu.Unlock()
		return nil, fmt.Errorf("%d boxes are already up: take one down first", MaxBoxes)
	}
	m.seq++
	name := fmt.Sprintf("motita-box-%s-%d", m.owner, m.seq)
	m.mu.Unlock()

	b := &Box{Name: name, internal: s.Internal || s.Compose != "", service: s.Service}
	var err error
	if s.Compose != "" {
		err = m.upCompose(ctx, b, s)
	} else {
		err = m.upContainer(ctx, b, s)
	}
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.boxes[name] = b
	m.mu.Unlock()
	return b, nil
}

// upContainer builds (when asked), networks and starts one container.
func (m *Manager) upContainer(ctx context.Context, b *Box, s Spec) error {
	image := s.Image
	if s.Dir != "" {
		image = b.Name
		bctx, cancel := context.WithTimeout(ctx, buildTimeout)
		out, err := runCmd(bctx, m.rt.Bin, append(append([]string{"build"}, m.labels()...), "-t", image, s.Dir)...)
		cancel()
		if err != nil {
			return fmt.Errorf("the image did not build: %v\n%s", err, tail(string(out), 1500))
		}
		b.image = image
	}
	args := append([]string{"run", "-d", "--name", b.Name}, m.labels()...)
	args = append(args, "--security-opt", "no-new-privileges", "--pids-limit", "512", "--memory", "1g")
	if b.internal {
		b.network = "motita-net-" + strings.TrimPrefix(b.Name, "motita-box-")
		mk := append(append([]string{"network", "create", "--internal"}, m.labels()...), b.network)
		if out, err := runCmd(ctx, m.rt.Bin, mk...); err != nil {
			m.destroy(ctx, b)
			return fmt.Errorf("the private network was not created: %v\n%s", err, tail(string(out), 800))
		}
		b.ownsNet = true
		args = append(args, "--network", b.network)
		b.URL = "http://" + b.Name + ":" + strconv.Itoa(s.Port)
	} else {
		// 127.0.0.1 and a port the runtime picks: never the whole network, never a fixed port that
		// something else on the machine may hold.
		args = append(args, "-p", "127.0.0.1::"+strconv.Itoa(s.Port))
	}
	if out, err := runCmd(ctx, m.rt.Bin, append(args, image)...); err != nil {
		m.destroy(ctx, b)
		return fmt.Errorf("the container did not start: %v\n%s", err, tail(string(out), 1500))
	}
	var err error
	if !b.internal {
		b.URL, err = m.address(ctx, b.Name, s.Port)
	}
	if err == nil {
		err = m.waitReady(ctx, b)
	}
	if err != nil {
		logs, _ := runCmd(ctx, m.rt.Bin, "logs", "--tail", "40", b.Name)
		m.destroy(ctx, b)
		return fmt.Errorf("%v\nlast log lines:\n%s", err, tail(string(logs), 1500))
	}
	return nil
}

// address asks the runtime which host port it chose and refuses anything but loopback: if the
// runtime ever published the port wider than asked, the box is not used.
func (m *Manager) address(ctx context.Context, name string, port int) (string, error) {
	out, err := runCmd(ctx, m.rt.Bin, "port", name, strconv.Itoa(port)+"/tcp")
	if err != nil {
		return "", fmt.Errorf("the published port is unknown: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if host, hp, ok := strings.Cut(line, ":"); ok && host == "127.0.0.1" {
			if _, err := strconv.Atoi(hp); err == nil {
				return "http://127.0.0.1:" + hp, nil
			}
		}
	}
	return "", fmt.Errorf("the container is not published on 127.0.0.1 only (%q)", strings.TrimSpace(string(out)))
}

// waitReady polls until the service answers any HTTP status.
func (m *Manager) waitReady(ctx context.Context, b *Box) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	for {
		if m.answers(ctx, b) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the service did not answer at %s in %s", b.URL, readyTimeout)
		case <-time.After(pollEvery):
		}
	}
}

// answers reports whether the service replies to GET / with any HTTP status.
func (m *Manager) answers(ctx context.Context, b *Box) bool {
	if b.internal {
		_, err := m.helperCurl(ctx, b, "/", "-s", "-o", "/dev/null", "--max-time", "3")
		return err == nil
	}
	resp, err := getURL(ctx, b.URL)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// box returns a running box by name, or the only one when name is empty.
func (m *Manager) box(name string) (*Box, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.boxes[name]; ok {
		return b, nil
	}
	if name == "" && len(m.boxes) == 1 {
		return m.only(), nil
	}
	return nil, fmt.Errorf("no box named %q is up (up: %s)", name, strings.Join(sortedKeys(m.boxes), ", "))
}

// only is the single box of a manager that has exactly one. The caller holds the lock.
func (m *Manager) only() *Box {
	return m.boxes[sortedKeys(m.boxes)[0]]
}

func sortedKeys(m map[string]*Box) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	return sortedNames(names)
}

// Get fetches path from a box and returns the status line, the content type and the start of the
// body. The address is the box's own: a path cannot name another host.
func (m *Manager) Get(ctx context.Context, name, path string) (string, error) {
	b, err := m.box(name)
	if err != nil {
		return "", err
	}
	u, err := b.join(path)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if b.internal {
		// -i keeps the status line and the headers in the answer, like the direct path writes them.
		path := strings.TrimPrefix(u, b.URL)
		out, err := m.helperCurl(ctx, b, path, "-sS", "-i", "--max-time", "20", "--max-filesize", "1048576")
		if err != nil {
			return "", fmt.Errorf("GET %s failed: %v\n%s", path, err, tail(out, 500))
		}
		return clip(out), nil
	}
	resp, err := getURL(ctx, u)
	if err != nil {
		return "", fmt.Errorf("GET %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	ctype := resp.Header.Get("Content-Type")
	return fmt.Sprintf("HTTP %s\nContent-Type: %s\n\n%s", resp.Status, ctype, clip(string(body))), nil
}

// clip keeps the start of a response.
func clip(s string) string {
	if len(s) > bodyLimit {
		return s[:bodyLimit] + "\n[body cut at 4096 bytes]"
	}
	return s
}

// join builds the address of path on the box. Only a path is accepted, so a model cannot turn the
// action into a request to some other host (`//host/x`, `@host`, a full URL).
func (b *Box) join(path string) (string, error) {
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, " \t\r\n\\@") {
		return "", fmt.Errorf("%q is not a path on the box: write it like /login", path)
	}
	return b.URL + path, nil
}

// Shot opens path on a box in a headless browser and saves a PNG to out. A box with a port on the
// loopback is photographed by the browser on this machine; an internal box, by a browser in a
// container on its network.
func (m *Manager) Shot(ctx context.Context, name, path, out string, width, height int) error {
	b, err := m.box(name)
	if err != nil {
		return err
	}
	u, err := b.join(path)
	if err != nil {
		return err
	}
	if width < 200 || width > 3000 || height < 200 || height > 3000 {
		return fmt.Errorf("a %dx%d window is out of range (200 to 3000)", width, height)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if b.internal {
		return m.shotInside(ctx, b, u, out, width, height)
	}
	bin, err := Browser()
	if err != nil {
		return err
	}
	profile, err := tempDir("", "motita-shot-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)
	args := []string{"--headless=new", "--disable-gpu", "--hide-scrollbars",
		"--user-data-dir=" + profile, "--virtual-time-budget=8000",
		fmt.Sprintf("--window-size=%d,%d", width, height), "--screenshot=" + out}
	if euid() == 0 {
		// Chromium refuses to start as root with its own sandbox on; the box, not the browser,
		// is what is being kept apart here, and the page is the user's own project.
		args = append(args, "--no-sandbox")
	}
	cctx, cancel := context.WithTimeout(ctx, shotTimeout)
	defer cancel()
	if o, err := runCmd(cctx, bin, append(args, u)...); err != nil {
		return fmt.Errorf("the browser failed: %v\n%s", err, tail(string(o), 800))
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return errors.New("the browser ran but wrote no picture")
	}
	return nil
}

// ShotName checks the file name a screenshot is saved under.
func ShotName(n string) (string, error) {
	n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".png")
	if !shotName.MatchString(n) {
		return "", fmt.Errorf("%q is not a file name: use lowercase letters, digits, - and _", n)
	}
	return n + ".png", nil
}

// Browser finds a Chromium or Chrome: $MOTITA_BROWSER, then the usual names on PATH, then a
// Playwright download.
func Browser() (string, error) {
	if b := getenv("MOTITA_BROWSER"); b != "" {
		return b, nil
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if p, err := lookPath(n); err == nil {
			return p, nil
		}
	}
	root := getenv("PLAYWRIGHT_BROWSERS_PATH")
	if root == "" {
		root = "/opt/pw-browsers"
	}
	for _, pat := range []string{"chromium*/chrome-linux*/chrome", "chromium"} {
		if m, _ := globFiles(filepath.Join(root, pat)); len(m) > 0 {
			return m[len(m)-1], nil
		}
	}
	return "", errors.New("no Chromium or Chrome found: set MOTITA_BROWSER or install one " +
		"(the screenshot is taken by motita, not by a command in the sandbox)")
}

// Down removes one box.
func (m *Manager) Down(ctx context.Context, name string) error {
	b, err := m.box(name)
	if err != nil {
		return err
	}
	m.destroy(ctx, b)
	m.mu.Lock()
	delete(m.boxes, b.Name)
	m.mu.Unlock()
	return nil
}

// DownAll removes every box of the run. It does not use the caller's context: it is what runs
// when that context is already cancelled.
func (m *Manager) DownAll() {
	m.mu.Lock()
	all := make([]*Box, 0, len(m.boxes))
	for _, n := range sortedKeys(m.boxes) {
		all = append(all, m.boxes[n])
	}
	m.boxes = map[string]*Box{}
	m.mu.Unlock()
	for _, b := range all {
		m.destroy(context.Background(), b)
	}
}

// destroy removes whatever of a box exists: its containers, the image built for it and the
// network made for it. Failures are ignored: something that is already gone is the goal, and this
// also runs on a box that failed half way.
func (m *Manager) destroy(ctx context.Context, b *Box) {
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if b.compose != nil {
		down := append(append([]string{}, b.compose...), "down", "-v", "--rmi", "local", "--remove-orphans")
		_, _ = runCmd(c, m.rt.Bin, down...)
		if b.scratch != "" {
			_ = os.RemoveAll(b.scratch)
		}
		return
	}
	_, _ = runCmd(c, m.rt.Bin, "rm", "-f", b.Name)
	if b.image != "" {
		_, _ = runCmd(c, m.rt.Bin, "rmi", "-f", b.image)
	}
	if b.ownsNet {
		_, _ = runCmd(c, m.rt.Bin, "network", "rm", b.network)
	}
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
