package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

func decodeSecrets(t *testing.T, body string) secretsView {
	t.Helper()
	var v secretsView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return v
}

func TestSecretsAreSavedListedAndNeverReturned(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	long := "tok_abcdefghijklmnopqrstuv1234"

	if w := llmCall(t, srv, http.MethodPut, "/v1/secrets/DEPLOY_TOKEN", `{"value":"`+long+`"}`); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	llmCall(t, srv, http.MethodPut, "/v1/secrets/PIN", `{"value":"1234"}`)
	w := llmCall(t, srv, http.MethodGet, "/v1/secrets", "")
	if strings.Contains(w.Body.String(), long) || strings.Contains(w.Body.String(), `"value"`) {
		t.Fatalf("a value was returned: %s", w.Body)
	}
	v := decodeSecrets(t, w.Body.String())
	if !v.CanSave || len(v.Secrets) != 2 {
		t.Fatalf("view = %+v", v)
	}
	if first, second := v.Secrets[0], v.Secrets[1]; first.Name != "DEPLOY_TOKEN" || first.Hint != "1234" {
		t.Errorf("first = %+v", first)
	} else if second.Hint != "" {
		t.Errorf("second = %+v", second)
	}
	if got := config.ReadSecrets(path)["DEPLOY_TOKEN"]; got != long {
		t.Errorf("stored %q", got)
	}
	if info, err := os.Stat(config.SecretsPath(path)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v %v", info, err)
	}

	w = llmCall(t, srv, http.MethodDelete, "/v1/secrets/PIN", "")
	if v := decodeSecrets(t, w.Body.String()); w.Code != http.StatusOK || len(v.Secrets) != 1 {
		t.Errorf("delete: %d %s", w.Code, w.Body)
	}
}

func TestSecretsRefuseBadRequests(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	for name, c := range map[string]struct{ method, url, body string }{
		"a reserved name":  {http.MethodPut, "/v1/secrets/PATH", `{"value":"x"}`},
		"a motita name":    {http.MethodPut, "/v1/secrets/MOTITA_X", `{"value":"x"}`},
		"a bad name":       {http.MethodPut, "/v1/secrets/1ABC", `{"value":"x"}`},
		"no value":         {http.MethodPut, "/v1/secrets/ABC", `{"value":""}`},
		"a multiline one":  {http.MethodPut, "/v1/secrets/ABC", `{"value":"a\nb"}`},
		"a reserved erase": {http.MethodDelete, "/v1/secrets/PATH", ``},
		"broken json":      {http.MethodPut, "/v1/secrets/ABC", `{`},
	} {
		if w := llmCall(t, srv, c.method, c.url, c.body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestSecretsNeedAConfigurationFile(t *testing.T) {
	llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, "")
	if v := decodeSecrets(t, llmCall(t, srv, http.MethodGet, "/v1/secrets", "").Body.String()); v.CanSave {
		t.Errorf("view = %+v", v)
	}
	if w := llmCall(t, srv, http.MethodPut, "/v1/secrets/ABC", `{"value":"x"}`); w.Code != http.StatusConflict {
		t.Errorf("save: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/secrets/ABC", ""); w.Code != http.StatusConflict {
		t.Errorf("delete: %d", w.Code)
	}
}

func TestSecretsReportAFailedWrite(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	old := setSecret
	setSecret = func(string, string, string) error { return errors.New("disk full") }
	defer func() { setSecret = old }()
	w := llmCall(t, srv, http.MethodPut, "/v1/secrets/ABC", `{"value":"x"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "disk full") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}
