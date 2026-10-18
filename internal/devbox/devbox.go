// Package devbox runs a project's service in a container that only motita can reach, so the agent
// can test it and take screenshots of it as evidence.
//
// The container is the same kind of thing a person would start with `podman run`, with three
// differences that are the whole point:
//
//   - its port is published on 127.0.0.1 and nowhere else (never 0.0.0.0, never the network), and
//     motita is the one that talks to it: the agent's own commands run without a network and
//     cannot reach it, so the only way in is the get and shot actions, which build the address
//     themselves from the box they started;
//   - nothing of the host is mounted into it and it is never privileged, so what runs inside
//     (the project's code, its dependencies) cannot read the user's files;
//   - it belongs to the run that started it and is removed when the run ends, and a box left by
//     a process that died is swept at the next start (see Sweep).
//
// The model never writes the runtime's command line: it names an image or a folder and a port,
// and this package builds the argv. No shell is involved anywhere.
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

// Spec says what to start. Exactly one of Image and Dir is set.
type Spec struct {
	// Image is a reference to pull or find locally.
	Image string
	// Dir is an absolute folder holding a Dockerfile or Containerfile, built into an image.
	Dir string
	// Port is the port the service listens on inside the container.
	Port int
}

// Box is a service that is up.
type Box struct {
	Name string
	// URL is the address motita reaches it at: http://127.0.0.1:<port>.
	URL string
}

// Manager keeps the boxes of one run.
type Manager struct {
	rt    Runtime
	owner string
	mu    sync.Mutex
	seq   int
	boxes map[string]*Box
}

// New returns a manager for the run named owner (a short random id).
func New(rt Runtime, owner string) *Manager {
	return &Manager{rt: rt, owner: owner, boxes: map[string]*Box{}}
}

// Runtime is the tool the manager uses.
func (m *Manager) Runtime() string { return m.rt.Bin }

// List returns the names of the boxes that are up, sorted.
func (m *Manager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.boxes))
	for n := range m.boxes {
		names = append(names, n)
	}
	return sortedNames(names)
}

func sortedNames(names []string) []string {
	sort.Strings(names)
	return names
}

// Validate checks a spec before anything runs.
func (s Spec) Validate() error {
	switch {
	case (s.Image == "") == (s.Dir == ""):
		return errors.New("name exactly one of image and dir")
	case s.Port < 1 || s.Port > 65535:
		return fmt.Errorf("port %d is not a port", s.Port)
	case s.Image != "" && !imageRef.MatchString(s.Image):
		return fmt.Errorf("%q is not an image reference", s.Image)
	}
	return nil
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

	image := s.Image
	if s.Dir != "" {
		image = name
		bctx, cancel := context.WithTimeout(ctx, buildTimeout)
		out, err := runCmd(bctx, m.rt.Bin, "build", "--label", Label+"=1", "--label", OwnerLabel+"="+m.owner,
			"-t", image, s.Dir)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("the image did not build: %v\n%s", err, tail(string(out), 1500))
		}
	}
	args := []string{"run", "-d", "--name", name,
		"--label", Label + "=1", "--label", OwnerLabel + "=" + m.owner,
		"--security-opt", "no-new-privileges", "--pids-limit", "512", "--memory", "1g",
		// 127.0.0.1 and a port the runtime picks: never the whole network, never a fixed port that
		// something else on the machine may hold.
		"-p", "127.0.0.1::" + strconv.Itoa(s.Port), image}
	if out, err := runCmd(ctx, m.rt.Bin, args...); err != nil {
		m.removeImage(ctx, s, image)
		return nil, fmt.Errorf("the container did not start: %v\n%s", err, tail(string(out), 1500))
	}
	url, err := m.address(ctx, name, s.Port)
	if err == nil {
		err = m.waitReady(ctx, url)
	}
	if err != nil {
		logs, _ := runCmd(ctx, m.rt.Bin, "logs", "--tail", "40", name)
		m.remove(ctx, name, s.Dir != "")
		return nil, fmt.Errorf("%v\nlast log lines:\n%s", err, tail(string(logs), 1500))
	}
	b := &Box{Name: name, URL: url}
	m.mu.Lock()
	m.boxes[name] = b
	m.mu.Unlock()
	return b, nil
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
func (m *Manager) waitReady(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	for {
		if resp, err := getURL(ctx, url); err == nil {
			_ = resp.Body.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the service did not answer at %s in %s", url, readyTimeout)
		case <-time.After(pollEvery):
		}
	}
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := getURL(ctx, u)
	if err != nil {
		return "", fmt.Errorf("GET %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	text := string(body)
	more := ""
	if len(body) > bodyLimit {
		text, more = string(body[:bodyLimit]), "\n[body cut at 4096 bytes]"
	}
	return fmt.Sprintf("HTTP %s\nContent-Type: %s\n\n%s%s", resp.Status, resp.Header.Get("Content-Type"), text, more), nil
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

// Shot opens path on a box in a headless browser and saves a PNG to out.
func (m *Manager) Shot(ctx context.Context, name, path, out string, width, height int) error {
	b, err := m.box(name)
	if err != nil {
		return err
	}
	u, err := b.join(path)
	if err != nil {
		return err
	}
	bin, err := Browser()
	if err != nil {
		return err
	}
	if width < 200 || width > 3000 || height < 200 || height > 3000 {
		return fmt.Errorf("a %dx%d window is out of range (200 to 3000)", width, height)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
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
	m.remove(ctx, b.Name, true)
	m.mu.Lock()
	delete(m.boxes, b.Name)
	m.mu.Unlock()
	return nil
}

// DownAll removes every box of the run. It does not use the caller's context: it is what runs
// when that context is already cancelled.
func (m *Manager) DownAll() {
	m.mu.Lock()
	names := make([]string, 0, len(m.boxes))
	for n := range m.boxes {
		names = append(names, n)
	}
	m.boxes = map[string]*Box{}
	m.mu.Unlock()
	for _, n := range names {
		m.remove(context.Background(), n, true)
	}
}

// remove deletes a container and, when it was built here, its image. Failures are ignored: a
// container that is already gone is the goal.
func (m *Manager) remove(ctx context.Context, name string, image bool) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _ = runCmd(c, m.rt.Bin, "rm", "-f", name)
	if image {
		_, _ = runCmd(c, m.rt.Bin, "rmi", "-f", name)
	}
}

func (m *Manager) removeImage(ctx context.Context, s Spec, image string) {
	if s.Dir != "" {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, _ = runCmd(c, m.rt.Bin, "rmi", "-f", image)
	}
}

// Sweep removes the containers earlier processes left behind (a crash, a kill): everything
// carrying Label. It is called when the gateway starts, when no run of this process exists yet.
func Sweep(ctx context.Context) int {
	rt, err := Detect(ctx)
	if err != nil {
		return 0
	}
	out, err := runCmd(ctx, rt.Bin, "ps", "-a", "-q", "--filter", "label="+Label)
	if err != nil {
		return 0
	}
	n := 0
	for _, id := range strings.Fields(string(out)) {
		if _, err := runCmd(ctx, rt.Bin, "rm", "-f", id); err == nil {
			n++
		}
	}
	return n
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
