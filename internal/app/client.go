package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/gateway"
	"github.com/madkoding/starlight/internal/tui"
)

// newClient builds the gateway client a front end speaks through, and is the one place the
// construction happens: the embedded interface and the remote one differ in WHICH address and
// WHICH session they are given, never in how they are built.
func (op Options) newClient(baseURL, token, session string) tui.Runner {
	if op.NewClient != nil {
		return op.NewClient(baseURL, token, session)
	}
	return gateway.NewClientForSession(baseURL, token, session)
}

// runClient runs the interface as a REMOTE of a gateway somebody else is running.
//
// It is the mode that makes the two halves of this program separable. The gateway talks to the
// model and runs commands; a client draws a conversation and sends keystrokes' worth of intent. A
// process in this mode builds no sandbox, opens no procedure library and creates no reasoning
// engine, so it can run on a machine that could never be the agent - a laptop, a phone, a tablet -
// and the machine running the commands is the only one that needs any of it.
//
// It does NOT start a gateway, not even a private one. That is the difference from the embedded
// interface: here there is no agent in this process at all, and pretending otherwise by starting a
// local one would quietly create a SECOND conversation that the user is not looking at.
func (op Options) runClient(_ context.Context, fl flags, cfg config.Config) int {
	if op.RunTUI != nil {
		// The test seam still wins: a test that wants to observe the interface must not need a
		// gateway listening on a port.
		return op.RunTUI(context.Background(), cfg, nil, nil, nil)
	}

	token, err := gateway.ReadToken(cfg.Gateway.TokenFile)
	if err != nil {
		// The message the user will actually see when the token is not where they expected, and
		// it names the file, because that is what they have to fix.
		fmt.Fprintf(op.Err, "cannot connect to the gateway at %s: %v\n", fl.connect, err)
		return ConfigError
	}

	session := fl.session
	if session == "" {
		session = gateway.DefaultSession
	}

	client := op.newClient(fl.connect, token, session)

	// The session is CHECKED before the interface opens. A session id the gateway does not know
	// must be reported now, with the list of what it does know, rather than producing an interface
	// that fails on its first command with a 404 the user cannot act on.
	if err := checkSession(client, session); err != nil {
		fmt.Fprintf(op.Err, "%v\n", err)
		return ConfigError
	}

	ui := tui.New(client)
	ui.In = op.Stdin
	ui.Out = op.Out
	ui.Err = op.Err
	ui.NoColor = noColour(os.Getenv, op.Out)
	return ui.Run(context.Background())
}

// checkSession verifies that the conversation exists, and says what does exist when it does not.
//
// "There is no session X" on its own is a dead end: the user has no way to learn the ids that are
// real without a second tool. Listing them is one request and turns a dead end into a next step.
func checkSession(client tui.Runner, session string) error {
	lister, ok := client.(interface {
		ListSessions(context.Context) ([]gateway.SessionStatus, error)
	})
	if !ok {
		// A runner that cannot list sessions is not a remote client, and it is not this mode's
		// problem: the check is a courtesy and its absence is not a failure.
		return nil
	}
	sessions, err := lister.ListSessions(context.Background())
	if err != nil {
		// The gateway could not be asked. That is worth reporting rather than ignoring: the
		// client is about to be handed to a user who will type into it, and a gateway that
		// cannot answer a listing is a gateway that will not answer anything else either.
		return fmt.Errorf("the gateway did not answer when asked which sessions it holds: %w", err)
	}
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		if s.ID == session {
			return nil
		}
		ids = append(ids, s.ID)
	}
	if len(ids) == 0 {
		return fmt.Errorf("the gateway holds no session called %q, and it reports none at all", session)
	}
	return fmt.Errorf("the gateway holds no session called %q; it holds: %s",
		session, strings.Join(ids, ", "))
}
