// Package gateway exposes one agent to many front ends over HTTP.
//
// It exists because an agent is not a terminal: the same conversation, the same procedure
// library and the same reward ledger have to be reachable from a text interface, a web page
// and a phone. The package owns the TRANSPORT only. The agent, its policy, its sandbox and its
// configuration stay where they are, and this package reaches them through the Service
// interface below.
//
// Everything here is the standard library. That is not a preference: the released binary has a
// 10 MB ceiling with under 2 MB of room beneath it, so a dependency is a cost this feature
// cannot pay.
package gateway

import (
	"context"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/session"
)

// Service is everything a front end may ask of the agent.
//
// It is declared here, structurally, and internal/tui is deliberately NOT imported: the
// production runner (*tui.AppRunner) already has every method below, so it satisfies Service
// with neither package knowing about the other. The compile-time assertions that keep that
// true live in service_test.go.
//
// RunConfig is deliberately ABSENT. Onboarding is a terminal wizard that reads lines from the
// terminal it was launched from, so it is a capability of a FRONT END and not of an agent. In
// the embedded case the wizard is handed to the client directly; a remote client refuses it
// and says why.
//
// RunPlan and RunTask both take a progress callback and are both streamed by the server. The
// callback's lines are forwarded VERBATIM: a plan run emits prefixes the interface parses, so
// a transport that reformatted them would break the view that reads them.
type Service interface {
	// RunPlan runs the read-only planner with the given user prompt.
	RunPlan(ctx context.Context, prompt string, progress func(string, ...any)) (string, error)
	// RunTask runs the agent in task mode with the given task description.
	RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error)
	// RunModels renders the catalogue the provider publishes.
	RunModels(ctx context.Context) (string, error)
	// ConversationReport describes the session in the interface's own words.
	ConversationReport() string
	// ConversationSummary is the same figures in the raw form a status bar draws.
	ConversationSummary() session.Snapshot
	// ResetConversation starts a new session.
	ResetConversation()
	// Config returns the current configuration.
	//
	// It carries a secret. It is on this interface because a front end draws the provider,
	// the model and whether a key is present, but the value MUST NOT be serialised: the
	// server reduces it with viewOf before it goes anywhere near a network. See view.go.
	Config() config.Config
	// SetReasoning changes the in-memory reasoning level.
	SetReasoning(level string)
	// RecordVerdict applies the user's verdict on the last turn to the skills it read, and
	// returns a human-readable report.
	RecordVerdict(good bool, note string) string
	// RewardReport renders what the library has learned, worst first.
	RewardReport() string
	// TakePendingQuestions returns the questions the last turn asked and CLEARS them.
	//
	// Taking clears because the window is opened once per question set: without that, a
	// repaint would reopen a window the user had already closed.
	TakePendingQuestions() ([]agent.AskItem, string)
	// Transcript returns the conversation so far, as the turns a front end draws.
	//
	// It is the answer to "I just connected, what has been said?": a client cannot be expected to
	// have been there for every turn, and the figures alone (ConversationReport, ConversationSummary)
	// are numbers, not a conversation. Without this a client that joins an existing conversation has
	// nothing to put on the screen.
	//
	// It is also how a client recovers from a dropped connection. Re-reading the turns is much less
	// code than reconciling a log of streamed events, and it cannot desynchronise: the turns are the
	// conversation, so there is no second version of it to disagree with.
	//
	// It deliberately does NOT expose Plan mode's raw message history. That history is the model's
	// own view - system prompt, tool calls, tool results - and a front end that rendered it would be
	// showing the user a wire format. What belongs on a screen is the turns below.
	Transcript() []agent.DialogueTurn
	// SetApprover installs the channel a consequential command is confirmed through.
	//
	// The SERVER installs its own before every run. It has to: a command that needs approval
	// and has nobody to ask is REFUSED rather than run unreviewed, so the transport is
	// exactly the thing that has to become the person asking.
	SetApprover(fn agent.Approver)
}
