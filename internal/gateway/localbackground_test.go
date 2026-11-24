package gateway

import (
	"errors"
	"net/http"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/localmodel"
)

var testReady = localmodel.Ready{BaseURL: "http://127.0.0.1:8471/v1", APIKey: "motita-local-0123456789abcdef", Context: 8192}

func TestLocalReadyGivesTheSmallCallsToTheLocalModel(t *testing.T) {
	withLocal(t, &fakeLocal{})
	srv, _ := modelsServer(t, llmEnv(t, openAIConfig))
	if err := srv.localReady(testReady); err != nil {
		t.Fatal(err)
	}
	if got := srv.configuredModels().For(config.TaskSummary); got != localSummaryChoice() {
		t.Fatalf("summary = %+v", got)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/local-model", `{}`); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if got := srv.configuredModels().For(config.TaskSummary); !got.Empty() {
		t.Errorf("deleting the model gives the summary back to the default: %+v", got)
	}
}

func TestLocalReadyKeepsAModelThePersonChose(t *testing.T) {
	withLocal(t, &fakeLocal{})
	srv, _ := modelsServer(t, llmEnv(t, openAIConfig))
	chosen := config.ModelChoice{Provider: "openai", Model: "gpt-5-nano"}
	if err := srv.writeSummaryModel(chosen); err != nil {
		t.Fatal(err)
	}
	if err := srv.localReady(testReady); err != nil {
		t.Fatal(err)
	}
	if got := srv.configuredModels().For(config.TaskSummary); got != chosen {
		t.Fatalf("a chosen summary model stays: %+v", got)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/local-model", `{}`); w.Code != http.StatusOK {
		t.Fatalf("delete: %d", w.Code)
	}
	if got := srv.configuredModels().For(config.TaskSummary); got != chosen {
		t.Errorf("and so does it after the delete: %+v", got)
	}
}

func TestTheSummaryModelWriteFailsLoudly(t *testing.T) {
	withLocal(t, &fakeLocal{})
	srv, _ := modelsServer(t, llmEnv(t, openAIConfig))
	old := setTaskModels
	setTaskModels = func(string, map[string]config.ModelChoice) error { return errors.New("disk full") }
	defer func() { setTaskModels = old }()
	if err := srv.localReady(testReady); err == nil {
		t.Error("a failed write is an error")
	}
	setTaskModels = old
	if err := srv.writeSummaryModel(localSummaryChoice()); err != nil {
		t.Fatal(err)
	}
	setTaskModels = func(string, map[string]config.ModelChoice) error { return errors.New("disk full") }
	if w := llmCall(t, srv, http.MethodDelete, "/v1/local-model", `{}`); w.Code != http.StatusInternalServerError {
		t.Errorf("delete: %d", w.Code)
	}
}
