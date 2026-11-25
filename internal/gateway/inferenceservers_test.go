package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/inference"
)

func inferenceCall(t *testing.T, srv *Server, method, path, body string) (int, inferenceList, string) {
	t.Helper()
	w := llmCall(t, srv, method, path, body)
	var v inferenceList
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return w.Code, v, w.Body.String()
}

const (
	infPath = "/v1/inference-servers"
	infKey  = "tok_abcdefghijklmnopqrstuv1234"
	infVar  = "MOTITA_INFERENCE_MI_GPU_API_KEY"
)

// addedGPU adds the server the next tests work on and returns the answer's raw body.
func addedGPU(t *testing.T, srv *Server) (inferenceView, string) {
	t.Helper()
	code, v, raw := inferenceCall(t, srv, http.MethodPost, infPath,
		`{"name":"Mi GPU","kind":"llamacpp","model":" qwen ","api_key":"`+infKey+`"}`)
	if code != http.StatusCreated || len(v.Servers) != 1 {
		t.Fatalf("add: %d %s", code, raw)
	}
	return v.Servers[0], raw
}

func TestInferenceServersAreAddedAndNeverReturnTheirKey(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)

	code, v, raw := inferenceCall(t, srv, http.MethodGet, infPath, "")
	if code != http.StatusOK || !v.CanSave || len(v.Servers) != 0 {
		t.Fatalf("empty: %d %s", code, raw)
	}
	if len(v.Kinds) != len(inference.Kinds) || v.Max != inference.MaxServers {
		t.Errorf("kinds and limit: %s", raw)
	}

	got, raw := addedGPU(t, srv)
	if got.ID != "mi-gpu" || got.BaseURL != "http://127.0.0.1:8080/v1" || got.Model != "qwen" {
		t.Errorf("added = %+v", got)
	}
	if got.Provider != "custom" || !got.HasKey || got.KeyHint != "…1234" {
		t.Errorf("added key = %+v", got)
	}
	if strings.Contains(raw, infKey) {
		t.Fatalf("the key was returned: %s", raw)
	}
	if config.StoredCredentials(path)[infVar] != infKey {
		t.Errorf("key not stored")
	}
}

func TestAnInferenceServerEditKeepsWhatItDoesNotName(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	addedGPU(t, srv)

	code, v, raw := inferenceCall(t, srv, http.MethodPut, infPath+"/mi-gpu",
		`{"name":"Mi GPU 2","kind":"ollama","base_url":"http://192.168.1.5:11434/v1/"}`)
	if code != http.StatusOK {
		t.Fatalf("edit: %d %s", code, raw)
	}
	got := v.Servers[0]
	if got.Name != "Mi GPU 2" || got.ID != "mi-gpu" || got.Provider != "ollama" {
		t.Errorf("edited = %+v", got)
	}
	if got.BaseURL != "http://192.168.1.5:11434/v1" || !got.HasKey {
		t.Errorf("edited address or key = %+v", got)
	}
}

// fixedClock makes the probe take 12 ms.
func fixedClock(t *testing.T) {
	t.Helper()
	old := probeNow
	calls := 0
	probeNow = func() time.Time {
		calls++
		if calls%2 == 1 {
			return time.Unix(0, 0)
		}
		return time.Unix(0, int64(12*time.Millisecond))
	}
	t.Cleanup(func() { probeNow = old })
}

// probeWith runs a probe against a fake model listing and returns what the provider was handed.
func probeWith(
	t *testing.T, srv *Server, models []string, fail error,
) (*httptest.ResponseRecorder, probeResult, config.LLM) {
	t.Helper()
	var seen config.LLM
	old := listProviderModels
	listProviderModels = func(_ context.Context, l config.LLM) ([]string, error) {
		seen = l
		return models, fail
	}
	t.Cleanup(func() { listProviderModels = old })
	w := llmCall(t, srv, http.MethodPost, infPath+"/mi-gpu/probe", "")
	var p probeResult
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return w, p, seen
}

func TestAnInferenceServerProbeSendsTheStoredKeyToItsProvider(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	addedGPU(t, srv)
	inferenceCall(t, srv, http.MethodPut, infPath+"/mi-gpu",
		`{"kind":"ollama","base_url":"http://192.168.1.5:11434/v1"}`)
	fixedClock(t)

	_, p, seen := probeWith(t, srv, []string{"b", "a"}, nil)
	if !p.OK || strings.Join(p.Models, ",") != "a,b" || p.LatencyMS != 12 {
		t.Errorf("probe = %+v", p)
	}
	if seen.Provider != "ollama" || seen.APIKey != infKey || seen.BaseURL != "http://192.168.1.5:11434/v1" {
		t.Errorf("handed to the provider = %+v", seen)
	}
}

func TestAFailingInferenceServerIsA200WhoseBodySaysWhy(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	addedGPU(t, srv)
	inferenceCall(t, srv, http.MethodPut, infPath+"/mi-gpu", `{"api_key":""}`)

	w, p, seen := probeWith(t, srv, nil, errors.New("connection refused"))
	if w.Code != http.StatusOK || p.OK || !strings.Contains(p.Error, "refused") {
		t.Errorf("failed probe: %d %s", w.Code, w.Body)
	}
	if seen.APIKey != "none" {
		t.Errorf("no key sends a placeholder, got %q", seen.APIKey)
	}
	if _, ok := config.StoredCredentials(path)[infVar]; ok {
		t.Error("the key was kept after removing it")
	}
}

func TestRemovingAnInferenceServerRemovesItsKey(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	addedGPU(t, srv)

	code, v, raw := inferenceCall(t, srv, http.MethodDelete, infPath+"/mi-gpu", "")
	if code != http.StatusOK || len(v.Servers) != 0 {
		t.Fatalf("delete: %d %s", code, raw)
	}
	if _, ok := config.StoredCredentials(path)[infVar]; ok {
		t.Error("the key outlived the server")
	}
}

func TestInferenceServersRefuseBadRequests(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	inferenceCall(t, srv, http.MethodPost, infPath, `{"name":"a","kind":"openai","base_url":"https://x.test/v1"}`)

	const keyOnPlainHTTP = `{"name":"b","kind":"openai","base_url":"http://example.com/v1","api_key":"k"}`
	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, infPath, `{`, http.StatusBadRequest},
		{http.MethodPost, infPath, `{"name":"","kind":"ollama"}`, http.StatusBadRequest},
		{http.MethodPost, infPath, `{"name":"b","kind":"openai"}`, http.StatusBadRequest},
		{http.MethodPost, infPath, `{"name":"b","kind":"ollama","base_url":"ftp://x"}`, http.StatusBadRequest},
		{http.MethodPost, infPath, `{"name":"b","kind":"ollama","api_key":"a b"}`, http.StatusBadRequest},
		{http.MethodPost, infPath, keyOnPlainHTTP, http.StatusBadRequest},
		{http.MethodPost, infPath, `{"name":"A","kind":"ollama"}`, http.StatusConflict},
		{http.MethodPut, infPath + "/a", `{`, http.StatusBadRequest},
		{http.MethodPut, infPath + "/A_", `{}`, http.StatusNotFound},
		{http.MethodPut, infPath + "/zzz", `{}`, http.StatusNotFound},
		{http.MethodPut, infPath + "/a", `{"kind":"nope"}`, http.StatusBadRequest},
		{http.MethodPut, infPath + "/a", `{"api_key":"a b"}`, http.StatusBadRequest},
		{http.MethodPut, infPath + "/a", `{"api_key":"k","base_url":"http://example.com/v1"}`, http.StatusBadRequest},
		{http.MethodDelete, infPath + "/A_", "", http.StatusNotFound},
		{http.MethodDelete, infPath + "/zzz", "", http.StatusNotFound},
		{http.MethodPost, infPath + "/A_/probe", "", http.StatusNotFound},
		{http.MethodPost, infPath + "/zzz/probe", "", http.StatusNotFound},
	}
	for _, c := range cases {
		if w := llmCall(t, srv, c.method, c.path, c.body); w.Code != c.want {
			t.Errorf("%s %s %s: %d %s, want %d", c.method, c.path, c.body, w.Code, w.Body, c.want)
		}
	}

	// A key kept for a server cannot follow it to plain http outside the network.
	inferenceCall(t, srv, http.MethodPut, infPath+"/a", `{"api_key":"k"}`)
	code, _, raw := inferenceCall(t, srv, http.MethodPut, infPath+"/a", `{"base_url":"http://example.com/v1"}`)
	if code != http.StatusBadRequest {
		t.Errorf("a kept key moved to plain http: %d %s", code, raw)
	}
}

func TestInferenceServersNeedAConfigurationFile(t *testing.T) {
	srv := newTestServer(t, &refreshingService{})
	for _, c := range [][3]string{
		{http.MethodPost, infPath, `{"name":"a","kind":"ollama"}`},
		{http.MethodPut, infPath + "/a", `{}`},
		{http.MethodDelete, infPath + "/a", ""},
	} {
		if w := llmCall(t, srv, c[0], c[1], c[2]); w.Code != http.StatusConflict {
			t.Errorf("%v: %d %s", c, w.Code, w.Body)
		}
	}
	code, v, raw := inferenceCall(t, srv, http.MethodGet, infPath, "")
	if code != http.StatusOK || v.CanSave {
		t.Errorf("list: %d %s", code, raw)
	}
}

func TestInferenceServersReportFailures(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	inferenceCall(t, srv, http.MethodPost, infPath, `{"name":"a","kind":"ollama"}`)

	// A list that does not parse is an error everywhere it is read, and is never overwritten.
	store := inference.PathBeside(path)
	good, _ := os.ReadFile(store)
	_ = os.WriteFile(store, []byte("{nope"), 0o600)
	for _, c := range [][3]string{
		{http.MethodGet, infPath, ""},
		{http.MethodPost, infPath, `{"name":"b","kind":"ollama"}`},
		{http.MethodPut, infPath + "/a", `{}`},
		{http.MethodDelete, infPath + "/a", ""},
		{http.MethodPost, infPath + "/a/probe", ""},
	} {
		if w := llmCall(t, srv, c[0], c[1], c[2]); w.Code != http.StatusInternalServerError {
			t.Errorf("%v: %d %s", c, w.Code, w.Body)
		}
	}
	_ = os.WriteFile(store, good, 0o600)

	oldUpdate := updateInference
	updateInference = func(*inference.Store, string, inference.Server) (inference.Server, error) {
		return inference.Server{}, errors.New("disk full")
	}
	if w := llmCall(t, srv, http.MethodPut, infPath+"/a", `{"model":"x"}`); w.Code != http.StatusInternalServerError {
		t.Errorf("edit write: %d", w.Code)
	}
	updateInference = oldUpdate

	// The key write failing: an add takes the entry back, an edit and a delete say so.
	old := setCredential
	setCredential = func(string, string, string) error { return errors.New("disk full") }
	defer func() { setCredential = old }()
	w := llmCall(t, srv, http.MethodPost, infPath, `{"name":"b","kind":"ollama","api_key":"k"}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("add: %d", w.Code)
	}
	if code, v, _ := inferenceCall(t, srv, http.MethodGet, infPath, ""); code != http.StatusOK || len(v.Servers) != 1 {
		t.Errorf("a failed add left its entry: %+v", v)
	}
	if w := llmCall(t, srv, http.MethodPut, infPath+"/a", `{"api_key":"k"}`); w.Code != http.StatusInternalServerError {
		t.Errorf("edit: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodDelete, infPath+"/a", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("delete: %d", w.Code)
	}
}
