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

// sessionSwitcher adapts the gateway client to what the interface draws.
//
// internal/app is the only package that may know both sides: internal/gateway cannot import
// internal/tui (a cycle, and the whole reason the gateway declares its interfaces structurally),
// and internal/tui must not know about the gateway's wire types. Translating here is the same job
// this package already does everywhere else - it is what wires the layers together.
//
// SwitchSession and CurrentSession are PROMOTED from the embedded client rather than forwarded by
// hand: the client already names them the way the interface expects, and a second copy that only
// delegates would be a second thing to keep in step.
type sessionSwitcher struct {
	*gateway.Client
}

// ListSessions is the one method that needs translating, because the interface's shape carries
// Current - a fact about the INTERFACE, not about the gateway - and the gateway has no business
// computing it.
func (s sessionSwitcher) ListSessions(ctx context.Context) ([]tui.SessionInfo, error) {
	all, err := s.Client.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	current := s.Client.CurrentSession()
	out := make([]tui.SessionInfo, 0, len(all))
	for _, item := range all {
		out = append(out, tui.SessionInfo{
			ID:      item.ID,
			Running: item.Running,
			Current: item.ID == current,
			// LastUsed travels so the list can be ordered by it: "where was I?" is answered by the
			// most recent conversation, and a front end that dropped the time would have to guess.
			LastUsed: item.LastUsed,
		})
	}
	return out, nil
}

// Conversation translates the conversation the gateway holds into the shape the interface draws.
//
// The translation lives HERE and nowhere else: internal/gateway cannot import internal/tui (a cycle,
// and the whole reason the gateway declares its interfaces structurally), so the client returns a
// type of its own and this adapter is where the two meet. It is the same job ListSessions does, for
// the same reason.
func (s sessionSwitcher) Conversation(ctx context.Context) ([]tui.Turn, error) {
	turns, err := s.Client.Conversation(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tui.Turn, 0, len(turns))
	for _, item := range turns {
		out = append(out, tui.Turn{User: item.User, Agent: item.Agent})
	}
	return out, nil
}

// liveRun adapts the client's run report to what the interface draws.
//
// A translation rather than a forwarding: internal/gateway cannot import internal/tui, so the
// client returns a type of its own and this is where the two meet - the same job Conversation does.
func (s sessionSwitcher) LiveRun(ctx context.Context) (tui.LiveRun, bool, error) {
	info, running, err := s.Client.LiveRun(ctx)
	if err != nil {
		return tui.LiveRun{}, false, err
	}
	return tui.LiveRun{RunID: info.RunID, LastSeq: info.LastSeq}, running, nil
}

// CancelRun asks the gateway to stop the run in the current session, and reports whether there was
// one to stop.
//
// It is StopRun, PROMOTED under the name the interface's capability declares: the client already
// answers the question the interface asks, and a second method that only delegated would be a
// second thing to keep in step.
func (s sessionSwitcher) CancelRun(ctx context.Context) (bool, error) {
	return s.Client.StopRun(ctx)
}

// newClient builds the gateway client a front end speaks through, and is the one place the
// construction happens: the embedded interface and the remote one differ in WHICH address and
// WHICH session they are given, never in how they are built.
func (op Options) newClient(baseURL, token, session string) tui.Runner {
	if op.NewClient != nil {
		return op.NewClient(baseURL, token, session)
	}
	return sessionSwitcher{gateway.NewClientForSession(baseURL, token, session)}
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
func (op Options) runClient(ctx context.Context, fl flags, cfg config.Config) int {
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

	client := op.newClient(connectURL(fl.connect), token, session)

	// The session is CHECKED before the interface opens. A session id the gateway does not know
	// must be reported now, with the list of what it does know, rather than producing an interface
	// that fails on its first command with a 404 the user cannot act on.
	//
	// The check STAYS, and it is not made redundant by the entry below: it is what produces the
	// message a mistyped id deserves - "the gateway holds no session called X; it holds: ..." -
	// before an interface is handed to a user. Entering the session is what then MAKES THE FLAG
	// MEAN SOMETHING, and the two are different jobs.
	if err := checkSession(client, session); err != nil {
		fmt.Fprintf(op.Err, "%v\n", err)
		return ConfigError
	}

	// A one-shot prompt is the script's path: one question, the answer on stdout, no interface. It
	// is what makes a client useful on a machine with no terminal, which is the same reason -serve
	// exists for the other side.
	//
	// It is checked BEFORE any drawing, so the answer is the only thing on stdout and a pipeline
	// gets exactly what it asked for.
	if fl.prompt != "" {
		answer, err := client.RunPlan(ctx, fl.prompt, func(format string, args ...any) {
			fmt.Fprintf(op.Err, format, args...)
		})
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return RunError
		}
		fmt.Fprintln(op.Out, answer)
		return Success
	}

	ui := tui.New(client)
	ui.In = op.Stdin
	ui.Out = op.Out
	ui.Err = op.Err
	ui.NoColor = noColour(os.Getenv, op.Out)
	// The version of the GATEWAY, not of this binary. This process is a client: it builds no
	// sandbox and runs no commands, so the build that matters is the one on the other end - and
	// showing our own here would be a confident answer to a question nobody asked.
	ui.Version = op.gatewayVersion(ctx, fl.connect)

	// Entering the session is the SAME act as /attach, and it goes through the same code.
	//
	// It is not a convenience: -session is how a user says "take me back to that conversation", and
	// starting on a blank screen contradicts the request. Two implementations - one for the flag and
	// one for the command - would be two places for "what entering a session means" to drift, and
	// the flag is the one nobody would think to check.
	//
	// It CANNOT BLOCK THE INTERFACE, which is why it is a single call and not a hand-rolled loop
	// here: attachTo asks the gateway whether a turn is in flight (one bounded round trip) and
	// FOLLOWS that turn on its own goroutine, so the interface is drawn immediately and the run
	// reports into it. Doing the follow inline at this call site would hold the interface undrawn
	// until the turn ended, which is the one failure the user would see as "the program hangs".
	//
	// The follow is anchored to BaseCtx and NOT to the shutdown context. The two are different
	// questions and they must not share an answer: Ctrl+C ends the program, and a turn outlives the
	// client that was watching it - the gateway keeps working and a later client picks it up. If
	// the follow were on the shutdown context, leaving the interface would stop the turn, which is
	// precisely what this design exists to prevent. Escape still stops it, because Escape is the
	// user saying stop.
	//
	// A failure here is reported ONLY when the runner cannot enter conversations at all, which is
	// the case of the embedded interface and of the many small runners a test builds. With the
	// gateway, checkSession above has already answered the question a wrong id raises, with a better
	// message than this one could give, so a failure now would be about the connection - and an
	// interface with no way back to the session is still an interface.
	if err := ui.AttachTo(op.BaseCtx, session); err != nil {
		fmt.Fprintf(op.Err, "%v\n", err)
	}

	return ui.Run(context.Background())
}

// gatewayVersion asks the gateway at the other end which build it is.
//
// A function and NOT a field the caller must remember to fill, for the reason this feature exists:
// the interface has to be able to say what is answering, and a version that is only correct when
// somebody passed it along is a version that will one day be wrong. The seam exists so a test can
// assert the decision without a live gateway, exactly like DiscoverGateway.
//
// A failure is not reported: the version is the one decoration in the interface, and an interface
// that refuses to open because a label could not be fetched has its priorities backwards. The row
// simply goes undrawn.
func (op Options) gatewayVersion(ctx context.Context, addr string) string {
	probe := op.ProbeGatewayHealth
	if probe == nil {
		probe = gateway.ProbeHealth
	}
	health, err := probe(ctx, strings.TrimPrefix(connectURL(addr), "http://"))
	if err != nil {
		return ""
	}
	return health.Version
}

// connectURL turns the address a user types into a URL a client can use.
//
// A user writes an ADDRESS - "127.0.0.1:7477", "the-host:7477" - because that is what an address
// looks like, and demanding a scheme would be making them write the transport's name for a
// transport they never chose. Without this, the first thing a new user sees is
// `parse "127.0.0.1:7477/v1/sessions": first path segment in URL cannot contain colon`, which
// names neither the flag nor the fix.
//
// connectURL turns a user-typed address into a base URL.
//
// An address that already carries a scheme is left alone, so https:// can be written explicitly by
// anyone terminating TLS in front of the gateway.
func connectURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.Contains(addr, "://") {
		return addr
	}
	return "http://" + addr
}

// checkSession verifies that the conversation exists, and says what does exist when it does not.
//
// It asserts on the INTERFACE's own listing shape, which is the one the runner actually offers. An
// earlier version asserted on the gateway's wire type instead, and the adapter that now sits in
// between returns the interface's - so the assertion stopped matching and the check became a silent
// no-op. That is the exact failure this function exists to prevent, so the types have to agree.
func checkSession(client tui.Runner, session string) error {
	lister, ok := client.(interface {
		ListSessions(context.Context) ([]tui.SessionInfo, error)
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
