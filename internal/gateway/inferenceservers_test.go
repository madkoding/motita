package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

func TestInferenceServersAreAddedEditedProbedAndRemoved(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	long := "tok_abcdefghijklmnopqrstuv1234"

	code, v, raw := inferenceCall(t, srv, http.MethodGet, "/v1/inference-servers", "")
	if code != http.StatusOK || !v.CanSave || len(v.Servers) != 0 || len(v.Kinds) != len(inference.Kinds) || v.Max != inference.MaxServers {
		t.Fatalf("empty: %d %s", code, raw)
	}

	code, v, raw = inferenceCall(t, srv, http.MethodPost, "/v1/inference-servers",
		`{"name":"Mi GPU","kind":"llamacpp","model":" qwen ","api_key":"`+long+`"}`)
	if code != http.StatusCreated || len(v.Servers) != 1 {
		t.Fatalf("add: %d %s", code, raw)
	}
	got := v.Servers[0]
	if got.ID != "mi-gpu" || got.BaseURL != "http://127.0.0.1:8080/v1" || got.Model != "qwen" || got.Provider != "custom" ||
		!got.HasKey || got.KeyHint != "…1234" {
		t.Errorf("added = %+v", got)
	}
	if strings.Contains(raw, long) {
		t.Fatalf("the key was returned: %s", raw)
	}
	if config.StoredCredentials(path)["MOTITA_INFERENCE_MI_GPU_API_KEY"] != long {
		t.Errorf("key not stored")
	}

	// An edit changes what it names and keeps the rest, key included.
	code, v, raw = inferenceCall(t, srv, http.MethodPut, "/v1/inference-servers/mi-gpu",
		`{"name":"Mi GPU 2","kind":"ollama","base_url":"http://192.168.1.5:11434/v1/"}`)
	if code != http.StatusOK || v.Servers[0].Name != "Mi GPU 2" || v.Servers[0].ID != "mi-gpu" ||
		v.Servers[0].Provider != "ollama" || v.Servers[0].BaseURL != "http://192.168.1.5:11434/v1" || !v.Servers[0].HasKey {
		t.Fatalf("edit: %d %s", code, raw)
	}

	// The probe sends the stored key to the kind's provider and answers with what it found.
	var seen config.LLM
	old := listProviderModels
	listProviderModels = func(_ context.Context, l config.LLM) ([]string, error) {
		seen = l
		return []string{"b", "a"}, nil
	}
	defer func() { listProviderModels = old }()
	clock := []time.Time{time.Unix(0, 0), time.Unix(0, int64(12*time.Millisecond))}
	oldNow := probeNow
	probeNow = func() time.Time { t0 := clock[0]; clock = append(clock[1:], clock[0]); return t0 }
	defer func() { probeNow = oldNow }()
	w := llmCall(t, srv, http.MethodPost, "/v1/inference-servers/mi-gpu/probe", "")
	var p probeResult
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if !p.OK || strings.Join(p.Models, ",") != "a,b" || p.LatencyMS != 12 || seen.Provider != "ollama" ||
		seen.APIKey != long || seen.BaseURL != "http://192.168.1.5:11434/v1" {
		t.Errorf("probe: %s %+v", w.Body, seen)
	}

	// A failing server is a 200 whose body says why; no key sends a placeholder.
	listProviderModels = func(_ context.Context, l config.LLM) ([]string, error) {
		seen = l
		return nil, errors.New("connection refused")
	}
	inferenceCall(t, srv, http.MethodPut, "/v1/inference-servers/mi-gpu", `{"api_key":""}`)
	w = llmCall(t, srv, http.MethodPost, "/v1/inference-servers/mi-gpu/probe", "")
	p = probeResult{}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != http.StatusOK || p.OK || !strings.Contains(p.Error, "refused") || seen.APIKey != "none" {
		t.Errorf("failed probe: %d %s %+v", w.Code, w.Body, seen)
	}
	if _, ok := config.StoredCredentials(path)["MOTITA_INFERENCE_MI_GPU_API_KEY"]; ok {
		t.Error("the key was kept after removing it")
	}

	// Keep a key, then remove the server: the key goes with it.
	inferenceCall(t, srv, http.MethodPut, "/v1/inference-servers/mi-gpu", `{"api_key":"abc"}`)
	code, v, raw = inferenceCall(t, srv, http.MethodDelete, "/v1/inference-servers/mi-gpu", "")
	if code != http.StatusOK || len(v.Servers) != 0 {
		t.Fatalf("delete: %d %s", code, raw)
	}
	if _, ok := config.StoredCredentials(path)["MOTITA_INFERENCE_MI_GPU_API_KEY"]; ok {
		t.Error("the key outlived the server")
	}
}

func TestInferenceServersRefuseBadRequests(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	inferenceCall(t, srv, http.MethodPost, "/v1/inference-servers", `{"name":"a","kind":"openai","base_url":"https://x.test/v1"}`)

	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/v1/inference-servers", `{`, http.StatusBadRequest},
		{http.MethodPost, "/v1/inference-servers", `{"name":"","kind":"ollama"}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/inference-servers", `{"name":"b","kind":"openai"}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/inference-servers", `{"name":"b","kind":"ollama","base_url":"ftp://x"}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/inference-servers", `{"name":"b","kind":"ollama","api_key":"a b"}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/inference-servers", `{"name":"b","kind":"openai","base_url":"http://example.com/v1","api_key":"k"}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/inference-servers", `{"name":"A","kind":"ollama"}`, http.StatusConflict},
		{http.MethodPut, "/v1/inference-servers/a", `{`, http.StatusBadRequest},
		{http.MethodPut, "/v1/inference-servers/A_", `{}`, http.StatusNotFound},
		{http.MethodPut, "/v1/inference-servers/zzz", `{}`, http.StatusNotFound},
		{http.MethodPut, "/v1/inference-servers/a", `{"kind":"nope"}`, http.StatusBadRequest},
		{http.MethodPut, "/v1/inference-servers/a", `{"api_key":"a b"}`, http.StatusBadRequest},
		{http.MethodPut, "/v1/inference-servers/a", `{"api_key":"k","base_url":"http://example.com/v1"}`, http.StatusBadRequest},
		{http.MethodDelete, "/v1/inference-servers/A_", "", http.StatusNotFound},
		{http.MethodDelete, "/v1/inference-servers/zzz", "", http.StatusNotFound},
		{http.MethodPost, "/v1/inference-servers/A_/probe", "", http.StatusNotFound},
		{http.MethodPost, "/v1/inference-servers/zzz/probe", "", http.StatusNotFound},
	}
	for _, c := range cases {
		if w := llmCall(t, srv, c.method, c.path, c.body); w.Code != c.want {
			t.Errorf("%s %s %s: %d %s, want %d", c.method, c.path, c.body, w.Code, w.Body, c.want)
		}
	}

	// A key kept for a server cannot follow it to plain http outside the network.
	inferenceCall(t, srv, http.MethodPut, "/v1/inference-servers/a", `{"api_key":"k"}`)
	if code, _, raw := inferenceCall(t, srv, http.MethodPut, "/v1/inference-servers/a", `{"base_url":"http://example.com/v1"}`); code != http.StatusBadRequest {
		t.Errorf("a kept key moved to plain http: %d %s", code, raw)
	}
}

func TestInferenceServersNeedAConfigurationFile(t *testing.T) {
	srv := newTestServer(t, &refreshingService{})
	for _, c := range [][3]string{
		{http.MethodPost, "/v1/inference-servers", `{"name":"a","kind":"ollama"}`},
		{http.MethodPut, "/v1/inference-servers/a", `{}`},
		{http.MethodDelete, "/v1/inference-servers/a", ""},
	} {
		if w := llmCall(t, srv, c[0], c[1], c[2]); w.Code != http.StatusConflict {
			t.Errorf("%v: %d %s", c, w.Code, w.Body)
		}
	}
	code, v, raw := inferenceCall(t, srv, http.MethodGet, "/v1/inference-servers", "")
	if code != http.StatusOK || v.CanSave {
		t.Errorf("list: %d %s", code, raw)
	}
}

func TestInferenceServersReportFailures(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	inferenceCall(t, srv, http.MethodPost, "/v1/inference-servers", `{"name":"a","kind":"ollama"}`)

	// A list that does not parse is an error everywhere it is read, and is never overwritten.
	store := inference.PathBeside(path)
	good, _ := os.ReadFile(store)
	_ = os.WriteFile(store, []byte("{nope"), 0o600)
	for _, c := range [][3]string{
		{http.MethodGet, "/v1/inference-servers", ""},
		{http.MethodPost, "/v1/inference-servers", `{"name":"b","kind":"ollama"}`},
		{http.MethodPut, "/v1/inference-servers/a", `{}`},
		{http.MethodDelete, "/v1/inference-servers/a", ""},
		{http.MethodPost, "/v1/inference-servers/a/probe", ""},
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
	if w := llmCall(t, srv, http.MethodPut, "/v1/inference-servers/a", `{"model":"x"}`); w.Code != http.StatusInternalServerError {
		t.Errorf("edit write: %d", w.Code)
	}
	updateInference = oldUpdate

	// The key write failing: an add takes the entry back, an edit and a delete say so.
	old := setCredential
	setCredential = func(string, string, string) error { return errors.New("disk full") }
	defer func() { setCredential = old }()
	if w := llmCall(t, srv, http.MethodPost, "/v1/inference-servers", `{"name":"b","kind":"ollama","api_key":"k"}`); w.Code != http.StatusInternalServerError {
		t.Errorf("add: %d", w.Code)
	}
	if code, v, _ := inferenceCall(t, srv, http.MethodGet, "/v1/inference-servers", ""); code != http.StatusOK || len(v.Servers) != 1 {
		t.Errorf("a failed add left its entry: %+v", v)
	}
	if w := llmCall(t, srv, http.MethodPut, "/v1/inference-servers/a", `{"api_key":"k"}`); w.Code != http.StatusInternalServerError {
		t.Errorf("edit: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/inference-servers/a", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("delete: %d", w.Code)
	}
}

func TestAnInferenceServerIsUsedAsTheEndpointOfItsProvider(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, svc := modelsServer(t, path)
	inferenceCall(t, srv, http.MethodPost, "/v1/inference-servers",
		`{"name":"Box","kind":"llamacpp","base_url":"http://10.0.0.5:8080/v1","model":"qwen"}`)
	inferenceCall(t, srv, http.MethodPost, "/v1/inference-servers",
		`{"name":"Oll","kind":"ollama","base_url":"http://10.0.0.6:11434/v1","api_key":"okey"}`)

	_, v, _ := inferenceCall(t, srv, http.MethodGet, "/v1/inference-servers", "")
	if v.Servers[0].InUse || v.Servers[1].InUse {
		t.Fatalf("nothing is in use yet: %+v", v.Servers)
	}

	// A keyless server gets the placeholder token the custom provider always sends, and its model
	// becomes the custom model.
	code, v, raw := inferenceCall(t, srv, http.MethodPost, "/v1/inference-servers/box/use", "")
	if code != http.StatusOK || !v.Servers[0].InUse || v.Servers[1].InUse {
		t.Fatalf("use: %d %s", code, raw)
	}
	creds := config.StoredCredentials(path)
	if creds["MOTITA_CUSTOM_BASE_URL"] != "http://10.0.0.5:8080/v1" || creds["MOTITA_CUSTOM_API_KEY"] != "none" {
		t.Errorf("credentials = %v", creds)
	}
	if len(svc.custom) != 1 || svc.custom[0].Model != "qwen" {
		t.Errorf("custom model = %+v", svc.custom)
	}
	if len(svc.refreshed) == 0 || !strings.HasPrefix(svc.refreshed[len(svc.refreshed)-1], "custom|none|") {
		t.Errorf("refreshed = %v", svc.refreshed)
	}

	// An Ollama server keeps its own key and sets no custom model.
	code, v, raw = inferenceCall(t, srv, http.MethodPost, "/v1/inference-servers/oll/use", "")
	creds = config.StoredCredentials(path)
	if code != http.StatusOK || !v.Servers[1].InUse || creds["MOTITA_OLLAMA_BASE_URL"] != "http://10.0.0.6:11434/v1" ||
		creds["MOTITA_OLLAMA_API_KEY"] != "okey" || len(svc.custom) != 1 {
		t.Errorf("ollama: %d %s %v", code, raw, creds)
	}
}

func TestTheManagedLocalModelIsListedReadOnly(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	_, v, _ := inferenceCall(t, srv, http.MethodGet, "/v1/inference-servers", "")
	if len(v.Servers) != 0 {
		t.Fatalf("no local model, no entry: %+v", v.Servers)
	}
	for _, kv := range [][2]string{{"MOTITA_LOCAL_BASE_URL", "http://127.0.0.1:8471/v1"}, {"MOTITA_LOCAL_API_KEY", "k"}} {
		if err := config.SetCredential(path, kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	_, v, _ = inferenceCall(t, srv, http.MethodGet, "/v1/inference-servers", "")
	if len(v.Servers) != 1 || !v.Servers[0].Managed || v.Servers[0].ID != managedLocalID || v.Servers[0].HasKey ||
		v.Servers[0].Provider != "local" || v.Servers[0].InUse {
		t.Fatalf("managed = %+v", v.Servers)
	}
	for _, m := range []string{"/v1/inference-servers/motita-local/use", "/v1/inference-servers/motita-local/probe"} {
		if w := llmCall(t, srv, http.MethodPost, m, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", m, w.Code)
		}
	}
	// Running on it, the entry says so.
	if err := config.SetLLMDefault(path, "local", "m", "http://127.0.0.1:8471/v1"); err != nil {
		t.Fatal(err)
	}
	_, v, _ = inferenceCall(t, srv, http.MethodGet, "/v1/inference-servers", "")
	if !v.Servers[0].InUse {
		t.Errorf("the default provider's own address is in use: %+v", v.Servers[0])
	}
}

func TestUsingAServerRefusesBadRequests(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := modelsServer(t, path)
	inferenceCall(t, srv, http.MethodPost, "/v1/inference-servers",
		`{"name":"Box","kind":"llamacpp","model":"qwen"}`)
	for p, want := range map[string]int{
		"/v1/inference-servers/A_/use":  http.StatusNotFound,
		"/v1/inference-servers/zzz/use": http.StatusNotFound,
	} {
		if w := llmCall(t, srv, http.MethodPost, p, ""); w.Code != want {
			t.Errorf("%s: %d", p, w.Code)
		}
	}

	old := setCredential
	setCredential = func(string, string, string) error { return errors.New("disk full") }
	w := llmCall(t, srv, http.MethodPost, "/v1/inference-servers/box/use", "")
	setCredential = old
	if w.Code != http.StatusInternalServerError {
		t.Errorf("credential write: %d", w.Code)
	}
	oldCustom := setCustom
	setCustom = func(string, config.CustomModel) error { return errors.New("disk full") }
	w = llmCall(t, srv, http.MethodPost, "/v1/inference-servers/box/use", "")
	setCustom = oldCustom
	if w.Code != http.StatusInternalServerError {
		t.Errorf("custom write: %d", w.Code)
	}

	// A broken list is an error, and without a file there is nowhere to save.
	_ = os.WriteFile(inference.PathBeside(path), []byte("{nope"), 0o600)
	if w := llmCall(t, srv, http.MethodPost, "/v1/inference-servers/box/use", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("broken list: %d", w.Code)
	}
	bare := newTestServer(t, &refreshingService{})
	if w := llmCall(t, bare, http.MethodPost, "/v1/inference-servers/box/use", ""); w.Code != http.StatusConflict {
		t.Errorf("no file: %d", w.Code)
	}
}
