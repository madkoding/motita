// Package localmodel runs Motita's own local model: Qwen3-8B (ISTA-DASLab's GSQ quantisation,
// Q2_K, about 3 GB) served by llama.cpp's llama-server on this machine, as an OpenAI-compatible
// provider. Nothing happens on its own: the user installs it (runtime and model are downloaded,
// resumable and verified), starts it, stops it and deletes it from the settings, and it is only
// offered when the GPU has at least 4 GB of memory.
package localmodel

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The one model this mode runs. Size and hash are Hugging Face's own for the file (its LFS
// object), so a download is checked against what was published, not against what arrived.
const (
	ModelID     = "qwen3-8b-gsq-q2_k"
	ModelLabel  = "Qwen3-8B GSQ (Q2_K, local)"
	ModelRepo   = "ISTA-DASLab/Qwen3-8B-GGUF-GSQ"
	ModelFile   = "qwen3-8b-Q2_K.gguf"
	ModelSize   = 3281732960
	ModelSHA256 = "2bc740416074ee7a7c26189d82d1b1c75274287ec70877c945a0548f2e561858"
)

// modelURL is where the file is downloaded from, and modelSize and modelSHA what it must be;
// variables so a test can serve a small one.
var (
	modelURL        = "https://huggingface.co/" + ModelRepo + "/resolve/main/" + ModelFile
	modelSize int64 = ModelSize
	modelSHA        = ModelSHA256
)

// MinVRAMMB is the least GPU memory the mode is offered with: "4 GB". A 4 GB card reports a
// little under 4096 MiB on some drivers, hence the margin.
const MinVRAMMB = 3800

// Why the mode is not offered.
const (
	ReasonNoGPU   = "no_gpu"
	ReasonLowVRAM = "low_vram"
)

// PreferredPort is where the server listens when it is free; another free port otherwise.
const PreferredPort = 8471

// Qwen3-8B's shape, to size the context: 36 layers, 8 KV heads of 128, the cache in q8_0.
const (
	modelLayers  = 36
	kvBytesToken = modelLayers * 8 * 128 * 2 * 17 / 16 // K and V, q8_0 (8.5 bits)
	overheadMB   = 450                                 // compute buffers and the driver's own
)

// Status is what the settings show.
type Status struct {
	ModelID   string `json:"model_id"`
	ModelName string `json:"model_name"`
	Repo      string `json:"repo"`
	File      string `json:"file"`
	Size      int64  `json:"size"`
	Dir       string `json:"dir"`

	GPU       *GPU   `json:"gpu,omitempty"`
	MinVRAMMB int    `json:"min_vram_mb"`
	Eligible  bool   `json:"eligible"`
	Reason    string `json:"reason,omitempty"`

	ModelPresent   bool   `json:"model_present"`
	RuntimePresent bool   `json:"runtime_present"`
	RuntimeSource  string `json:"runtime_source,omitempty"` // "path" or "installed"
	RuntimeTag     string `json:"runtime_tag,omitempty"`

	Task   Task   `json:"task"`
	Server Server `json:"server"`
}

// Task is the install in progress or the last one's failure.
type Task struct {
	Running bool   `json:"running"`
	Phase   string `json:"phase,omitempty"` // "runtime" or "model"
	Done    int64  `json:"done,omitempty"`
	Total   int64  `json:"total,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Server is the llama-server process.
type Server struct {
	Running   bool   `json:"running"`
	Starting  bool   `json:"starting,omitempty"`
	Port      int    `json:"port,omitempty"`
	Context   int    `json:"context,omitempty"`
	GPULayers int    `json:"gpu_layers,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Ready is what the provider needs once the server answers.
type Ready struct {
	BaseURL string
	APIKey  string
	Context int
}

// Manager owns the folder, the install and the server process.
type Manager struct {
	dir     string
	probe   probe
	client  *http.Client
	onReady func(Ready) error

	mu         sync.Mutex
	task       Task
	cancel     context.CancelFunc
	installing chan struct{} // closed when the install in progress ends
	srv        Server
	cmd        *exec.Cmd
	done       chan struct{}
	log        *tail
	gpu        *GPU
	gpuAt      time.Time
}

// gpuTTL is how long a GPU reading is reused: nvidia-smi takes a moment.
const gpuTTL = 30 * time.Second

// healthTimeout bounds how long the server may take to load the model, healthPoll is how often it
// is asked and stopGrace how long it has to stop before it is killed; variables so a test can
// shorten them.
var (
	healthTimeout = 4 * time.Minute
	healthPoll    = 500 * time.Millisecond
	stopGrace     = 10 * time.Second
)

// New is a manager over dir (the motita home's "local" folder). onReady is told each time the
// server starts answering; its error is shown beside the running server.
func New(dir string, onReady func(Ready) error) *Manager {
	return &Manager{dir: dir, probe: systemProbe(), client: &http.Client{}, onReady: onReady}
}

func (m *Manager) modelPath() string   { return filepath.Join(m.dir, "models", ModelFile) }
func (m *Manager) runtimeDir() string  { return filepath.Join(m.dir, "runtime") }
func (m *Manager) currentFile() string { return filepath.Join(m.runtimeDir(), "current") }

// detectGPU reads the GPU, reusing a recent reading.
func (m *Manager) detectGPU(ctx context.Context) *GPU {
	m.mu.Lock()
	if m.gpuAt.After(time.Now().Add(-gpuTTL)) {
		g := m.gpu
		m.mu.Unlock()
		return g
	}
	m.mu.Unlock()
	g := m.probe.detect(ctx)
	m.mu.Lock()
	m.gpu, m.gpuAt = g, time.Now()
	m.mu.Unlock()
	return g
}

// eligibility says whether a GPU can run the model, and why not.
func eligibility(g *GPU) (bool, string) {
	switch {
	case g == nil:
		return false, ReasonNoGPU
	case g.TotalMB < MinVRAMMB:
		return false, ReasonLowVRAM
	default:
		return true, ""
	}
}

// Status reads the folder, the GPU and the process.
func (m *Manager) Status(ctx context.Context) Status {
	g := m.detectGPU(ctx)
	ok, why := eligibility(g)
	st := Status{ModelID: ModelID, ModelName: ModelLabel, Repo: ModelRepo, File: ModelFile, Size: ModelSize,
		Dir: m.dir, GPU: g, MinVRAMMB: MinVRAMMB, Eligible: ok, Reason: why}
	if fi, err := os.Stat(m.modelPath()); err == nil && fi.Size() == modelSize {
		st.ModelPresent = true
	}
	if path, source, tag := m.serverBinary(); path != "" {
		st.RuntimePresent, st.RuntimeSource, st.RuntimeTag = true, source, tag
	}
	m.mu.Lock()
	st.Task, st.Server = m.task, m.srv
	m.mu.Unlock()
	return st
}

// serverBinary is the llama-server to run: the installed one, else one on PATH.
func (m *Manager) serverBinary() (path, source, tag string) {
	if b, err := os.ReadFile(m.currentFile()); err == nil {
		tag = strings.TrimSpace(string(b))
		if safeName(tag) {
			if p := findServer(filepath.Join(m.runtimeDir(), tag), m.probe.goos); p != "" {
				return p, "installed", tag
			}
		}
	}
	if p, err := lookPath(serverName(m.probe.goos)); err == nil {
		return p, "path", ""
	}
	return "", "", ""
}

// ErrBusy is an install or a server already under way.
var ErrBusy = errors.New("the local model is busy: wait for the current step to finish")

// ErrNotEligible is a start on a machine without the GPU memory.
var ErrNotEligible = errors.New("this computer does not have the 4 GB of GPU memory the local model needs")

// Install downloads what is missing - the runtime, then the model - in the background.
func (m *Manager) Install() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.task.Running {
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.installing = cancel, make(chan struct{})
	m.task = Task{Running: true}
	go m.install(ctx, m.installing)
	return nil
}

// Cancel stops an install; what was downloaded is kept for the next one.
func (m *Manager) Cancel() {
	m.mu.Lock()
	cancel, installing := m.cancel, m.installing
	m.mu.Unlock()
	if installing != nil {
		cancel()
		<-installing
	}
}

func (m *Manager) install(ctx context.Context, installing chan struct{}) {
	err := m.installRuntime(ctx)
	if err == nil {
		err = m.installModel(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	defer close(installing)
	m.cancel, m.installing = nil, nil
	m.task = Task{}
	if errors.Is(err, context.Canceled) {
		m.task.Error = "cancelled: what was downloaded is kept, install again to resume"
	} else if err != nil {
		m.task.Error = err.Error()
	}
}

func (m *Manager) setPhase(phase string, total int64) {
	m.mu.Lock()
	m.task.Phase, m.task.Done, m.task.Total = phase, 0, total
	m.mu.Unlock()
}

func (m *Manager) progress(done, total int64) {
	m.mu.Lock()
	m.task.Done, m.task.Total = done, total
	m.mu.Unlock()
}

func (m *Manager) installRuntime(ctx context.Context) error {
	if p, _, _ := m.serverBinary(); p != "" {
		return nil
	}
	m.setPhase("runtime", 0)
	r, err := fetchRelease(ctx, m.client, strings.TrimSpace(os.Getenv(TagVariable)))
	if err != nil {
		return err
	}
	a, err := pickAsset(r, m.probe.goos, m.probe.goarch)
	if err != nil {
		return err
	}
	sum := digestOf(a)
	if sum == "" {
		return fmt.Errorf("GitHub published no SHA-256 for %s, so it is not installed", a.Name)
	}
	m.setPhase("runtime", a.Size)
	archive := filepath.Join(m.runtimeDir(), filepath.Base(a.Name))
	d := download{url: a.URL, dest: archive, size: a.Size, sha256: sum, client: m.client}
	if err := d.run(ctx, m.progress); err != nil {
		return err
	}
	// The folder is only used once "current" names it, so a half-unpacked one is never run.
	dest := filepath.Join(m.runtimeDir(), r.Tag)
	_ = os.RemoveAll(dest)
	err = unpack(archive, dest)
	_ = os.Remove(archive)
	if err != nil {
		return fmt.Errorf("unpacking %s: %w", a.Name, err)
	}
	if findServer(dest, m.probe.goos) == "" {
		return fmt.Errorf("%s holds no %s", a.Name, serverName(m.probe.goos))
	}
	return os.WriteFile(m.currentFile(), []byte(r.Tag+"\n"), 0o644)
}

func (m *Manager) installModel(ctx context.Context) error {
	if fi, err := os.Stat(m.modelPath()); err == nil && fi.Size() == modelSize {
		return nil
	}
	m.setPhase("model", modelSize)
	d := download{url: modelURL, dest: m.modelPath(), size: modelSize, sha256: modelSHA, client: m.client}
	return d.run(ctx, m.progress)
}

// plan is the context and the layers on the GPU for a GPU: as much context as the card's size
// suggests, then as many layers as fit beside it in the free memory.
func plan(g GPU) (ctx, layers int) {
	ctx = 8192
	if g.TotalMB >= 8192 {
		ctx = 16384
	}
	if g.TotalMB >= 12288 {
		ctx = 32768
	}
	free := g.FreeMB
	if free <= 0 || free > g.TotalMB {
		free = g.TotalMB * 9 / 10
	}
	if g.Unified {
		free = g.TotalMB
	}
	modelMB := ModelSize >> 20
	kvMB := ctx * kvBytesToken >> 20
	if modelMB+kvMB+overheadMB <= free {
		return ctx, 99
	}
	perLayer := (modelMB + kvMB) / (modelLayers + 1)
	layers = (free - overheadMB) / perLayer
	return ctx, max(layers, 0)
}

// Start launches the server in the background; onReady is called once it answers.
func (m *Manager) Start(ctx context.Context) error {
	st := m.Status(ctx)
	switch {
	case !st.Eligible:
		return ErrNotEligible
	case !st.ModelPresent || !st.RuntimePresent:
		return errors.New("install the local model first")
	}
	bin, _, _ := m.serverBinary()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.task.Running || m.srv.Running || m.srv.Starting {
		return ErrBusy
	}
	// 0 when nothing is free: llama-server then fails, and says so.
	port := freePort(PreferredPort)
	key := "motita-local-" + rand.Text()
	nctx, layers := plan(*st.GPU)
	cmd := exec.Command(bin, serverArgs(m.modelPath(), port, key, nctx, layers)...)
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = serverEnv(os.Environ(), filepath.Dir(bin), m.probe.goos)
	m.log = &tail{}
	cmd.Stdout, cmd.Stderr = m.log, m.log
	setProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting llama-server: %w", err)
	}
	m.cmd, m.done = cmd, make(chan struct{})
	m.srv = Server{Starting: true, Port: port, Context: nctx, GPULayers: layers}
	go m.wait(cmd, m.done)
	go m.awaitHealth(port, key, nctx, m.done)
	return nil
}

// serverArgs are llama-server's flags: loopback only, a token, every layer that fits on the GPU,
// the cache in q8_0 with flash attention, and the chat template the model ships (--jinja).
func serverArgs(model string, port int, key string, nctx, layers int) []string {
	return []string{
		"-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"--api-key", key, "--alias", ModelID,
		"-c", strconv.Itoa(nctx), "-ngl", strconv.Itoa(layers),
		"-fa", "on", "-ctk", "q8_0", "-ctv", "q8_0", "--jinja",
	}
}

// serverEnv lets the server find the libraries unpacked beside it on Linux.
func serverEnv(env []string, dir, goos string) []string {
	if goos != "linux" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	lib := dir
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "LD_LIBRARY_PATH="); ok {
			if v != "" {
				lib = dir + string(os.PathListSeparator) + v
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LD_LIBRARY_PATH="+lib)
}

// listen opens a port to see that it is free; a variable so a test can find none.
var listen = net.Listen

// freePort is preferred when it is free, else any free port on loopback.
func freePort(preferred int) int {
	for _, p := range []int{preferred, 0} {
		if l, err := listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p))); err == nil {
			port := l.Addr().(*net.TCPAddr).Port
			_ = l.Close()
			return port
		}
	}
	return 0
}

// wait notes the process's end; an end nobody asked for is shown with the server's last words.
func (m *Manager) wait(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	close(done)
	stopped := m.srv.Error == "stopped"
	m.cmd = nil
	m.srv = Server{}
	if !stopped {
		m.srv.Error = exitText(err, m.log.String())
	}
}

func exitText(err error, log string) string {
	msg := "llama-server stopped"
	if err != nil {
		msg += ": " + err.Error()
	}
	if last := lastLines(log, 6); last != "" {
		msg += "\n" + last
	}
	return msg
}

// awaitHealth polls /health until the model is loaded, then hands the provider its address.
func (m *Manager) awaitHealth(port int, key string, nctx int, done chan struct{}) {
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(healthTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return
		case <-time.After(healthPoll):
		}
		if healthy(base) {
			m.mu.Lock()
			m.srv.Starting, m.srv.Running = false, true
			m.mu.Unlock()
			if m.onReady == nil {
				return
			}
			if err := m.onReady(Ready{BaseURL: base + "/v1", APIKey: key, Context: nctx}); err != nil {
				m.mu.Lock()
				m.srv.Error = "the server runs, but its address could not be saved: " + err.Error()
				m.mu.Unlock()
			}
			return
		}
	}
	m.stop("the model did not load within " + healthTimeout.String())
}

// healthClient asks /health; a server still loading may be slow to answer.
var healthClient = &http.Client{Timeout: 2 * time.Second}

func healthy(base string) bool {
	resp, err := healthClient.Get(base + "/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Stop ends the server and waits for it.
func (m *Manager) Stop() { m.stop("stopped") }

func (m *Manager) stop(why string) {
	m.mu.Lock()
	cmd, done := m.cmd, m.done
	if cmd == nil {
		m.mu.Unlock()
		return
	}
	m.srv.Error = why
	m.mu.Unlock()
	terminate(cmd)
	select {
	case <-done:
	case <-time.After(stopGrace):
		_ = cmd.Process.Kill()
		<-done
	}
	m.mu.Lock()
	if why != "stopped" {
		m.srv.Error = why
	}
	m.mu.Unlock()
}

// Delete stops everything and removes the runtime and the model. A llama-server on PATH is the
// user's and is left alone.
func (m *Manager) Delete() error {
	m.Cancel()
	m.Stop()
	m.mu.Lock()
	m.task, m.srv = Task{}, Server{}
	m.mu.Unlock()
	return os.RemoveAll(m.dir)
}

// Close stops the server, for Motita's own shutdown.
func (m *Manager) Close() {
	m.Cancel()
	m.Stop()
}

// tail keeps the last few KB a process wrote.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

const tailMax = 8 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailMax {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-tailMax:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
