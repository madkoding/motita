package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary act as the tool itself when the marker is set.
func TestMain(m *testing.M) {
	if os.Getenv("MOCKAPI_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// TestMainServesAndAnswersHealthz starts the tool for real and checks it answers,
// which covers the flag parsing and the server bootstrap.
func TestMainServesAndAnswersHealthz(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	port := "18099"
	cmd := exec.Command(binary, "-port", port, "-host", "127.0.0.1")
	cmd.Env = append(os.Environ(), "MOCKAPI_TEST_MAIN=1")
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
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the tool did not answer on %s (stderr: %s)", url, stderr.String())
}

// TestMainReportsAListenFailure: when the port cannot be bound the program must
// report it and exit non-zero instead of pretending it started.
func TestMainReportsAListenFailure(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}
	cmd := exec.Command(binary, "-port", "1")
	cmd.Env = append(os.Environ(), "MOCKAPI_TEST_MAIN=1", "MOCKAPI_FORCE_LISTEN_FAILURE=1")
	var errs strings.Builder
	cmd.Stderr = &errs

	err = cmd.Run()
	if err == nil {
		t.Fatal("a listen failure must exit non-zero")
	}
	if !strings.Contains(errs.String(), "error:") {
		t.Errorf("the failure must be reported on stderr: %q", errs.String())
	}
}

// TestMainBodyReportsAListenFailure: the bootstrap must report the failure and ask
// for a non-zero exit, instead of pretending the mock is up.
func TestMainBodyReportsAListenFailure(t *testing.T) {
	original := listenAndServe
	defer func() { listenAndServe = original }()
	listenAndServe = func(*http.Server) error { return errors.New("port already in use") }

	port, host := 8099, "127.0.0.1"
	var exited []int
	mainBody(&port, &host, func(code int) { exited = append(exited, code) })

	if len(exited) != 1 || exited[0] != 1 {
		t.Errorf("the bootstrap must ask for exit 1: %v", exited)
	}
}

// TestMainBodyStartsAndStops: on success the bootstrap returns without asking for
// an exit (the replaced server returns immediately).
func TestMainBodyStartsAndStops(t *testing.T) {
	original := listenAndServe
	defer func() { listenAndServe = original }()
	listenAndServe = func(srv *http.Server) error {
		// The handler must be wired: a request is served by the in-process server.
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Body.String() != "ok" {
			t.Errorf("the handler is not wired: %q", rec.Body.String())
		}
		if srv.ReadHeaderTimeout <= 0 {
			t.Error("a read-header timeout must be set")
		}
		return nil
	}

	port, host := 8099, "127.0.0.1"
	var exited []int
	mainBody(&port, &host, func(code int) { exited = append(exited, code) })

	if len(exited) != 0 {
		t.Errorf("a successful start must not exit: %v", exited)
	}
}
