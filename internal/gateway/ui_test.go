package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putUI(t *testing.T, srv *Server, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, srv.BaseURL()+"/v1/ui", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func decodeUI(t *testing.T, w *httptest.ResponseRecorder) uiView {
	t.Helper()
	var v uiView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

// TestTheInterfaceLanguageIsReadAndSavedInTheConfiguration: GET answers the setting the gateway
// started with and what it means on this host; PUT writes the new one into the configuration
// file - the only place every interface started later will look - and answers it.
func TestTheInterfaceLanguageIsReadAndSavedInTheConfiguration(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "es_CL.UTF-8")
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(cfg, []byte("# mine\nllm:\n  provider: openai\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = cfg; o.UILanguage = " AUTO " })

	w := get(t, srv, "/v1/ui", testToken)
	if w.Code != http.StatusOK || decodeUI(t, w) != (uiView{Language: "auto", Resolved: "es"}) {
		t.Fatalf("GET = %d %s", w.Code, w.Body.String())
	}
	if w := get(t, srv, "/v1/ui", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the language is behind the token like everything else: %d", w.Code)
	}

	w = putUI(t, srv, `{"language":"EN"}`, testToken)
	if w.Code != http.StatusOK || decodeUI(t, w) != (uiView{Language: "en", Resolved: "en"}) {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	body, _ := os.ReadFile(cfg)
	if string(body) != "# mine\nllm:\n  provider: openai\n\nui:\n  language: en\n" {
		t.Errorf("the configuration must carry the language, and nothing else may change:\n%s", body)
	}
	if v := decodeUI(t, get(t, srv, "/v1/ui", testToken)); v.Language != "en" {
		t.Errorf("GET after PUT = %+v", v)
	}
}

func TestPutUIRefusals(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(cfg, []byte("ui:\n  language: es\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = cfg })
	for _, body := range []string{`{"language":"fr"}`, `{"language":""}`} {
		if w := putUI(t, srv, body, testToken); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, w.Code)
		}
	}
	if w := putUI(t, srv, `not json`, testToken); w.Code != http.StatusBadRequest {
		t.Errorf("a malformed body = %d, want 400", w.Code)
	}

	old := setUILanguage
	setUILanguage = func(string, string) error { return errors.New("read-only filesystem") }
	w := putUI(t, srv, `{"language":"en"}`, testToken)
	setUILanguage = old
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "read-only") {
		t.Errorf("a failed save must be reported: %d %s", w.Code, w.Body.String())
	}
	if v := decodeUI(t, get(t, srv, "/v1/ui", testToken)); v.Language != "auto" {
		t.Errorf("a language that was not saved must not be reported as set: %+v", v)
	}

	bare := newTestServer(t, &fakeService{}, func(o *Options) { o.UILanguage = "klingon" })
	if v := decodeUI(t, get(t, bare, "/v1/ui", testToken)); v.Language != "auto" {
		t.Errorf("an invalid starting value reads as auto: %+v", v)
	}
	if w := putUI(t, bare, `{"language":"es"}`, testToken); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "configuration file") {
		t.Errorf("no configuration file = %d %s, want 409", w.Code, w.Body.String())
	}
}

// TestTheClientReadsAndSetsTheInterfaceLanguage: the terminal behind a gateway reaches the same
// setting, in both the context form and the runner-interface form.
func TestTheClientReadsAndSetsTheInterfaceLanguage(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "en_US.UTF-8")
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(cfg, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = cfg })
	c := NewClient(srv.BaseURL(), testToken)

	setting, resolved, err := c.FetchUILanguage(context.Background())
	if err != nil || setting != "auto" || resolved != "en" {
		t.Fatalf("FetchUILanguage = %q %q %v", setting, resolved, err)
	}
	if err := c.SetUILanguage("es"); err != nil {
		t.Fatal(err)
	}
	if got := c.UILanguage(); got != "es" {
		t.Errorf("UILanguage = %q, want es", got)
	}
	if err := c.SaveUILanguage(context.Background(), "fr"); err == nil {
		t.Error("a refused language must come back as an error")
	}

	gone := NewClient("http://127.0.0.1:1", testToken)
	if got := gone.UILanguage(); got != "" {
		t.Errorf("an unreachable gateway reads as no setting: %q", got)
	}
	if _, _, err := gone.FetchUILanguage(context.Background()); err == nil {
		t.Error("an unreachable gateway must be an error")
	}
}
