package clitest

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// clitest tests ITSELF with the contract it imposes on the two mocks. That is not
// ceremony: the helper re-execs os.Executable() to reach the real bootstrap, and the
// binary it finds here is this test binary, so the same subprocess trick applies. If it
// did not work for clitest, it would not work for the tools either.
//
// The alternative — leaving this package untested because "it is only test
// infrastructure" — is how a shared contract quietly stops matching the programs that
// depend on it.

const (
	selfMarker    = "CLITEST_IS_THE_TEST_BINARY"
	selfForceFail = "CLITEST_FORCE_LISTEN_FAILURE"
	selfPort      = 45731
)

// selfListen is the bootstrap hook, swapped by the contract's own tests.
var selfListen = func(srv *http.Server) error { return srv.ListenAndServe() }

// selfTool is the tool under test: clitest itself.
func selfTool() Tool {
	return Tool{
		Name:           "clitest",
		Marker:         selfMarker,
		Port:           selfPort,
		ForceFailure:   selfForceFail,
		Handler:        selfHandler(),
		ListenAndServe: &selfListen,
		MainBody:       selfMain,
	}
}

// selfHandler is the smallest server that satisfies the contract: /healthz answers "ok".
func selfHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	return mux
}

// selfMain mirrors the mocks' bootstrap: report a listen failure and ask for exit 1,
// otherwise serve until killed.
func selfMain(port int, host string, exit func(int)) {
	p := port
	if p == 0 {
		p = selfPort
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, p),
		Handler:           selfHandler(),
		ReadHeaderTimeout: time.Second,
	}
	// The forced failure is a variable, not a privileged port: port 1 is bindable when
	// the suite runs as root, so relying on it would make the test pass locally and fail
	// in a container, or the other way round.
	if os.Getenv(selfForceFail) == "1" {
		fmt.Fprintln(os.Stderr, "error: the listen failure was forced")
		exit(1)
		return
	}
	if err := selfListen(srv); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exit(1)
	}
}

// TestMain serves as the program when the contract starts this binary as a subprocess.
// It deliberately does NOT call m.Run() in that case: the flags the contract passes
// (-port, -host) belong to the mock programs, and letting the testing package parse them
// would reject them before the server ever started.
func TestMain(m *testing.M) {
	if os.Getenv(selfMarker) != "1" {
		os.Exit(m.Run())
	}
	selfMain(0, "127.0.0.1", os.Exit)
}

// TestTheSharedContract runs every rule the mocks are held to, on clitest.
func TestTheSharedContract(t *testing.T) {
	Run(t, selfTool())
}
