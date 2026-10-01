package tui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/onboard"
	"github.com/madkoding/motita/internal/sandbox"
	taskpkg "github.com/madkoding/motita/internal/task"
)

// A failed turn says what happened and, for the failures a newcomer meets first, what to do.
func TestErrorsCarryAHintForTheCommonFailures(t *testing.T) {
	local := config.Default()
	local.LLM.Provider = "ollama"
	local.LLM.BaseURL = "http://localhost:11434/v1"
	cases := []struct {
		cfg  config.Config
		err  string
		hint string
	}{
		{local, "dial tcp 127.0.0.1:11434: connect: connection refused", "ollama serve"},
		{configWithKey("k"), "Post https://api.openai.com: no such host", "could not reach the provider"},
		{configWithKey("k"), "a run is already in progress in this session", "still stopping"},
		{configWithKey(""), "the LLM key is missing", "/config to add one"},
		{configWithKey("k"), "HTTP 401: invalid api key", "refused the key"},
		{configWithKey("k"), "HTTP 429: rate limit reached", "out of credit"},
		{configWithKey("k"), "HTTP 404: model not found", "/models lists"},
		{configWithKey("k"), "the sandbox could not start", ""},
	}
	for _, tc := range cases {
		tu, _ := newKeyTUI("")
		tu.Runner = &fakeRunner{cfg: tc.cfg, cfgSet: true}
		got := tu.errorText(errors.New(tc.err))
		if !strings.HasPrefix(got, "error: "+tc.err) {
			t.Errorf("the error itself must come first and whole: %q", got)
		}
		if tc.hint == "" {
			if strings.Contains(got, "hint:") {
				t.Errorf("an unknown failure must get no hint: %q", got)
			}
			continue
		}
		if !strings.Contains(got, tc.hint) {
			t.Errorf("%q: the hint must mention %q, got %q", tc.err, tc.hint, got)
		}
	}
}

// reloadRunner is a runner that can apply the setup the wizard wrote.
type reloadRunner struct {
	*fakeRunner
	reloadErr error
	reloads   int
	// after is the configuration the reload leaves in place.
	after config.Config
}

func (r *reloadRunner) ReloadConfig(context.Context) error {
	r.reloads++
	if r.reloadErr != nil {
		return r.reloadErr
	}
	r.fakeRunner.mu.Lock()
	r.fakeRunner.cfg, r.fakeRunner.cfgSet = r.after, true
	r.fakeRunner.mu.Unlock()
	return nil
}

// /config used to say "configuration written" while every following turn still ran with the old
// provider. The setup is applied before the interface says anything, and what it says is what is
// in use now.
func TestTheSetupIsAppliedAndSaid(t *testing.T) {
	after := configWithKey("sk-new")
	after.LLM.Provider, after.LLM.Model = "anthropic", "claude-sonnet-5-5"
	r := &reloadRunner{fakeRunner: &fakeRunner{}, after: after}
	tu := newFakeTUI("/config\nq\n", r)
	tu.Run(context.Background())
	if r.reloads != 1 {
		t.Fatalf("the setup must be applied once, got %d", r.reloads)
	}
	if !strings.Contains(messageTexts(tu), "now using anthropic · claude-sonnet-5-5") {
		t.Errorf("the interface must say what is in use:\n%s", messageTexts(tu))
	}

	// A setup with no key yet is applied, and the missing key is said.
	r = &reloadRunner{fakeRunner: &fakeRunner{}, after: configWithKey("")}
	tu = newFakeTUI("/config\nq\n", r)
	tu.Run(context.Background())
	if !strings.Contains(messageTexts(tu), "There is no API key yet") {
		t.Errorf("a missing key must be said:\n%s", messageTexts(tu))
	}

	// A setup that cannot be applied says so, and how to get it applied.
	r = &reloadRunner{fakeRunner: &fakeRunner{}, reloadErr: errors.New("invalid YAML")}
	tu = newFakeTUI("/config\nq\n", r)
	tu.Run(context.Background())
	if !strings.Contains(messageTexts(tu), "could not be applied now (invalid YAML)") {
		t.Errorf("the failure must be said:\n%s", messageTexts(tu))
	}

	// A runner that cannot apply a setup at all asks for a restart.
	tu = newFakeTUI("/config\nq\n", &fakeRunner{})
	tu.Run(context.Background())
	if !strings.Contains(messageTexts(tu), "Restart motita to use it") {
		t.Errorf("a runner without a reload must say how to apply the setup:\n%s", messageTexts(tu))
	}
}

func TestACancelledSetupChangesNothing(t *testing.T) {
	r := &reloadRunner{fakeRunner: &fakeRunner{configErr: onboard.ErrCancelled}}
	tu := newFakeTUI("/config\nq\n", r)
	tu.Run(context.Background())
	if r.reloads != 0 {
		t.Error("a cancelled setup must not be applied")
	}
	if !strings.Contains(messageTexts(tu), "setup cancelled: nothing was changed") {
		t.Errorf("the cancellation must be said:\n%s", messageTexts(tu))
	}
}

// The interface runs the wizard it is handed, which is this process's own even when the runner is
// a client of a gateway on another machine.
func TestTheSetupFieldIsTheWizardThatRuns(t *testing.T) {
	runner := &fakeRunner{}
	tu := newFakeTUI("/config\nq\n", runner)
	ran := false
	tu.Setup = func(context.Context) error { ran = true; return nil }
	tu.Run(context.Background())
	if !ran || runner.configCalled {
		t.Errorf("the Setup field must run instead of the runner's wizard (setup=%v runner=%v)", ran, runner.configCalled)
	}
}

// On the Setup screen an empty line runs the setup.
func TestEnterOnTheSetupScreenRunsTheSetup(t *testing.T) {
	runner := &fakeRunner{}
	tu := newFakeTUI("\nq\n", runner)
	tu.screen = ScreenConfig
	tu.Run(context.Background())
	if runner.configCalls != 1 {
		t.Errorf("the setup must run once, got %d", runner.configCalls)
	}
}

// writeSetup writes the configuration the wizard would, in a HOME of the test's own.
func writeSetup(t *testing.T, yaml, creds string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, v := range []string{"MOTITA_LLM_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "MOTITA_LLM_PROVIDER", "MOTITA_LLM_MODEL"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if creds != "" {
		if err := os.WriteFile(config.CredentialsPath(config.File()), []byte(creds), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReloadConfigAppliesTheNewSetup(t *testing.T) {
	writeSetup(t, "llm:\n  provider: anthropic\n  model: claude-sonnet-5-5\nanchor:\n  kind: auto\n",
		"export ANTHROPIC_API_KEY='sk-ant-new'\n")
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, configWithKey("sk-old"), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.SetLLM("gemini", "") // remembers an endpoint, which the reload must forget
	if err := r.ReloadConfig(context.Background()); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	got := r.Config()
	if got.LLM.Provider != "anthropic" || got.LLM.Model != "claude-sonnet-5-5" || got.LLM.APIKey != "sk-ant-new" {
		t.Errorf("llm = %+v", got.LLM)
	}
	if got.Anchor.Kind != "auto" {
		t.Errorf("the check must be applied too, got %q", got.Anchor.Kind)
	}
	if r.Engine != nil || r.endpoints != nil {
		t.Error("the engine and the remembered endpoints belong to the old setup")
	}
}

// A setup with no key yet is still applied: the interface then says the key is missing.
func TestReloadConfigAppliesASetupWithNoKey(t *testing.T) {
	writeSetup(t, "llm:\n  provider: anthropic\n  model: m\n", "")
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, configWithKey("sk-old"), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	if err := r.ReloadConfig(context.Background()); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	if got := r.Config().LLM; got.Provider != "anthropic" || got.APIKey != "" {
		t.Errorf("llm = %+v", got)
	}
}

func TestReloadConfigRefusesAFileThatDoesNotLoad(t *testing.T) {
	writeSetup(t, "llm: [not, a, map\n", "")
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, configWithKey("sk-old"), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	if err := r.ReloadConfig(context.Background()); err == nil {
		t.Fatal("a file that does not load must be an error")
	}
	if r.Config().LLM.APIKey != "sk-old" {
		t.Error("nothing may change when the file does not load")
	}
}

func TestSetupPathWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := setupPath(); got != filepath.Join(".", "motita.yaml") && got != "./motita.yaml" {
		t.Errorf("setupPath = %q", got)
	}
}

// stoppedAgent ends its run quietly when it is cancelled, the way the real agent does.
type stoppedAgent struct{ fakeAgent }

func (stoppedAgent) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// A STOPPED turn is reported as stopped, not as a finished one with nothing to say.
func TestAStoppedTaskIsReportedAsStopped(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return stoppedAgent{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.RunTask(ctx, "valid task", func(string, ...any) {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// While a command waits for approval, the keys answer it - even with the turn running beside the
// input loop.
func TestTheLoopAnswersAnApprovalWhileTheTurnRuns(t *testing.T) {
	r := newLiveRunner()
	tu, keys := newLiveTUI(r)
	done := runLive(context.Background(), tu)

	keys.Write([]byte("deploy\n"))
	waitOn(t, r.started, "the task to start")
	reply := make(chan bool, 1)
	tu.approvalChannel() <- &confirmState{req: agent.ApprovalRequest{Command: "make deploy"}, reply: reply}
	keys.Write([]byte("y"))
	keys.Write([]byte("\n"))
	if !waitOn(t, reply, "the answer") {
		t.Error("y must approve the command")
	}
	// A message typed while the turn runs is queued, and /quit stops the turn without sending it.
	keys.Write([]byte("and then this\n"))
	go keys.Write([]byte("/quit\n"))
	waitOn(t, r.stopped, "the task to stop")
	waitOn(t, done, "the interface to exit")
	text := messageTexts(tu)
	if !strings.Contains(text, "approved by the user") || !strings.Contains(text, "Queued") {
		t.Errorf("the conversation must record the approval and the queued message:\n%s", text)
	}
	select {
	case task := <-r.started:
		t.Errorf("the queued message must not start on the way out, got %q", task)
	default:
	}
}
