package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/gateway"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/tui"
)

// TestNewClientWithoutASeamBuildsARealClient: the seam is for tests, and a process without one must
// still get a working client. Without this the seam would be the ONLY construction path, and the
// real program would be the one case nobody exercises.
func TestNewClientWithoutASeamBuildsARealClient(t *testing.T) {
	op := Options{}
	got := op.newClient("http://127.0.0.1:7477", testToken, "sabc")

	client, ok := got.(*gateway.Client)
	if !ok {
		t.Fatalf("newClient built a %T, want the real client", got)
	}
	if client.Session() != "sabc" {
		t.Errorf("the client speaks for %q, want sabc", client.Session())
	}
}

// TestParseRefusesAClientFlagWithoutItsValue: -connect and -session name things, and naming nothing
// is a mistake worth catching at the parser rather than three layers down. This is the branch that
// turns "-connect=" into a sentence the user can act on.
func TestParseRefusesAClientFlagWithoutItsValue(t *testing.T) {
	for _, args := range [][]string{
		{"-connect"},
		{"-connect="},
		{"-connect", "   "},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("%v was accepted, -connect needs an address", args)
		}
	}
	// And a well-formed pair is read back exactly.
	fl, err := parse([]string{"-connect", "127.0.0.1:7477", "-session", "sabc"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fl.connect != "127.0.0.1:7477" || fl.session != "sabc" {
		t.Errorf("parsed %q and %q", fl.connect, fl.session)
	}
	// -session without a value is the same class of mistake.
	if _, err := parse([]string{"-session"}); err == nil {
		t.Error("-session with no value must be refused")
	}
}

// TestCheckSessionReportsAGatewayThatWillNotAnswer: a gateway that cannot be asked anything is
// worth saying out loud BEFORE an interface is handed to a user, because the alternative is an
// interface where every command fails for a reason nobody stated.
func TestCheckSessionReportsAGatewayThatWillNotAnswer(t *testing.T) {
	c := listingRunner{err: errors.New("connection refused")}
	err := checkSession(c, "default")
	if err == nil {
		t.Fatal("a gateway that did not answer must be reported")
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("the failure must say the gateway did not answer, got: %v", err)
	}
}

// TestCheckSessionReportsAGatewayHoldingNothing: an empty list is not a wrong id - it is a gateway
// with no conversations at all, and saying so is different from listing the ones that exist.
func TestCheckSessionReportsAGatewayHoldingNothing(t *testing.T) {
	err := checkSession(listingRunner{}, "default")
	if err == nil {
		t.Fatal("a gateway holding no sessions must be reported")
	}
	if !strings.Contains(err.Error(), "none at all") {
		t.Errorf("the failure must distinguish an empty gateway, got: %v", err)
	}
}

// TestCheckSessionPassesForARunnerThatCannotList: the check is a courtesy. A runner that is not a
// remote client - the embedded interface builds one - must not be refused by it.
func TestCheckSessionPassesForARunnerThatCannotList(t *testing.T) {
	if err := checkSession(noopRunner{}, "anything"); err != nil {
		t.Errorf("a runner that cannot list sessions must pass the check, got: %v", err)
	}
}

// TestCheckSessionAcceptsAConversationThatExists: the happy path, which has to be a test or the
// three failures above could all pass with a function that always fails.
func TestCheckSessionAcceptsAConversationThatExists(t *testing.T) {
	c := listingRunner{sessions: []gateway.SessionStatus{{ID: "default"}, {ID: "sother"}}}
	if err := checkSession(c, "sother"); err != nil {
		t.Errorf("an existing session was refused: %v", err)
	}
}

// TestClientModeStillAnswersTheInterfaceSeam: runClient hands the interface to the test seam when
// one is installed, EXACTLY like the embedded path does. A client mode that ignored the seam would
// be untestable, and a test could pass while exercising a different program than it thought.
func TestClientModeStillAnswersTheInterfaceSeam(t *testing.T) {
	called := false
	var out strings.Builder
	op, _ := connectOptions(t, &out, []string{"-connect", "127.0.0.1:7477", "-tui"})
	op.RunTUI = func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
		called = true
		return Success
	}
	if code := Run(op); code != Success {
		t.Fatalf("Run = %d, want Success; output: %s", code, out.String())
	}
	if !called {
		t.Error("the interface seam was not used in client mode")
	}
}

// TestServingAndConnectingAtOnceIsRefused: -serve makes this process a gateway and -connect makes
// it a client of somebody else's. Both at once cannot both be true, and a program that silently
// picked one would leave the user staring at an interface for the thing they did not ask for.
//
// It is refused rather than resolved because there is no sensible resolution: the two flags
// contradict, and the only thing a program can honestly do with a contradiction is say so.
func TestServingAndConnectingAtOnceIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	var out strings.Builder
	op, _ := connectOptions(t, &out, []string{"-connect", srv.URL, "-serve"})
	op.RunTUI = func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
		return Success
	}
	if code := Run(op); code == Success {
		t.Fatalf("-serve with -connect was accepted; output: %s", out.String())
	}
	if !strings.Contains(out.String(), "connect") {
		t.Errorf("the refusal must name the flags involved, got: %s", out.String())
	}
}

var _ tui.Runner = noopRunner{}

// TestAnAddressWithoutASchemeIsAccepted: the user types an ADDRESS, not a URL. "-connect
// 127.0.0.1:7477" is what the help text shows and what an address looks like, and refusing it with
// a parse error that names neither the flag nor the fix was a real failure found by running it.
func TestAnAddressWithoutASchemeIsAccepted(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:7477": "http://127.0.0.1:7477",
		"the-host:7477":  "http://the-host:7477",
		" http://h:1 ":   "http://h:1",
		"https://h:7477": "https://h:7477",
	}
	for in, want := range cases {
		if got := connectURL(in); got != want {
			t.Errorf("connectURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestConnectPassesAUsableURLToTheClient: the normalisation has to REACH the client, or the fix is
// in a function nobody calls - which is the shape of the bug it repairs.
func TestConnectPassesAUsableURLToTheClient(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	cfgPath := planConfig(t, srv)
	writeTokenFile(t, cfgPath)

	var got string
	var out strings.Builder
	// The address is given WITHOUT a scheme, exactly as a user would type it.
	bare := strings.TrimPrefix(srv.URL, "http://")
	op, _ := connectOptions(t, &out, []string{"-config", cfgPath, "-connect", bare})
	op.NewClient = func(baseURL, tok, session string) tui.Runner {
		got = baseURL
		return listingRunner{sessions: []gateway.SessionStatus{{ID: "default"}}}
	}

	if code := Run(op); code != Success {
		t.Fatalf("Run = %d; output: %s", code, out.String())
	}
	if !strings.HasPrefix(got, "http://") {
		t.Errorf("the client was given %q, it must be a usable URL", got)
	}
}
