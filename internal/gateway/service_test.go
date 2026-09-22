package gateway

import (
	"context"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/tui"
)

// The whole design of this package rests on the assertions below, so they are compile-time
// ones rather than a comment.
//
// internal/gateway declares Service STRUCTURALLY: it lists the methods a front end needs, and
// *tui.AppRunner already has all of them, so the production runner satisfies the interface
// with NEITHER package importing the other. That is what keeps the text interface - the most
// heavily tested code in the repository - out of this change entirely.
//
// If somebody makes the gateway depend on the interface being declared in the tui package, or
// adds a method to Service that the runner does not have, this file stops compiling and the
// mistake is caught here instead of at the wiring in internal/app.

var _ Service = (*tui.AppRunner)(nil)

// The two halves the TUI already asks for by type assertion (see internal/tui/confirm.go and
// the askSource interface in internal/tui/tui.go). The server installs the approver; the
// client reads the questions the last turn left behind. Both are on the production runner
// today, and asserting it here means a future change cannot quietly drop one of them.
var (
	_ interface {
		SetApprover(agent.Approver)
	} = (*tui.AppRunner)(nil)

	_ interface {
		TakePendingQuestions() ([]agent.AskItem, string)
	} = (*tui.AppRunner)(nil)
)

// The runner also carries RunConfig, which Service deliberately does NOT declare: onboarding
// is a terminal wizard that reads lines from the terminal it was launched from (see
// internal/onboard.Run, which takes its input as a reader), so it is a capability of a FRONT
// END rather than of an agent. This asserts the capability really is there to be handed over,
// which is exactly what the embedded client does.
var _ interface {
	RunConfig(ctx context.Context) error
} = (*tui.AppRunner)(nil)

// TestTheContractHolds documents where the real check lives. The assertions above are the
// test: they fail the BUILD, not the run, which is the only way an interface cannot drift
// quietly between two packages.
func TestTheContractHolds(t *testing.T) {
	t.Log("the compile-time assertions above are the test; see the package comment in service.go")
}
