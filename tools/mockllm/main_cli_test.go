package main

import (
	"net/http"
	"testing"

	"github.com/madkoding/motita/tools/clitest"
)

// TestTheStartupContract is the half of the mock's behaviour that has nothing to do
// with the script it plays: flags, health, and a bind failure that is reported. The
// contract itself lives in tools/clitest, shared with mockapi.
func TestTheStartupContract(t *testing.T) {
	clitest.Run(t, clitest.Tool{
		Name:           "mockllm",
		Marker:         "MOCKLLM_TEST_MAIN",
		Port:           18210,
		ForceFailure:   "MOCKLLM_FORCE_LISTEN_FAILURE",
		Handler:        http.HandlerFunc(handle),
		ListenAndServe: &listenAndServe,
		MainBody: func(port int, host string, exit func(int)) {
			mainBody(&port, &host, exit)
		},
	})
}
