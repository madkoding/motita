package localmodel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as a fake llama-server: run with MOTITA_FAKE_LLAMA=1 it answers /health on the
// port it is given until it is stopped.
func TestMain(m *testing.M) {
	if os.Getenv("MOTITA_FAKE_LLAMA") == "1" {
		fakeLlama(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

func fakeLlama(args []string) {
	port := ""
	for i, a := range args {
		if a == "--port" && i+1 < len(args) {
			port = args[i+1]
		}
	}
	if os.Getenv("MOTITA_FAKE_LLAMA_FAIL") == "1" {
		_, _ = os.Stderr.WriteString("out of memory\n")
		os.Exit(3)
	}
	status := http.StatusOK
	if os.Getenv("MOTITA_FAKE_LLAMA_SLOW") == "1" {
		// Never loads, and does not stop when asked: it has to be killed.
		signal.Ignore(syscall.SIGTERM)
		status = http.StatusServiceUnavailable
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		os.Exit(2)
	}
	_ = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
}

// readyManager is a manager with a GPU, a model file of the right size and the test binary as
// its llama-server.
func readyManager(t *testing.T, onReady func(Ready) error) *Manager {
	t.Helper()
	t.Setenv("MOTITA_FAKE_LLAMA", "1")
	old := lookPath
	lookPath = func(string) (string, error) { return os.Args[0], nil }
	t.Cleanup(func() { lookPath = old })
	m := New(t.TempDir(), onReady)
	m.probe = fakeProbe("linux", "amd64", map[string]string{"nvidia-smi": "GPU, 8192, 8000"}, nil)
	if err := os.MkdirAll(filepath.Dir(m.modelPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(m.modelPath())
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(ModelSize) // sparse: no 3 GB written
	_ = f.Close()
	return m
}

func TestStartServesThenStops(t *testing.T) {
	ready := make(chan Ready, 1)
	m := readyManager(t, func(r Ready) error {
		ready <- r
		return nil
	})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("a second start: %v", err)
	}
	var r Ready
	select {
	case r = <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("the server never became ready")
	}
	local := strings.HasPrefix(r.BaseURL, "http://127.0.0.1:") && strings.HasPrefix(r.APIKey, "motita-local-")
	if !local || r.Context != 16384 {
		t.Fatalf("ready = %+v", r)
	}
	st := m.Status(context.Background())
	if !st.Server.Running || st.Server.GPULayers != 99 || st.RuntimeSource != "path" {
		t.Fatalf("server = %+v", st.Server)
	}
	m.Stop()
	if st := m.Status(context.Background()); st.Server.Running {
		t.Fatalf("after stop: %+v", st.Server)
	}
	if err := m.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.dir); !os.IsNotExist(err) {
		t.Error("delete removes the folder")
	}
}

func TestAnAddressThatCannotBeSavedIsShown(t *testing.T) {
	m := readyManager(t, func(Ready) error { return errors.New("disk full") })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if st := m.Status(context.Background()); st.Server.Running && strings.Contains(st.Server.Error, "disk full") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the failed save was never shown")
}

func TestACrashedServerShowsItsLastWords(t *testing.T) {
	t.Setenv("MOTITA_FAKE_LLAMA_FAIL", "1")
	m := readyManager(t, nil)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := m.Status(context.Background()); st.Server.Error != "" {
			if !strings.Contains(st.Server.Error, "out of memory") || st.Server.Running {
				t.Fatalf("server = %+v", st.Server)
			}
			m.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the crash was never reported")
}
