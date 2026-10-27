package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// modelsService is the fake that also takes a models block, like the production runner.
type modelsService struct {
	refreshingService
	models []config.Models
	think  []config.Thinking
	custom []config.CustomModel
}

func (m *modelsService) SetTaskThinking(t config.Thinking) { m.think = append(m.think, t) }

func (m *modelsService) SetTaskModels(ms config.Models) { m.models = append(m.models, ms) }

func modelsServer(t *testing.T, path string) (*Server, *modelsService) {
	t.Helper()
	svc := &modelsService{}
	svc.cfg = config.Default()
	svc.cfg.LLM.Provider, svc.cfg.LLM.Model = "openai", "gpt-5-mini"
	srv := newTestServer(t, svc, func(o *Options) {
		o.ConfigPath = path
		o.NewService = func() (Service, error) {
			n := &modelsService{}
			n.cfg = config.Default()
			return n, nil
		}
	})
	return srv, svc
}

func TestLLMTasksAreSavedAndReachEverySession(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	if err := config.SetCredential(path, "ANTHROPIC_API_KEY", "sk-ant"); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCredential(path, "MOTITA_LLM_API_KEY", "sk-openai"); err != nil {
		t.Fatal(err)
	}
	srv, svc := modelsServer(t, path)
	v := decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if len(v.Kinds) != len(config.TaskKinds) || len(v.Tasks) != 0 {
		t.Fatalf("no task has a model yet: %+v %+v", v.Kinds, v.Tasks)
	}

	w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks",
		`{"tasks":{"plan":{"provider":"anthropic","model":"claude-opus-5-5"},`+
			`"summary":{"provider":"openai","model":"gpt-5-nano"},"code":{"provider":"","model":""}}}`)
	v = decodeLLM(t, w)
	if w.Code != http.StatusOK || v.Tasks["plan"].Model != "claude-opus-5-5" || v.Tasks["summary"].Provider != "openai" {
		t.Fatalf("save: %d %+v", w.Code, v.Tasks)
	}
	if _, ok := v.Tasks["code"]; ok {
		t.Errorf("an empty kind follows the default: %+v", v.Tasks)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Models.Plan != "anthropic/claude-opus-5-5" || cfg.Models.Code != "" {
		t.Errorf("the file holds the models block: %v %+v", err, cfg.Models)
	}
	if len(svc.models) != 1 || svc.models[0].Summary != "openai/gpt-5-nano" {
		t.Errorf("the open session takes the new set: %+v", svc.models)
	}

	w = llmCall(t, srv, http.MethodPost, "/v1/sessions", `{}`)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	conv := srv.sessions[created.ID]
	if conv == nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if got := conv.svc.(*modelsService).models; len(got) != 1 || got[0].Plan != "anthropic/claude-opus-5-5" {
		t.Errorf("a new session starts with the set: %+v", got)
	}

	// The models a test listed are drawn with the provider.
	srv.rememberModels("openai", []string{"gpt-x"})
	if p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), "openai"); len(p.Listed) != 1 {
		t.Errorf("listed models: %+v", p)
	}
}

func TestLLMTasksRefusals(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := modelsServer(t, path)
	for body, want := range map[string]int{
		`not json`: http.StatusBadRequest,
		`{"tasks":{"narrate":{"provider":"openai","model":"m"}}}`: http.StatusBadRequest,
		`{"tasks":{"plan":{"provider":"nope","model":"m"}}}`:      http.StatusBadRequest,
		`{"tasks":{"plan":{"provider":"openai","model":""}}}`:     http.StatusBadRequest,
		`{"tasks":{"plan":{"provider":"","model":"m"}}}`:          http.StatusBadRequest,
		`{"tasks":{"plan":{"provider":"gemini","model":"m"}}}`:    http.StatusConflict,
	} {
		if w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks", body); w.Code != want {
			t.Errorf("%s: %d, want %d (%s)", body, w.Code, want, w.Body.String())
		}
	}

	old := setTaskModels
	setTaskModels = func(string, map[string]config.ModelChoice) error { return errors.New("disk full") }
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks", `{"tasks":{}}`)
	setTaskModels = old
	if w.Code != http.StatusInternalServerError {
		t.Errorf("a failed write: %d %s", w.Code, w.Body.String())
	}
}

func TestLLMTasksNeedAFile(t *testing.T) {
	llmEnv(t, "")
	svc := &modelsService{}
	svc.cfg = config.Default()
	svc.cfg.Models.Review = "openai/gpt-5-mini"
	srv := newTestServer(t, svc)
	v := decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if v.Tasks["review"].Model != "gpt-5-mini" {
		t.Errorf("without a file, the session's own set answers: %+v", v.Tasks)
	}
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks", `{"tasks":{}}`); w.Code != http.StatusConflict {
		t.Errorf("no file to save in: %d", w.Code)
	}
}

func TestLLMThinkingLevelsAreSavedAndReachEverySession(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, svc := modelsServer(t, path)
	v := decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if len(v.Levels) != 4 || len(v.Thinking) != 1 || v.Thinking["summary"] != "off" {
		t.Fatalf("only the summary kind has a level by default: %+v %+v", v.Levels, v.Thinking)
	}
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks",
		`{"tasks":{},"thinking":{"plan":"high","summary":"off","code":""}}`)
	v = decodeLLM(t, w)
	if w.Code != http.StatusOK || v.Thinking["plan"] != "high" || v.Thinking["summary"] != "off" {
		t.Fatalf("save: %d %+v", w.Code, v.Thinking)
	}
	if _, ok := v.Thinking["code"]; ok {
		t.Errorf("an empty kind follows the session: %+v", v.Thinking)
	}
	if len(svc.think) != 1 || svc.think[0].Plan != "high" {
		t.Errorf("the open session takes the new set: %+v", svc.think)
	}
	// A body without "thinking" leaves the levels alone.
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks", `{"tasks":{}}`); w.Code != http.StatusOK ||
		decodeLLM(t, w).Thinking["plan"] != "high" {
		t.Errorf("levels kept: %d %s", w.Code, w.Body.String())
	}
	w = llmCall(t, srv, http.MethodPost, "/v1/sessions", `{}`)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	conv := srv.sessions[created.ID]
	if conv == nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if got := conv.svc.(*modelsService).think; len(got) != 1 || got[0].Plan != "high" {
		t.Errorf("a new session starts with the set: %+v", got)
	}
}

func TestLLMThinkingRefusals(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := modelsServer(t, path)
	for _, body := range []string{
		`{"thinking":{"narrate":"low"}}`,
		`{"thinking":{"plan":"max"}}`,
	} {
		if w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	old := setTaskThink
	setTaskThink = func(string, map[string]string) error { return errors.New("disk full") }
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks", `{"tasks":{},"thinking":{}}`)
	setTaskThink = old
	if w.Code != http.StatusInternalServerError {
		t.Errorf("a failed write: %d %s", w.Code, w.Body.String())
	}
}
