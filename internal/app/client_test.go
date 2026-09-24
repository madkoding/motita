package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/session"
	"github.com/madkoding/motita/internal/tui"
)

// noopRunner is a tui.Runner that does nothing, for tests that only care WHICH runner was built.
type noopRunner struct{}

func (noopRunner) RunTask(context.Context, string, func(string, ...any)) (string, error) {
	return "", nil
}

func (noopRunner) RunPlan(context.Context, string, func(string, ...any)) (string, error) {
	return "", nil
}

func (noopRunner) RunConfig(context.Context) error           { return nil }
func (noopRunner) ConversationReport() string                { return "" }
func (noopRunner) ConversationSummary() session.Snapshot     { return session.Snapshot{} }
func (noopRunner) ResetConversation()                        {}
func (noopRunner) RunModels(context.Context) (string, error) { return "", nil }
func (noopRunner) Config() config.Config                     { return config.Default() }
func (noopRunner) SetReasoning(string)                       {}
func (noopRunner) RecordVerdict(bool, string) string         { return "" }
func (noopRunner) RewardReport() string                      { return "" }

// ListSessions is here because checkSession reaches for it: a double that omitted it would take
// the "this is not a remote client" branch and the session check would never run, which is the
// opposite of what these tests are for.
func (r listingRunner) ListSessions(context.Context) ([]tui.SessionInfo, error) {
	return r.sessions, r.err
}

// listingRunner is a tui.Runner that ANSWERS the session listing, which noopRunner does not.
type listingRunner struct {
	noopRunner
	sessions []tui.SessionInfo
	err      error
}

// writeTokenFile puts a token where the configuration will look for it.
//
// A RELATIVE token_file resolves BESIDE THE CONFIGURATION FILE, not under the home: that is the
// convention config.go documents (a path written in a file is relative to that file), and it is
// why the gateway writes its token next to its config. A test that put it anywhere else would be
// testing a client that cannot find its own credential - which is a different test, and there is
// one for exactly that.
func writeTokenFile(t *testing.T, cfgPath string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(cfgPath), "gateway.token")
	if err := os.WriteFile(path, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return path
}

// testToken is a well-formed token: MinTokenBytes BYTES is 64 hexadecimal characters.
const testToken = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

// connectOptions is the minimum a -connect test needs, with the two seams a client mode touches.
//
// NewSandbox is INJECTED and records that it was called: the claim being tested is that the client
// path does NOT build one, and a claim about something not happening is only worth anything if the
// thing would otherwise have happened.
func connectOptions(t *testing.T, out io.Writer, args []string) (Options, *bool) {
	t.Helper()
	built := false
	op := Options{
		Args:    args,
		Out:     out,
		Err:     out,
		Stdin:   strings.NewReader(""),
		BaseCtx: context.Background(),
		NewSandbox: func(sandbox.Options) (*sandbox.Sandbox, error) {
			built = true
			return nil, nil
		},
	}
	// RunTUI is deliberately NOT set here. It is the seam that makes runClient skip straight to
	// the interface, so a test that sets it cannot observe the client that was built - which is
	// what most of these tests are about. Only the sandbox test wants it.
	return op, &built
}

// TestConnectDoesNotBuildASandbox: a remote client runs no commands of its own - the commands run on
// the gateway's machine - so the sandbox it would build is a sandbox it can never use. Building it
// anyway is not harmless: on a machine without cgroup permissions, `motita -connect host -tui`
// fails at "could not prepare the sandbox" for something the user never asked for.
func TestConnectDoesNotBuildASandbox(t *testing.T) {
	var out strings.Builder
	op, boxBuilt := connectOptions(t, &out, []string{"-connect", "127.0.0.1:7477", "-tui"})
	// The interface itself is replaced, so this test needs no gateway listening anywhere. What it
	// is watching for is the sandbox ABOVE that replacement, on the way in.
	op.RunTUI = func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
		return Success
	}

	_ = Run(op)
	if *boxBuilt {
		t.Fatal("connecting to a gateway built a sandbox, which a remote client has no use for")
	}
}

// TestConnectUsesTheGatewayItWasGiven: the address and the token come from where the user said, not
// from a gateway this process started.
func TestConnectUsesTheGatewayItWasGiven(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	// A real gateway, with a real token file, and a configuration that points at both.
	cfgPath := planConfig(t, srv)
	writeTokenFile(t, cfgPath)

	var got string
	var out strings.Builder
	op, _ := connectOptions(t, &out, []string{"-config", cfgPath, "-connect", srv.URL, "-tui"})
	// The gateway's address and token file are what the CONFIG says, so the flag alone decides
	// nothing: this is the test that the client reads them from where they live.
	op.NewClient = func(baseURL, tok, session string) tui.Runner {
		got = baseURL + "|" + session
		if tok != testToken {
			t.Errorf("the client was handed a token other than the one in the file")
		}
		return noopRunner{}
	}

	_ = Run(op)
	if !strings.HasPrefix(got, srv.URL+"|") {
		t.Fatalf("the client pointed at %q, it must point at the gateway it was given", got)
	}
	if !strings.HasSuffix(got, "|"+gateway.DefaultSession) {
		t.Errorf("the client asked for %q, it must default to the default conversation", got)
	}
}

// TestConnectWithoutATokenFileSaysSo: the failure the user will actually hit, and it has to be
// actionable rather than a bare 401 later.
func TestConnectWithoutATokenFileSaysSo(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	var out strings.Builder
	cfgPath := planConfig(t, srv)
	// HOME is what decides where the default token file lives, so an empty HOME means an empty
	// token directory - the shape of a first try against somebody else's gateway.
	t.Setenv("HOME", t.TempDir())

	op, _ := connectOptions(t, &out, []string{"-config", cfgPath, "-connect", srv.URL, "-tui"})
	code := Run(op)
	if code == Success {
		t.Fatal("connecting without a token must fail")
	}
	if !strings.Contains(out.String(), "token") {
		t.Errorf("the failure must mention the token, got: %s", out.String())
	}
}

// TestConnectWithoutAnAddressIsRefused: -connect with nothing after it is a mistake, and the
// message must say which flag is incomplete rather than starting a client pointed at nothing.
func TestConnectWithoutAnAddressIsRefused(t *testing.T) {
	var out strings.Builder
	op, _ := connectOptions(t, &out, []string{"-connect"})
	if code := Run(op); code == Success {
		t.Fatalf("-connect with no address must fail; output was: %s", out.String())
	}
	if !strings.Contains(out.String(), "connect") {
		t.Errorf("the failure must name the flag, got: %s", out.String())
	}
}

// TestASessionThatDoesNotExistIsReportedAtStart: a session id the gateway does not know must be
// reported BEFORE the interface opens. An interface that opens and then fails on every command with
// a 404 is worse than a refusal, because the user has no way to learn which ids are real - and the
// answer is one request away, so the message lists them.
func TestASessionThatDoesNotExistIsReportedAtStart(t *testing.T) {
	silence(t)

	// A gateway that answers the session listing with one real conversation and refuses anything
	// else, which is what a wrong id looks like from the client's side.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessions":[{"id":"default"},{"id":"sother"}]}`))
	})
	mux.HandleFunc("/v1/sessions/sother", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such session"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfgPath := planConfig(t, srv)
	writeTokenFile(t, cfgPath)

	var out strings.Builder
	op, _ := connectOptions(t, &out, []string{
		"-config", cfgPath, "-connect", srv.URL, "-tui", "-session", "smissing",
	})
	op.NewClient = func(baseURL, tok, session string) tui.Runner {
		return listingRunner{sessions: []tui.SessionInfo{
			{ID: "default"}, {ID: "sother"},
		}}
	}

	code := Run(op)
	if code == Success {
		t.Fatal("connecting to a session that does not exist must fail, not open an interface")
	}
	msg := out.String()
	if !strings.Contains(msg, "smissing") {
		t.Errorf("the failure must name the session that was asked for, got: %s", msg)
	}
	// And it says what DOES exist, because otherwise the user has no next step.
	if !strings.Contains(msg, "sother") {
		t.Errorf("the failure must list the sessions that do exist, got: %s", msg)
	}
}
