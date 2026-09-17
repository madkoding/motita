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

// TestMainServesAndAnswersHealthz starts the tool for real, on a port of its own,
// and checks that it answers. It is the only way to cover the flag parsing and the
// server bootstrap.
func TestMainServesAndAnswersHealthz(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	// A fixed but unusual port keeps the test from clashing with a running mock.
	port := "18210"
	cmd := exec.Command(binary, "-port", port, "-host", "127.0.0.1")
	cmd.Env = append(os.Environ(), "MOCKLLM_TEST_MAIN=1")
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
				return // it is up and answering
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
	cmd.Env = append(os.Environ(), "MOCKLLM_TEST_MAIN=1", "MOCKLLM_FORCE_LISTEN_FAILURE=1")
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
// for a non-zero exit.
func TestMainBodyReportsAListenFailure(t *testing.T) {
	original := listenAndServe
	defer func() { listenAndServe = original }()
	listenAndServe = func(*http.Server) error { return errors.New("port already in use") }

	port, host := 8210, "127.0.0.1"
	var exited []int
	mainBody(&port, &host, func(code int) { exited = append(exited, code) })

	if len(exited) != 1 || exited[0] != 1 {
		t.Errorf("the bootstrap must ask for exit 1: %v", exited)
	}
}

// TestMainBodyStartsAndStops: on success the bootstrap returns without asking for
// an exit, and the handler must be wired with a read-header timeout.
func TestMainBodyStartsAndStops(t *testing.T) {
	original := listenAndServe
	defer func() { listenAndServe = original }()
	listenAndServe = func(srv *http.Server) error {
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

	port, host := 8210, "127.0.0.1"
	var exited []int
	mainBody(&port, &host, func(code int) { exited = append(exited, code) })

	if len(exited) != 0 {
		t.Errorf("a successful start must not exit: %v", exited)
	}
}

// TestListenAndServeReallyServes: the real implementation must bind and serve.
func TestListenAndServeReallyServes(t *testing.T) {
	srv := &http.Server{
		Addr:              "127.0.0.1:18209",
		Handler:           http.HandlerFunc(handle),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = listenAndServe(srv) }()
	defer srv.Close()

	url := "http://127.0.0.1:18209/healthz"
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
	t.Fatalf("the real server did not answer on %s", url)
}
