package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeCodeIndex(t *testing.T, w *httptest.ResponseRecorder) codeIndexView {
	t.Helper()
	var v codeIndexView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func TestTheCodeIndexIsReadFromTheFile(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, "llm:\n  provider: openai\n") })
	v := decodeCodeIndex(t, get(t, srv, "/v1/code-index", testToken))
	if !v.Enabled || !v.Semantic || !v.CanSave || v.Embeddings != "auto" || v.EndpointLocal || v.MaxFileKB != 256 {
		t.Errorf("default = %+v", v)
	}

	noFile := newTestServer(t, &fakeService{})
	if v := decodeCodeIndex(t, get(t, noFile, "/v1/code-index", testToken)); v.CanSave {
		t.Errorf("no file, nothing to save in: %+v", v)
	}

	broken := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, "llm: [\n") })
	if w := get(t, broken, "/v1/code-index", testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable file: %d", w.Code)
	}
}

func TestTheCodeIndexIsSavedAndRemoteEmbeddingsNeedConfirmation(t *testing.T) {
	path := networkConfig(t, "llm:\n  provider: openai\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	put := func(body string) *httptest.ResponseRecorder {
		return sendNetwork(t, srv, http.MethodPut, "/v1/code-index", body)
	}

	for name, body := range map[string]string{
		"nothing":        `{}`,
		"only confirm":   `{"confirm":true}`,
		"remote":         `{"embeddings_url":"https://api.openai.com/v1","embeddings_model":"m"}`,
		"not json":       `{`,
		"no model":       `{"embeddings_url":"http://localhost:11434/v1"}`,
		"negative limit": `{"max_files":-1}`,
	} {
		if w := put(body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}

	v := decodeCodeIndex(t, put(`{"semantic":false,"embeddings_auto":false,"max_file_kb":64,"max_files":100,"enabled":true}`))
	if v.Semantic || v.EmbeddingsAuto || v.Embeddings != "off" || v.MaxFileKB != 64 || v.MaxFiles != 100 {
		t.Errorf("limits = %+v", v)
	}
	v = decodeCodeIndex(t, put(`{"embeddings_url":" http://localhost:11434/v1 ","embeddings_model":" nomic-embed-text "}`))
	if v.Embeddings != "endpoint" || !v.EndpointLocal || v.EmbeddingsURL != "http://localhost:11434/v1" ||
		v.EmbeddingsModel != "nomic-embed-text" {
		t.Errorf("local endpoint needs no confirmation: %+v", v)
	}
	v = decodeCodeIndex(t, put(`{"embeddings_url":"https://api.openai.com/v1","embeddings_model":"m","confirm":true}`))
	if v.Embeddings != "endpoint" || v.EndpointLocal {
		t.Errorf("remote confirmed: %+v", v)
	}
	v = decodeCodeIndex(t, put(`{"embeddings_url":""}`))
	if v.Embeddings != "off" {
		t.Errorf("clearing the endpoint needs no confirmation: %+v", v)
	}

	noFile := newTestServer(t, &fakeService{})
	w := sendNetwork(t, noFile, http.MethodPut, "/v1/code-index", `{"semantic":false}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "configuration file") {
		t.Errorf("no file: %d %s", w.Code, w.Body.String())
	}
}
