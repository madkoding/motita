// Package clitest holds the start-up contract the two mock tools share.
//
// mockapi and mockllm are separate programs with the same bootstrap: parse the
// flags, answer a health endpoint, and report a listen failure instead of
// pretending to have started. The tests that described that contract were copies of
// each other, which is how one of the two drifts and the suite stops meaning what
// it says. The contract lives here once; each tool supplies only what is genuinely
// its own — its marker, its port and the variable that forces a failure.
package clitest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Tool describes one mock server's bootstrap.
type Tool struct {
	// Name is the program, for the failure messages.
	Name string
	// Marker is the environment variable that makes the test binary run main().
	Marker string
	// Port is a fixed, unusual port so the test cannot clash with a running mock.
	Port int
	// ForceFailure is the variable the program reads to simulate a bind failure.
	ForceFailure string
	// Handler is the production handler, so the wiring is what gets exercised.
	Handler http.Handler
	// ListenAndServe is the production bootstrap hook. It is a pointer to the
	// package variable so the failure path can be driven in-process instead of by
	// taking a real port away from the machine.
	ListenAndServe *func(*http.Server) error
	// MainBody runs the bootstrap with an injected exit, which is what makes the
	// failure path observable without a subprocess.
	MainBody func(port int, host string, exit func(int))
}

// Run executes the whole shared contract.
func Run(t *testing.T, tool Tool) {
	t.Helper()
	t.Run("answers_healthz", func(t *testing.T) { testAnswersHealthz(t, tool) })
	t.Run("reports_a_listen_failure", func(t *testing.T) { testReportsAListenFailure(t, tool) })
	t.Run("bootstrap_reports_a_failure", func(t *testing.T) { testBootstrapReportsAFailure(t, tool) })
	t.Run("bootstrap_starts_and_stops", func(t *testing.T) { testBootstrapStartsAndStops(t, tool) })
	t.Run("real_server_binds_and_serves", func(t *testing.T) { testRealServerBindsAndServes(t, tool) })
}

// testAnswersHealthz starts the program for real and checks it answers. It is the
// only way to cover the flag parsing and the server bootstrap.
func testAnswersHealthz(t *testing.T, tool Tool) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	port := strconv.Itoa(tool.Port)
	cmd := exec.Command(binary, "-port", port, "-host", "127.0.0.1")
	cmd.Env = append(os.Environ(), tool.Marker+"=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	url := "http://127.0.0.1:" + port + "/healthz"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return // it is up and answering
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not answer on %s (stderr: %s)", tool.Name, url, stderr.String())
}

// testReportsAListenFailure: when the port cannot be bound the program must report
// it and exit non-zero instead of pretending it started.
//
// The failure is forced through the tool's own variable rather than by taking a
// privileged port: port 1 is bindable when the suite runs as root, so relying on it
// would make the test pass locally and fail in a container, or the other way round.
func testReportsAListenFailure(t *testing.T, tool Tool) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}
	cmd := exec.Command(binary, "-port", "1")
	cmd.Env = append(os.Environ(), tool.Marker+"=1", tool.ForceFailure+"=1")
	var errs strings.Builder
	cmd.Stderr = &errs

	if err := cmd.Run(); err == nil {
		t.Fatal("a listen failure must exit non-zero")
	}
	if !strings.Contains(errs.String(), "error:") {
		t.Errorf("the failure must be reported on stderr: %q", errs.String())
	}
}

// testBootstrapReportsAFailure: the bootstrap must ask for a non-zero exit when the
// server cannot start, instead of returning as if all were well.
func testBootstrapReportsAFailure(t *testing.T, tool Tool) {
	t.Helper()
	swap(t, tool, func(*http.Server) error { return errors.New("port already in use") })

	var exited []int
	tool.MainBody(tool.Port, "127.0.0.1", func(code int) { exited = append(exited, code) })

	if len(exited) != 1 || exited[0] != 1 {
		t.Errorf("the bootstrap must ask for exit 1: %v", exited)
	}
}

// testBootstrapStartsAndStops: on success the bootstrap returns without asking for
// an exit, and the server must be wired with a read-header timeout — without one, a
// client that never finishes its request line holds the connection open.
func testBootstrapStartsAndStops(t *testing.T, tool Tool) {
	t.Helper()
	swap(t, tool, func(srv *http.Server) error {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Body.String() != "ok" {
			t.Errorf("the handler is not wired: %q", rec.Body.String())
		}
		if srv.ReadHeaderTimeout <= 0 {
			t.Error("a read-header timeout must be set")
		}
		return nil
	})

	var exited []int
	tool.MainBody(tool.Port, "127.0.0.1", func(code int) { exited = append(exited, code) })

	if len(exited) != 0 {
		t.Errorf("a successful start must not exit: %v", exited)
	}
}

// testRealServerBindsAndServes drives the production listen helper, which the tests
// above replace: without this the real one would never run under a test.
func testRealServerBindsAndServes(t *testing.T, tool Tool) {
	t.Helper()
	addr := "127.0.0.1:" + strconv.Itoa(tool.Port+1)
	srv := &http.Server{
		Addr:              addr,
		Handler:           tool.Handler,
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = (*tool.ListenAndServe)(srv) }()
	defer srv.Close()

	url := "http://" + addr + "/healthz"
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("the real server of %s did not answer on %s", tool.Name, url)
}

// swap replaces the listen hook for the duration of the test. Replacing it is what
// makes the bootstrap's two exits reachable: the real hook blocks until the server
// is stopped, which a test cannot observe.
func swap(t *testing.T, tool Tool, fn func(*http.Server) error) {
	t.Helper()
	original := *tool.ListenAndServe
	t.Cleanup(func() { *tool.ListenAndServe = original })
	*tool.ListenAndServe = fn
}
