// Package mockserve is the bootstrap the simulated servers (tools/mockapi and
// tools/mockllm) share: the HTTP server they listen with, the startup line and the
// exit on a failed bind. Each mock keeps only what it plays.
package mockserve

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Listen returns the function a mock blocks on. It is kept in a variable by each
// mock so a test can replace it (the real one only returns when the server stops),
// and failEnv set to "1" makes the bind fail on purpose, for the failure path.
func Listen(failEnv string) func(*http.Server) error {
	return func(srv *http.Server) error {
		if os.Getenv(failEnv) == "1" {
			return errors.New("forced listen failure for the test")
		}
		return srv.ListenAndServe()
	}
}

// Run serves h on host:port under the given name, announcing the base URL on
// stderr, and calls exit(1) when listen returns an error.
func Run(name, host string, port int, h http.Handler, listen func(*http.Server) error, exit func(int)) {
	run(os.Stderr, name, host, port, h, listen, exit)
}

func run(out io.Writer, name, host string, port int, h http.Handler, listen func(*http.Server) error, exit func(int)) {
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(out, "%s listening on http://%s:%d/v1\n", name, host, port)
	if err := listen(srv); err != nil {
		fmt.Fprintln(out, "error:", err)
		exit(1)
	}
}
