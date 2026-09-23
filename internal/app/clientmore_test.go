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
	"github.com/madkoding/starlight/internal/session"
	"github.com/madkoding/starlight/internal/tui"
)

// TestTheAdapterTranslatesTheConversation: the interface's Turn and the gateway's Turn are two
// types in two packages that cannot import each other, so this adapter is the only place the
// conversation can cross. A translation that dropped a field would show the user a conversation
// with the authors missing, which is a view that lies about who said what.
func TestTheAdapterTranslatesTheConversation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[
			{"User":"count the files","Agent":"there are twelve","Kind":"task"},
			{"User":"and the directories?","Agent":"three","Kind":"chat"}
		]}`))
	}))
	defer srv.Close()

	sw := sessionSwitcher{gateway.NewClientForSession(srv.URL, testToken, "default")}
	turns, err := sw.Conversation(context.Background())
	if err != nil {
		t.Fatalf("Conversation: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("translated %d turns, want 2: %+v", len(turns), turns)
	}
	if turns[0].User != "count the files" || turns[0].Agent != "there are twelve" {
		t.Errorf("the first turn lost something in the translation: %+v", turns[0])
	}
	if turns[1].Agent != "three" {
		t.Errorf("the second turn lost something in the translation: %+v", turns[1])
	}
}

// TestTheAdapterCarriesAFailedConversation: a gateway that cannot be asked what has been said must
// produce an error, not an empty conversation. An empty list reads as "nothing has been said", which
// is a different - and much more alarming - statement than "the question could not be asked".
func TestTheAdapterCarriesAFailedConversation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"no"}`))
	}))
	defer srv.Close()

	sw := sessionSwitcher{gateway.NewClientForSession(srv.URL, testToken, "default")}
	turns, err := sw.Conversation(context.Background())
	if err == nil {
		t.Fatal("a refused read must be reported")
	}
	if turns != nil {
		t.Errorf("a failed read returned %v, it must return nothing", turns)
	}
}

// TestNewClientWithoutASeamBuildsARealClient: the seam is for tests, and a process without one must
// still get a working client. Without this the seam would be the ONLY construction path, and the
// real program would be the one case nobody exercises.
func TestNewClientWithoutASeamBuildsARealClient(t *testing.T) {
	op := Options{}
	got := op.newClient("http://127.0.0.1:7477", testToken, "sabc")

	// The runner is the ADAPTER, not the bare client: the interface's optional session capability
	// needs the interface's own listing shape, and the adapter is what bridges it.
	sw, ok := got.(tui.SessionSwitcher)
	if !ok {
		t.Fatalf("newClient built a %T, which does not offer the interface's session capability", got)
	}
	if sw.CurrentSession() != "sabc" {
		t.Errorf("the client speaks for %q, want sabc", sw.CurrentSession())
	}
}

// TestTheAdapterTranslatesTheListing: the adapter is the only place the gateway's wire type and the
// interface's drawing type meet, and Current is computed here because it is a fact about the
// INTERFACE - the gateway has no business knowing which conversation a given front end is on.
func TestTheAdapterTranslatesTheListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessions":[
			{"id":"default","running":true},
			{"id":"sother","running":false}
		]}`))
	}))
	defer srv.Close()

	sw := sessionSwitcher{gateway.NewClientForSession(srv.URL, testToken, "sother")}
	all, err := sw.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("listed %d sessions, want 2", len(all))
	}
	if all[0].ID != "default" || !all[0].Running {
		t.Errorf("the running flag was lost in the translation: %+v", all[0])
	}
	if !all[1].Current {
		t.Errorf("the conversation the client is on must be marked as current: %+v", all[1])
	}
	if all[0].Current {
		t.Errorf("a conversation the client is NOT on is marked current: %+v", all[0])
	}
}

// TestSwitchingSessionsIsAOneWayMove: the client can be moved to another conversation, and doing
// so must take the cached state with it - a token from the previous conversation showing as this
// one's context is a status bar that lies.
func TestSwitchingSessionsIsAOneWayMove(t *testing.T) {
	c := gateway.NewClientForSession("http://127.0.0.1:7477", testToken, "default")
	if err := c.SwitchSession(context.Background(), "sother"); err != nil {
		t.Fatalf("SwitchSession: %v", err)
	}
	if got := c.CurrentSession(); got != "sother" {
		t.Errorf("the client is on %q, want sother", got)
	}
	// An empty id is refused rather than accepted as "the default": it would silently move the
	// client to a conversation nobody asked for.
	if err := c.SwitchSession(context.Background(), "  "); err == nil {
		t.Error("switching to an empty id must be refused")
	}
	if got := c.CurrentSession(); got != "sother" {
		t.Errorf("a refused switch moved the client to %q", got)
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
	c := listingRunner{sessions: []tui.SessionInfo{{ID: "default"}, {ID: "sother"}}}
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
		return listingRunner{sessions: []tui.SessionInfo{{ID: "default"}}}
	}

	if code := Run(op); code != Success {
		t.Fatalf("Run = %d; output: %s", code, out.String())
	}
	if !strings.HasPrefix(got, "http://") {
		t.Errorf("the client was given %q, it must be a usable URL", got)
	}
}

// TestTheAdapterCarriesAFailedListing: a gateway that cannot be asked which conversations it holds
// must produce an ERROR, not an empty list. An empty list would read as "there are none", which is
// a different - and much more alarming - statement than "the question could not be asked".
func TestTheAdapterCarriesAFailedListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"no"}`))
	}))
	defer srv.Close()

	sw := sessionSwitcher{gateway.NewClientForSession(srv.URL, testToken, "default")}
	all, err := sw.ListSessions(context.Background())
	if err == nil {
		t.Fatal("a refused listing must be reported")
	}
	if all != nil {
		t.Errorf("a failed listing returned %v, it must return nothing", all)
	}
}

// TestConnectWithAPromptAsksOnceAndPrintsTheAnswer: the mode a script uses, and the one that makes
// a client useful on a machine with no terminal - the same reason -serve exists for the other side.
// One question, the answer on stdout, no interface drawn and no agent in this process.
func TestConnectWithAPromptAsksOnceAndPrintsTheAnswer(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"the answer"})
	defer srv.Close()
	cfgPath := planConfig(t, srv)
	writeTokenFile(t, cfgPath)

	var out strings.Builder
	op, _ := connectOptions(t, &out, []string{
		"-config", cfgPath, "-connect", srv.URL, "-session", "default",
		"-p", "how many files are there?",
	})
	// The client is the seam, and it is a runner that answers the one question.
	op.NewClient = func(baseURL, tok, session string) tui.Runner {
		return answerRunner{answer: "the answer"}
	}

	if code := Run(op); code != Success {
		t.Fatalf("Run = %d; output: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "the answer") {
		t.Errorf("the answer was not printed: %s", out.String())
	}
}

// answerRunner is a tui.Runner that returns one plan answer and records nothing else.
type answerRunner struct{ answer string }

func (answerRunner) RunTask(context.Context, string, func(string, ...any)) (string, error) {
	return "", nil
}

func (r answerRunner) RunPlan(_ context.Context, _ string, progress func(string, ...any)) (string, error) {
	// The progress callback is exercised here rather than left unrun: it is how a long answer
	// reports what it is doing, and it goes to STDERR precisely so stdout carries the answer alone.
	if progress != nil {
		progress("working on it\n")
	}
	return r.answer, nil
}

func (answerRunner) RunConfig(context.Context) error           { return nil }
func (answerRunner) ConversationReport() string                { return "" }
func (answerRunner) ConversationSummary() session.Snapshot     { return session.Snapshot{} }
func (answerRunner) ResetConversation()                        {}
func (answerRunner) RunModels(context.Context) (string, error) { return "", nil }
func (answerRunner) Config() config.Config                     { return config.Default() }
func (answerRunner) SetReasoning(string)                       {}
func (answerRunner) RecordVerdict(bool, string) string         { return "" }
func (answerRunner) RewardReport() string                      { return "" }

// TestConnectWithAPromptReportsAFailedAnswer: a script needs a NON-ZERO exit when the question
// could not be answered, or a failure becomes an empty string in a pipeline and nobody notices.
func TestConnectWithAPromptReportsAFailedAnswer(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"unused"})
	defer srv.Close()
	cfgPath := planConfig(t, srv)
	writeTokenFile(t, cfgPath)

	var out strings.Builder
	op, _ := connectOptions(t, &out, []string{
		"-config", cfgPath, "-connect", srv.URL, "-p", "anything",
	})
	op.NewClient = func(baseURL, tok, session string) tui.Runner { return failingPlanRunner{} }

	if code := Run(op); code == Success {
		t.Fatalf("a failed question must not exit Success; output: %s", out.String())
	}
	if !strings.Contains(out.String(), "boom") {
		t.Errorf("the failure must be reported, got: %s", out.String())
	}
}

// failingPlanRunner is a runner whose plan fails, for the exit code.
type failingPlanRunner struct{ answerRunner }

func (failingPlanRunner) RunPlan(context.Context, string, func(string, ...any)) (string, error) {
	return "", errors.New("boom")
}

// TestAFailedQuestionStillPrintsItsProgressToStderr: progress on stderr and the answer on stdout is
// what makes "-connect ... -p" usable in a pipeline: the answer is the only thing a downstream
// command sees, while a human watching the terminal still gets told what is happening.
func TestAFailedQuestionStillPrintsItsProgressToStderr(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"unused"})
	defer srv.Close()
	cfgPath := planConfig(t, srv)
	writeTokenFile(t, cfgPath)

	var stdout, stderr strings.Builder
	op, _ := connectOptions(t, &stderr, []string{
		"-config", cfgPath, "-connect", srv.URL, "-p", "anything",
	})
	op.Out = &stdout
	op.Err = &stderr
	op.NewClient = func(baseURL, tok, session string) tui.Runner { return answerRunner{answer: "42"} }

	if code := Run(op); code != Success {
		t.Fatalf("Run = %d; stderr: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "42" {
		t.Errorf("stdout = %q, it must carry the answer and nothing else", got)
	}
	if !strings.Contains(stderr.String(), "working on it") {
		t.Errorf("progress did not reach stderr: %q", stderr.String())
	}
}
