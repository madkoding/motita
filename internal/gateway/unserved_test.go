package gateway

import (
	"context"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/localmodel"
)

func TestSessionsOnALocalModelThatIsNotServedAreFlagged(t *testing.T) {
	f := &fakeLocal{st: localmodel.Status{}}
	withLocal(t, f)
	srv, _ := modelsServer(t, llmEnv(t, openAIConfig))
	list := []SessionStatus{{ID: "a", Provider: config.ProviderLocal}, {ID: "b", Provider: "openai"}}
	srv.markUnserved(context.Background(), list)
	if !list[0].ModelUnserved || list[1].ModelUnserved {
		t.Fatalf("only the local session with no server is flagged: %+v", list)
	}
	f.st.Server.Running = true
	list = []SessionStatus{{ID: "a", Provider: config.ProviderLocal}}
	srv.markUnserved(context.Background(), list)
	if list[0].ModelUnserved {
		t.Error("a running server serves it")
	}
	// No session on the local model: the manager is not even asked.
	srv.markUnserved(context.Background(), []SessionStatus{{ID: "b", Provider: "openai"}})
}
