package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
)

// modelsRunner is a runner on openai with an injected engine, and no stray keys in the environment.
func modelsRunner(t *testing.T, models config.Models) (*AppRunner, *llm.Client) {
	t.Helper()
	for _, v := range []string{"ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENAI_API_KEY", "MOTITA_LLM_API_KEY"} {
		t.Setenv(v, "")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MOTITA_AUTH_DIR", t.TempDir())
	cfg := config.Default()
	cfg.LLM.Provider, cfg.LLM.Model = "openai", "gpt-5-mini"
	cfg.LLM.BaseURL, cfg.LLM.APIKey = "https://api.openai.com/v1", "sk-openai"
	cfg.Models = models
	own := &llm.Client{}
	return NewAppRunner(&strings.Builder{}, &strings.Builder{}, cfg, own, &sandbox.Sandbox{}, logx.Global()), own
}

// A kind of work with a model of its own runs on it, with its provider's key and endpoint; a kind
// with none, or with the session's own model, runs on the session's engine.
func TestEngineForUsesTheModelAssignedToTheKind(t *testing.T) {
	r, own := modelsRunner(t, config.Models{
		Plan:    "anthropic/claude-opus-5-5",
		Code:    "openai/gpt-5-mini",
		Summary: "codex/gpt-5-nano",
	})
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")

	cfg, e, err := r.engineFor(config.TaskPlan)
	planLLM := cfg.LLM.Provider == "anthropic" && cfg.LLM.Model == "claude-opus-5-5" &&
		cfg.LLM.APIKey == "sk-ant" && cfg.LLM.BaseURL == "https://api.anthropic.com"
	if err != nil || e == own || !planLLM {
		t.Fatalf("plan: %v %+v", err, cfg.LLM)
	}
	if _, again, _ := r.engineFor(config.TaskPlan); again != e {
		t.Error("the engine of a kind is built once per session")
	}
	if _, e, _ := r.engineFor(config.TaskCode); e != own {
		t.Error("the session's own model needs no engine of its own")
	}
	if _, e, _ := r.engineFor(config.TaskReview); e != own {
		t.Error("a kind with no model runs on the session's")
	}
	// codex shares the OpenAI account: the key carries over.
	if cfg, _, err := r.engineFor(config.TaskSummary); err != nil || cfg.LLM.APIKey != "sk-openai" {
		t.Errorf("summary: %v %+v", err, cfg.LLM)
	}

	// The session's provider with another model keeps the session's key and endpoint.
	r.SetTaskModels(config.Models{Review: "openai/gpt-5"})
	if cfg, e, err := r.engineFor(config.TaskReview); err != nil || e == own || cfg.LLM.APIKey != "sk-openai" ||
		cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("review: %v %+v", err, cfg.LLM)
	}

	r.SetTaskModels(config.Models{})
	if _, e, _ := r.engineFor(config.TaskPlan); e != own {
		t.Error("a new set drops the engines built from the old one")
	}
}

// A model whose provider cannot run falls back to the session's, so the work still runs.
func TestEngineForFallsBackWhenTheAssignedModelCannotRun(t *testing.T) {
	r, own := modelsRunner(t, config.Models{Review: "gemini/gemini-3-pro"})
	if _, e, err := r.engineFor(config.TaskReview); err != nil || e != own {
		t.Errorf("review: %v, own=%v", err, e == own)
	}
	if e := r.roleEngine(config.TaskReview); e != own {
		t.Error("the role's engine falls back too")
	}
	if e := r.summaryEngine(own); e != own {
		t.Error("with no summary model, compaction uses the turn's engine")
	}
}

// The summary model compacts the conversation when one is assigned; the role engine hands a
// background agent its role's model, and nil when even the session's engine cannot be built.
func TestSummaryAndRoleEngines(t *testing.T) {
	r, own := modelsRunner(t, config.Models{Summary: "anthropic/claude-haiku-4-5", Research: "anthropic/claude-haiku-4-5"})
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	if e := r.summaryEngine(own); e == own || e == nil {
		t.Error("compaction runs on the summary model")
	}
	if e := r.roleEngine(config.TaskResearch); e == own || e == nil {
		t.Error("a research agent runs on the research model")
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	r.SetTaskModels(config.Models{Summary: "anthropic/claude-haiku-4-5"})
	if e := r.summaryEngine(own); e != own {
		t.Error("a summary model that cannot run leaves compaction on the turn's engine")
	}

	broken, _ := modelsRunner(t, config.Models{})
	broken.cfgMu.Lock()
	broken.Engine, broken.Cfg.LLM.Provider = nil, "nope"
	broken.cfgMu.Unlock()
	if e := broken.roleEngine(config.TaskCode); e != nil {
		t.Error("no engine at all is nil, and the agent keeps its own")
	}
	broken.SetTaskModels(config.Models{Summary: "gemini/gemini-3-flash"})
	if e := broken.summaryEngine(own); e != own {
		t.Error("with nothing to build, compaction keeps the turn's engine")
	}
}

// roleAgent records the role engines a turn hands it.
type roleAgent struct {
	askingAgent
	roles func(string) *llm.Client
}

func (a *roleAgent) SetRoleEngines(fn func(string) *llm.Client) { a.roles = fn }

// A task turn hands its agent the session's role engines, so the background agents it starts
// run on this session's models and on no other session's.
func TestRunTaskHandsTheAgentTheSessionsRoleEngines(t *testing.T) {
	r, own := modelsRunner(t, config.Models{})
	ag := &roleAgent{askingAgent: askingAgent{res: agent.TaskResult{Pass: true, Summary: "done"}}}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return ag
	}
	if _, err := r.RunTask(context.Background(), "do it", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if ag.roles == nil || ag.roles(config.TaskReview) != own {
		t.Error("the agent was not handed the session's role engines")
	}
}

// dropEngines forgets the session engine and every engine of a kind.
func TestDropEnginesForgetsEveryEngine(t *testing.T) {
	r, _ := modelsRunner(t, config.Models{Plan: "openai/gpt-5"})
	r.cfgMu.Lock()
	r.taskEngines = map[string]*llm.Client{config.TaskPlan: {}}
	r.dropEngines()
	gone := r.taskEngines == nil && r.Engine == nil
	r.cfgMu.Unlock()
	if !gone {
		t.Error("dropEngines forgets every engine")
	}
}

// noteAgent records the project note a turn hands it.
type noteAgent struct {
	askingAgent
	note string
}

func (a *noteAgent) SetProjectNote(note string) { a.note = note }

// A task turn is told about the project it works in, with the same sentences Plan mode's system
// prompt ends with: how it is run and which project it is.
func TestRunTaskHandsTheAgentTheProject(t *testing.T) {
	r, _ := modelsRunner(t, config.Models{})
	ag := &noteAgent{askingAgent: askingAgent{res: agent.TaskResult{Pass: true, Summary: "done"}}}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return ag
	}
	r.SetProjectNote("\n\nThis session belongs to the project \"Shop\".")
	r.SetContainerRuntime("podman")
	if _, err := r.RunTask(context.Background(), "do it", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ag.note, "run with podman") || !strings.HasSuffix(ag.note, `the project "Shop".`) {
		t.Errorf("the agent was not told about its project: %q", ag.note)
	}
}

// A tier engine exists only when the tier is assigned.
func TestTierEngineExistsOnlyWhenAssigned(t *testing.T) {
	r, _ := modelsRunner(t, config.Models{})
	if r.tierEngine(config.TaskCheap) != nil {
		t.Error("an unassigned tier has no engine")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	r.SetTaskModels(config.Models{Cheap: "anthropic/claude-haiku-4-5"})
	if r.tierEngine(config.TaskCheap) == nil || r.tierEngine(config.TaskStrong) != nil {
		t.Error("the assigned tier has an engine, the other does not")
	}
}

// classifierRunner is a runner whose session model answers every call with reply.
func classifierRunner(t *testing.T, reply string) *AppRunner {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, reply)
	}))
	t.Cleanup(srv.Close)
	r, _ := modelsRunner(t, config.Models{})
	r.Cfg.LLM.BaseURL = srv.URL
	r.Cfg.LLM.MaxAttempts = 1
	r.Cfg.LLM.Timeout = 5 * time.Second
	engine, err := llm.New(r.Cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine
	return r
}

// The classifier sends EASY to the cheap tier and everything else, a failed call and an engine
// that cannot be built included, to the strong one: a hard piece never goes to the weak model
// because the judge was unreachable.
func TestClassifyPieceDefaultsToStrong(t *testing.T) {
	if got := classifierRunner(t, " easy ").classifyPiece(context.Background(), "p", "b"); got != config.TaskCheap {
		t.Errorf("EASY = %q, want cheap", got)
	}
	if got := classifierRunner(t, "HARD").classifyPiece(context.Background(), "p", "b"); got != config.TaskStrong {
		t.Errorf("HARD = %q, want strong", got)
	}
	many := "touch a.go b.go c.go d.go and keep the files in step"
	if got := classifierRunner(t, "EASY").classifyPiece(context.Background(), "p", many); got != config.TaskStrong {
		t.Errorf("a brief naming many files = %q, want strong without asking", got)
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if got := classifierRunner(t, "EASY").classifyPiece(ctx, "p", "b"); got != config.TaskStrong {
		t.Errorf("a failed judge = %q, want strong", got)
	}
	broken, _ := modelsRunner(t, config.Models{})
	broken.cfgMu.Lock()
	broken.Engine, broken.Cfg.LLM.Provider = nil, "nope"
	broken.cfgMu.Unlock()
	if got := broken.classifyPiece(context.Background(), "p", "b"); got != config.TaskStrong {
		t.Errorf("no engine = %q, want strong", got)
	}
}

// tierAgent records the tier routing a turn hands it.
type tierAgent struct {
	askingAgent
	engine   func(string) *llm.Client
	classify func(context.Context, string, string) string
}

func (a *tierAgent) SetTiers(e func(string) *llm.Client, c func(context.Context, string, string) string) {
	a.engine, a.classify = e, c
}

// A task turn hands its agent the session's tiers, like its role engines.
func TestRunTaskHandsTheAgentTheTiers(t *testing.T) {
	r, _ := modelsRunner(t, config.Models{})
	ag := &tierAgent{askingAgent: askingAgent{res: agent.TaskResult{Pass: true, Summary: "done"}}}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return ag
	}
	if _, err := r.RunTask(context.Background(), "do it", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if ag.engine == nil || ag.classify == nil || ag.engine(config.TaskCheap) != nil {
		t.Error("the agent was not handed the session's tiers")
	}
}

// pieceSignals counts the different files a brief names and notices tests.
func TestPieceSignals(t *testing.T) {
	if f, tests := pieceSignals("edit main.go and main.go, then util/x.ts"); f != 2 || tests {
		t.Errorf("files=%d tests=%v, want 2 false", f, tests)
	}
	if _, tests := pieceSignals("Añade la prueba"); !tests { // spanish-fixture: a brief in Spanish
		t.Error("a Spanish brief that asks for a test")
	}
	if _, tests := pieceSignals("add a Test"); !tests {
		t.Error("an English one")
	}
}
