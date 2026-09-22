package main

import (
	"net/http"
	"os"
	"testing"

	"github.com/madkoding/starlight/tools/clitest"
)

// TestMain lets the test binary act as the tool itself when the marker is set:
// main() parses the flags and blocks on ListenAndServe, which only makes sense in a
// process of its own.
func TestMain(m *testing.M) {
	if os.Getenv("MOCKAPI_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// TestTheStartupContract is the half of the mock's behaviour that has nothing to do
// with the script it plays: flags, health, and a bind failure that is reported. The
// contract itself lives in tools/clitest, shared with mockllm.
func TestTheStartupContract(t *testing.T) {
	clitest.Run(t, clitest.Tool{
		Name:           "mockapi",
		Marker:         "MOCKAPI_TEST_MAIN",
		Port:           18099,
		ForceFailure:   "MOCKAPI_FORCE_LISTEN_FAILURE",
		Handler:        http.HandlerFunc(handle),
		ListenAndServe: &listenAndServe,
		MainBody: func(port int, host string, exit func(int)) {
			mainBody(&port, &host, exit)
		},
	})
}
