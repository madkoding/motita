package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// unsetEnvForTest removes the named env vars for the duration of the test and
// restores them afterwards. t.Setenv("") sets a variable to the empty string
// (which os.LookupEnv still reports as present), so this helper is needed when
// a test must assert that a variable is truly absent.
func unsetEnvForTest(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		old, ok := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if ok {
				os.Setenv(name, old)
			} else {
				os.Unsetenv(name)
			}
		})
	}
}

// The current provider appears even when no key is available for it, so a front
// end can show what is active and let the user configure a key.
func TestHandleProvidersReturnsCurrentWithoutKey(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	// Ensure no env var supplies a key for any provider.
	unsetEnvForTest(t, "MOTITA_LLM_API_KEY", "OLLAMA_API_KEY", "GITHUB_COPILOT_TOKEN")

	srv := newTestServer(t, &fakeService{cfg: cfg})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/providers"), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	var out struct {
		Providers []providerInfo `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	var found bool
	for _, p := range out.Providers {
		if p.ID == "openai" {
			found = true
			if !p.IsCurrent {
				t.Errorf("openai: is_current = false, want true")
			}
			if p.KeyPresent {
				t.Errorf("openai: key_present = true, want false when no key is configured")
			}
		}
	}
	if !found {
		t.Fatalf("the current provider (openai) was not in the list: %+v", out.Providers)
	}
}

// A provider whose env var is set appears in the list with key_present = true.
func TestHandleProvidersReturnsProvidersWithKeys(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	// Set the env var for a non-current, generic-key provider (openrouter).
	t.Setenv("MOTITA_LLM_API_KEY", "sk-test")

	srv := newTestServer(t, &fakeService{cfg: cfg})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/providers"), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	var out struct {
		Providers []providerInfo `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}

	// The current provider (openai) must appear.
	var foundOpenAI bool
	// At least one provider with a key present (any generic-key provider that
	// is NOT the current one) must appear, because MOTITA_LLM_API_KEY is set.
	var foundWithKey bool
	for _, p := range out.Providers {
		if p.ID == "openai" {
			foundOpenAI = true
		}
		if p.KeyPresent && !p.IsCurrent {
			foundWithKey = true
		}
	}
	if !foundOpenAI {
		t.Errorf("the current provider (openai) was not in the list: %+v", out.Providers)
	}
	if !foundWithKey {
		t.Errorf("no provider with a key (other than the current one) appeared: %+v", out.Providers)
	}
}

// A provider whose key is absent and that is not the current one does NOT
// appear, so the list is only what a front end can act on.
func TestHandleProvidersOmitsProvidersWithoutKey(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	unsetEnvForTest(t, "MOTITA_LLM_API_KEY", "OLLAMA_API_KEY", "GITHUB_COPILOT_TOKEN")

	srv := newTestServer(t, &fakeService{cfg: cfg})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/providers"), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	var out struct {
		Providers []providerInfo `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	// Every provider in the list must be either the current one or have a key.
	for _, p := range out.Providers {
		if !p.IsCurrent && !p.KeyPresent {
			t.Errorf("provider %q is neither current nor has a key; it must not appear", p.ID)
		}
	}
}

// handleModelList returns 502 when it cannot reach the provider (no valid key
// or base URL). The default base URL for the empty/unknown provider cannot be
// reached from a test, so the call fails and the handler reports 502.
func TestHandleModelListReturnsBadGateway(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	cfg.LLM.BaseURL = "http://127.0.0.1:1" // unreachable
	unsetEnvForTest(t, "MOTITA_LLM_API_KEY", "OLLAMA_API_KEY", "GITHUB_COPILOT_TOKEN")

	srv := newTestServer(t, &fakeService{cfg: cfg})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/model-list"), testToken)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %q)", w.Code, w.Body.String())
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if e.Error == "" {
		t.Errorf("the error reason must reach the user: %q", w.Body.String())
	}
}