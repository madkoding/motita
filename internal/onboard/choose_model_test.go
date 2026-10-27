package onboard

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// --- choosing the model ------------------------------------------------------

// TestChooseModelIsLimitedToTheProvider: the list must offer only the models of
// the provider just chosen. A model from another provider would fail at runtime.
func TestChooseModelIsLimitedToTheProvider(t *testing.T) {
	stubDirectAuth(t, "stub-token")
	for _, p := range WizardProviders() {
		dir := t.TempDir()
		out, res, err := run(context.Background(), t, dir, modelAnswers(t, p), Answers{})
		if err != nil {
			t.Fatalf("%s: Run: %v", p.ID, err)
		}
		wantModel := ""
		if p.FetchModels {
			wantModel = "llama3.3" // first stubbed model
		} else {
			wantModel = p.Models[0].ID
		}
		if res.Model != wantModel {
			t.Errorf("%s: model = %q, want %q", p.ID, res.Model, wantModel)
		}
		// The matcher below must be able to see a menu entry at all, or every "absent" is vacuous.
		if !menuEntryPresent(out, wantModel) {
			t.Errorf("%s: the menu does not list its own first model %q:\n%s", p.ID, wantModel, stripANSI(out))
		}
		checkNoForeignModels(t, p, out)
	}
}

// modelAnswers is what a user types to reach the model menu of a provider and save.
// Ollama asks for the key before the model and fetches the live catalogue.
// Providers with SupportsDirectAuth ask an extra question (how to
// authenticate) before the key prompt.
func modelAnswers(t *testing.T, p Provider) []string {
	switch {
	case p.FetchModels && p.ID == "ollama":
		stubOllamaModels(t, []string{"llama3.3", "qwen2.5"})
		// provider, host (cloud), key, model, anchor, save
		return []string{p.ID, "2", "dummy-key", "", "3", ""}
	case p.FetchModels:
		// Ollama Cloud is only the cloud: no host to ask, then the key and the live catalogue.
		stubOllamaModels(t, []string{"llama3.3", "qwen2.5"})
		// provider, key, model, anchor, save
		return []string{p.ID, "dummy-key", "", "3", ""}
	case p.SupportsDirectAuth:
		// provider, auth (direct), model, anchor (no check), save
		return []string{p.ID, "1", "", "3", ""}
	case p.Login != "":
		// provider, model, anchor, save: a login of its own needs nothing else
		return []string{p.ID, "", "3", ""}
	case p.AskBaseURL:
		// provider, endpoint, key, model, anchor, save
		return []string{p.ID, "", "", "", "3", ""}
	default:
		// provider, key, model, anchor, save
		return []string{p.ID, "", "", "3", ""}
	}
}

// checkNoForeignModels: no model of any other provider may appear in the menu. Model IDs and
// even label substrings can legitimately overlap across providers (gpt-4o
// is offered by both openai and copilot), so the check looks for the
// label as a MENU ENTRY — the exact "  N. Label" pattern — rather than as
// a bare substring, which would match inside another provider's label.
func checkNoForeignModels(t *testing.T, p Provider, out string) {
	for _, other := range Providers() {
		if other.ID == p.ID {
			continue
		}
		for _, m := range other.Models {
			if ownModel(p, m.ID) {
				continue // the same id offered by both (gpt-4.1 by OpenAI and Copilot)
			}
			if menuEntryPresent(out, m.ID) {
				t.Errorf("%s: the menu offers %s, from %s", p.ID, m.Label, other.ID)
			}
		}
	}
}

// ownModel reports whether a provider offers a model id itself.
func ownModel(p Provider, id string) bool {
	for _, m := range p.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// TestChooseModelAcceptsAFreeTextID: the catalogue is a shortcut, not a limit;
// models appear faster than any list can follow.
func TestChooseModelAcceptsAFreeTextID(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"openai", "", "", "gpt-5.2-turbo-experimental", "3", ""},
		Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Model != "gpt-5.2-turbo-experimental" {
		t.Errorf("model = %q", res.Model)
	}
}

// --- Ollama Cloud provider ---------------------------------------------------

// TestOllamaWizardUsesFixedBaseURL: the user should not be asked for a URL; the
// provider already knows it is https://ollama.com/v1.
func TestOllamaWizardUsesFixedBaseURL(t *testing.T) {
	stubOllamaModels(t, []string{"llama3.3", "qwen2.5"})
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"ollama", "2", "my-key", "", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != "ollama" {
		t.Errorf("provider = %q", res.Provider.ID)
	}
	cfg, err := os.ReadFile(res.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "https://ollama.com/v1") {
		t.Errorf("the Ollama base URL must be fixed:\n%s", cfg)
	}
}

// TestOllamaWizardSelectsAFetchedModelByNumber: the live catalogue is presented
// and a model can be chosen by its option number.
func TestOllamaWizardSelectsAFetchedModelByNumber(t *testing.T) {
	stubOllamaModels(t, []string{"llama3.3", "qwen2.5"})
	dir := t.TempDir()
	out, res, err := run(context.Background(), t, dir, []string{"ollama", "2", "my-key", "2", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Model != "qwen2.5" {
		t.Errorf("model = %q, want qwen2.5", res.Model)
	}
	if !strings.Contains(out, "llama3.3") || !strings.Contains(out, "qwen2.5") {
		t.Errorf("the fetched models must be shown: %q", out)
	}
}

// TestOllamaWizardAcceptsTypedModelWhenFetchFails: if /api/tags cannot be reached,
// the user can still type a model id.
func TestOllamaWizardAcceptsTypedModelWhenFetchFails(t *testing.T) {
	dir := t.TempDir()
	old := modelLister
	modelLister = func(_ context.Context, _, _ string) ([]string, error) {
		return nil, errors.New("mock network error")
	}
	defer func() { modelLister = old }()
	_, res, err := run(context.Background(), t, dir, []string{"ollama", "2", "my-key", "custom-model", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Model != "custom-model" {
		t.Errorf("model = %q, want custom-model", res.Model)
	}
}

// TestOllamaWizardFallsBackToBuiltInListWhenEmpty: an empty live catalogue must
// not leave the user with nothing to choose: the built-in list is offered.
func TestOllamaWizardFallsBackToBuiltInListWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	old := modelLister
	modelLister = func(_ context.Context, _, _ string) ([]string, error) {
		return []string{}, nil
	}
	defer func() { modelLister = old }()
	out, res, err := run(context.Background(), t, dir, []string{"ollama", "2", "my-key", "", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "built-in list") {
		t.Errorf("the fallback must be announced:\n%s", out)
	}
	p, _ := Lookup("ollama")
	if res.Model != p.Models[0].ID {
		t.Errorf("model = %q, want the first built-in model %q", res.Model, p.Models[0].ID)
	}
}

// TestOllamaWizardAcceptsPresetBaseURL: a fixed provider still lets a preset
// override the base URL, which is useful for a self-hosted Ollama endpoint.
func TestOllamaWizardAcceptsPresetBaseURL(t *testing.T) {
	dir := t.TempDir()
	stubOllamaModels(t, []string{"llama3.3"})
	_, res, err := run(context.Background(), t, dir, []string{"", "3", ""},
		Answers{Provider: "ollama", APIKey: "preset-key", BaseURL: "http://localhost:11434/v1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg, err := os.ReadFile(res.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "http://localhost:11434/v1") {
		t.Errorf("preset base URL was not used:\n%s", cfg)
	}
}

// TestListOllamaModelsWrapperUsesTheAPI: the package-level wrapper reaches a real
// Ollama /api/tags endpoint and returns the model names.
func TestListOllamaModelsWrapperUsesTheAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/tags") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.3"},{"name":"qwen2.5"}]}`))
	}))
	defer srv.Close()

	old := modelLister
	modelLister = listOllamaModels
	defer func() { modelLister = old }()

	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"ollama", "", "3", ""},
		Answers{BaseURL: srv.URL + "/v1", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Model != "llama3.3" {
		t.Errorf("model = %q, want llama3.3", res.Model)
	}
}

// TestOllamaWizardUsesPresetKey: a key supplied in Answers should skip the key
// prompt even for the Ollama provider.
func TestOllamaWizardUsesPresetKey(t *testing.T) {
	stubOllamaModels(t, []string{"llama3.3", "qwen2.5"})
	dir := t.TempDir()
	out, res, err := run(context.Background(), t, dir, []string{"2", "", "3", ""},
		Answers{Provider: "ollama", APIKey: "preset-key"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != "ollama" {
		t.Errorf("provider = %q", res.Provider.ID)
	}
	if strings.Contains(out, "Paste the key") {
		t.Error("the key prompt must not appear when the key is preset")
	}
}

// TestOllamaWizardRequiresKeyBeforeModel: without a preset key the API key prompt
// appears before the model list.
func TestOllamaWizardRequiresKeyBeforeModel(t *testing.T) {
	stubOllamaModels(t, []string{"llama3.3", "qwen2.5"})
	dir := t.TempDir()
	out, _, err := run(context.Background(), t, dir, []string{"ollama", "2", "my-key", "", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	keyIdx := strings.Index(out, "Paste the key")
	modelIdx := strings.Index(out, "Which model should motita use?")
	if keyIdx == -1 || modelIdx == -1 || keyIdx > modelIdx {
		t.Errorf("the key prompt must come before the model prompt:\n%s", out)
	}
}

// TestChooseModelWithAProviderThatHasNoCatalogue: a provider whose catalogue is
// empty (and which publishes none) must still let the user type a model id, and
// must refuse a blank answer instead of accepting nothing.
func TestChooseModelWithAProviderThatHasNoCatalogue(t *testing.T) {
	empty := Provider{ID: "custom", Name: "Custom"}

	// A blank first answer is refused, then the typed id is accepted.
	s := &session{in: bufio.NewReader(strings.NewReader("\nmy-model\n")), out: io.Discard}
	model, err := s.chooseModel(context.Background(), empty, "", "", "k")
	if err != nil {
		t.Fatalf("chooseModel: %v", err)
	}
	if model != "my-model" {
		t.Errorf("model = %q, want my-model", model)
	}

	// The prompt names a plain model id when there is no menu to pick from.
	var out bytes.Buffer
	s2 := &session{in: bufio.NewReader(strings.NewReader("typed\n")), out: &out}
	if _, err := s2.chooseModel(context.Background(), empty, "", "", "k"); err != nil {
		t.Fatalf("chooseModel: %v", err)
	}
	if !strings.Contains(out.String(), "no models were offered") {
		t.Errorf("the empty catalogue must be explained:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Model id:") {
		t.Errorf("with no menu the prompt must ask for an id:\n%s", out.String())
	}
}

// TestKeyVariableForFallsBackForAnUnknownProvider: an id that is not in the
// catalogue still produces a usable variable name.
func TestKeyVariableForFallsBackForAnUnknownProvider(t *testing.T) {
	if got := keyVariableFor("nope"); got != "MOTITA_LLM_API_KEY" {
		t.Errorf("keyVariableFor(unknown) = %q", got)
	}
	if got := keyVariableFor("ollama"); got != "MOTITA_OLLAMA_API_KEY" {
		t.Errorf("keyVariableFor(ollama) = %q", got)
	}
}

// TestGeneratedHeaderNamesTheProviderVariable: the file tells the user which
// variable to export, and for Ollama that is OLLAMA_API_KEY, not OPENAI_API_KEY.
func TestGeneratedHeaderNamesTheProviderVariable(t *testing.T) {
	for _, tc := range []struct {
		provider string
		want     string
		answers  []string
	}{
		// ollama: provider, host (cloud), key, model, anchor, save
		{"ollama", "OLLAMA_API_KEY", []string{"ollama", "2", "k", "", "3", ""}},
		// openai: provider, endpoint, key, model, anchor, save
		{"openai", "MOTITA_LLM_API_KEY", []string{"openai", "", "k", "", "3", ""}},
	} {
		dir := t.TempDir()
		if tc.provider == "ollama" {
			stubOllamaModels(t, []string{"a"})
		}
		_, res, err := run(context.Background(), t, dir, tc.answers, Answers{})
		if err != nil {
			t.Fatalf("%s: Run: %v", tc.provider, err)
		}
		cfg, err := os.ReadFile(res.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(cfg), tc.want) {
			t.Errorf("%s: the header must mention %s:\n%s", tc.provider, tc.want, cfg)
		}
	}
}
