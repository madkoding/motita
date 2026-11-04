package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/localmodel"
)

// fakeLocal stands in for the manager: it answers what the test sets.
type fakeLocal struct {
	st                           localmodel.Status
	installErr, startErr, delErr error
	calls                        []string
}

func (f *fakeLocal) Status(context.Context) localmodel.Status { return f.st }
func (f *fakeLocal) Install() error {
	f.calls = append(f.calls, "install")
	return f.installErr
}

func (f *fakeLocal) Cancel() { f.calls = append(f.calls, "cancel") }
func (f *fakeLocal) Start(context.Context) error {
	f.calls = append(f.calls, "start")
	return f.startErr
}
func (f *fakeLocal) Stop() { f.calls = append(f.calls, "stop") }
func (f *fakeLocal) Delete() error {
	f.calls = append(f.calls, "delete")
	return f.delErr
}

func (f *fakeLocal) Close() { f.calls = append(f.calls, "close") }

// withLocal makes every server of the test use f, in a folder of the test's own.
func withLocal(t *testing.T, f *fakeLocal) {
	t.Helper()
	dir := t.TempDir()
	oldDir, oldNew := localDir, newLocalModel
	localDir = func() string { return dir }
	newLocalModel = func(string, func(localmodel.Ready) error) localModeler { return f }
	t.Cleanup(func() { localDir, newLocalModel = oldDir, oldNew })
}

func TestLocalModelEndpoints(t *testing.T) {
	f := &fakeLocal{st: localmodel.Status{ModelID: localmodel.ModelID, Eligible: true}}
	withLocal(t, f)
	srv, _ := modelsServer(t, llmEnv(t, openAIConfig))
	var st localmodel.Status
	w := get(t, srv, "/v1/local-model", testToken)
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.ModelID != localmodel.ModelID {
		t.Fatalf("status: %d %v", w.Code, err)
	}
	for _, c := range []struct {
		method, route string
		want          int
	}{
		{http.MethodPost, "/v1/local-model/install", http.StatusAccepted},
		{http.MethodPost, "/v1/local-model/cancel", http.StatusOK},
		{http.MethodPost, "/v1/local-model/start", http.StatusAccepted},
		{http.MethodPost, "/v1/local-model/stop", http.StatusOK},
		{http.MethodDelete, "/v1/local-model", http.StatusOK},
	} {
		if w := llmCall(t, srv, c.method, c.route, `{}`); w.Code != c.want {
			t.Errorf("%s %s: %d %s", c.method, c.route, w.Code, w.Body.String())
		}
	}
	if strings.Join(f.calls, ",") != "install,cancel,start,stop,delete" {
		t.Errorf("calls = %v", f.calls)
	}
	srv.closeLocalModel()
	if f.calls[len(f.calls)-1] != "close" {
		t.Error("the gateway's close stops the server")
	}
}

func TestLocalModelRefusals(t *testing.T) {
	f := &fakeLocal{st: localmodel.Status{Eligible: false}}
	withLocal(t, f)
	srv, _ := modelsServer(t, llmEnv(t, openAIConfig))
	refusal := func(method, route string) (int, string) {
		w := llmCall(t, srv, method, route, `{}`)
		var body struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Code
	}
	if c, why := refusal(http.MethodPost, "/v1/local-model/install"); c != http.StatusConflict || why != "not_eligible" {
		t.Errorf("install on a small GPU: %d %s", c, why)
	}
	f.st.Eligible, f.installErr = true, localmodel.ErrBusy
	if c, why := refusal(http.MethodPost, "/v1/local-model/install"); c != http.StatusConflict || why != "busy" {
		t.Errorf("busy install: %d %s", c, why)
	}
	for err, want := range map[error]string{
		localmodel.ErrNotEligible: "not_eligible", localmodel.ErrBusy: "busy", errors.New("x"): "start_failed",
	} {
		f.startErr = err
		if c, why := refusal(http.MethodPost, "/v1/local-model/start"); c != http.StatusConflict || why != want {
			t.Errorf("start %v: %d %s", err, c, why)
		}
	}
	f.delErr = errors.New("busy disk")
	if c, _ := refusal(http.MethodDelete, "/v1/local-model"); c != http.StatusInternalServerError {
		t.Errorf("delete: %d", c)
	}
}

func TestLocalModelWithoutAConfigurationFile(t *testing.T) {
	f := &fakeLocal{st: localmodel.Status{Eligible: true}}
	withLocal(t, f)
	srv, _ := modelsServer(t, "")
	if w := llmCall(t, srv, http.MethodPost, "/v1/local-model/start", `{}`); w.Code != http.StatusConflict {
		t.Errorf("start without a file: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/local-model", `{}`); w.Code != http.StatusOK {
		t.Errorf("delete without a file: %d %s", w.Code, w.Body.String())
	}
}

func TestLocalReadyConnectsTheProvider(t *testing.T) {
	withLocal(t, &fakeLocal{})
	path := llmEnv(t, openAIConfig)
	srv, _ := modelsServer(t, path)
	ready := localmodel.Ready{BaseURL: "http://127.0.0.1:8471/v1", APIKey: "motita-local-0123456789abcdef",
		Context: 8192}
	if err := srv.localReady(ready); err != nil {
		t.Fatal(err)
	}
	p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), config.ProviderLocal)
	if p.Key != config.KeyStored || p.BaseURL != "http://127.0.0.1:8471/v1" || len(p.Models) != 1 {
		t.Fatalf("local provider: %+v", p)
	}
	if got := (config.LLM{Provider: config.ProviderLocal}).WindowOverride(); got != 8192 {
		t.Errorf("window = %d", got)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/local-model", `{}`); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if p = providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), config.ProviderLocal); p.Key != config.KeyMissing {
		t.Errorf("deleted: %+v", p)
	}
}

func TestLocalReadyAndDeleteReportAFailedWrite(t *testing.T) {
	withLocal(t, &fakeLocal{})
	srv, _ := modelsServer(t, llmEnv(t, openAIConfig))
	old := setCredential
	setCredential = func(_, name, _ string) error {
		if name == config.LocalContextVariable {
			return errors.New("disk full")
		}
		return nil
	}
	defer func() { setCredential = old }()
	if err := srv.localReady(localmodel.Ready{BaseURL: "http://127.0.0.1:8471/v1", APIKey: "k", Context: 1}); err == nil {
		t.Error("a failed write is an error")
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/local-model", `{}`); w.Code != http.StatusInternalServerError {
		t.Errorf("delete: %d", w.Code)
	}
}

func TestTheRealManagerIsMadeOnFirstUse(t *testing.T) {
	dir := t.TempDir()
	old := localDir
	localDir = func() string { return dir }
	defer func() { localDir = old }()
	srv, _ := modelsServer(t, "")
	if w := get(t, srv, "/v1/local-model", testToken); w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	srv.closeLocalModel()
}
