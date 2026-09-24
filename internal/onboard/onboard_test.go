package onboard

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedTime() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }

// run feeds the answers as if typed, and returns the conversation and the result.
func run(ctx context.Context, t *testing.T, dir string, answers []string, preset Answers) (string, Result, error) {
	t.Helper()
	in := strings.NewReader(strings.Join(answers, "\n") + "\n")
	var out bytes.Buffer
	res, err := Run(ctx, in, &out, filepath.Join(dir, "config.yaml"), preset, fixedTime())
	return out.String(), res, err
}

// stubOllamaModels replaces the live model lister with a deterministic catalogue
// for the duration of the test. Use it in any test that selects the Ollama
// provider.
func stubOllamaModels(t *testing.T, models []string) {
	t.Helper()
	old := modelLister
	modelLister = func(_ context.Context, _, _ string) ([]string, error) {
		return models, nil
	}
	t.Cleanup(func() { modelLister = old })
}

// --- the generated file must be loadable by the real parser ------------------

// TestGeneratedConfigIsAcceptedByTheProgram: the whole point of the wizard is that
// the file it writes works. This loads it with the same validation the CLI uses.
func TestGeneratedConfigIsAcceptedByTheProgram(t *testing.T) {
	dir := t.TempDir()
	stubDirectAuth(t, "test-oauth-token")
	preset := Answers{Provider: "anthropic", Model: "claude-3-5-haiku-latest"}
	// The anchor, base URL and auth choice are still asked (presets left empty on purpose).
	_, res, err := run(context.Background(), t, dir, []string{"1", "true", "", "1"}, preset)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	content, err := os.ReadFile(res.ConfigPath)
	if err != nil {
		t.Fatalf("the configuration must exist: %v", err)
	}
	text := string(content)

	// The key is NOT in the configuration, and the settings that make it work on
	// its own are there.
	if strings.Contains(text, "api_key:") {
		t.Error("the configuration must not carry a key")
	}
	for _, want := range []string{"task_source:", "anchor:", "sandbox:", "llm:", "agent:",
		"provider: anthropic", "model: claude-3-5-haiku-latest", "base_url:", "kind: stdin"} {
		if !strings.Contains(text, want) {
			t.Errorf("the generated configuration lacks %q", want)
		}
	}
}

// TestClaudeCodeAsksForNoKey: the Claude subscription is the claude CLI's own login,
// so the wizard asks for no endpoint and no key, writes no credentials, and says
// how to log in instead.
func TestClaudeCodeAsksForNoKey(t *testing.T) {
	dir := t.TempDir()
	// provider, model (default), check (always pass): nothing else may be asked.
	out, res, err := run(context.Background(), t, dir, []string{"claude-code", "", "2"}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Model != "sonnet" || res.CredentialsPath != "" {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(credentialsPathFor(res.ConfigPath)); !os.IsNotExist(err) {
		t.Errorf("no credentials file may be written: %v", err)
	}
	plain := stripANSI(out)
	for _, asked := range []string{"API endpoint", "API key", "Authentication"} {
		if strings.Contains(plain, asked) {
			t.Errorf("the wizard asked for %q", asked)
		}
	}
	if !strings.Contains(plain, "claude auth login") || !strings.Contains(plain, "install Claude Code") {
		t.Errorf("the summary must say how to log in:\n%s", plain)
	}
	content, _ := os.ReadFile(res.ConfigPath)
	text := string(content)
	for _, want := range []string{"provider: claude-code", "model: sonnet", `base_url: ""`, "No key is needed"} {
		if !strings.Contains(text, want) {
			t.Errorf("the configuration lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "MOTITA_LLM_API_KEY") {
		t.Error("the configuration must not send the user to a key variable")
	}
}

// TestNothingIsWrittenWhenTheUserCancels: cancelling must leave no trace, so a
// half-configured agent cannot exist.
func TestNothingIsWrittenWhenTheUserCancels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// "q" at the provider question.
	_, _, err := run(context.Background(), t, dir, []string{"q"}, Answers{})
	if err != ErrCancelled {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("nothing must be written when the wizard is cancelled")
	}

	// EOF (a pipe with no input) is a cancellation too.
	var out bytes.Buffer
	if _, err := Run(context.Background(), strings.NewReader(""), &out, path, Answers{}, fixedTime()); err != ErrCancelled {
		t.Errorf("EOF = %v, want ErrCancelled", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("nothing must be written after EOF")
	}
}

// TestRunCancelsOnContextShutdown: Ctrl+C during the wizard must return ErrCancelled
// immediately, without writing anything.
func TestRunCancelsOnContextShutdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	go func() {
		// Cancel after the prompt is printed but before an answer is given.
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Run(ctx, strings.NewReader(""), &out, path, Answers{}, fixedTime())
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("nothing may be written when the context is cancelled")
	}
}

// TestAskReturnsReadErrors: a broken terminal must be reported when the error is
// neither EOF nor a cancellation.
func TestAskReturnsReadErrors(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	in := &failingReader{remaining: 0}
	_, err := Run(ctx, in, &out, filepath.Join(dir, "config.yaml"), Answers{}, fixedTime())
	if err == nil {
		t.Fatal("a broken terminal must be reported")
	}
	if errors.Is(err, ErrCancelled) {
		t.Errorf("err = %v, want a read error, not cancellation", err)
	}
}

// TestReadLineCancelsWhenBlocked: a slow reader must not prevent Ctrl+C from
// stopping the wizard.
func TestReadLineCancelsWhenBlocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	r, _ := io.Pipe() // blocks forever
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Run(ctx, r, &out, path, Answers{}, fixedTime())
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

// --- choosing the provider ---------------------------------------------------

func TestChooseProviderByNumber(t *testing.T) {
	dir := t.TempDir()
	stubDirectAuth(t, "test-token")
	provider := Providers()[4] // anthropic
	model := provider.Models[0].ID
	out, res, err := run(context.Background(), t, dir, []string{"5", "1", "2", "", "1"}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != provider.ID {
		t.Errorf("provider = %q, want %q", res.Provider.ID, provider.ID)
	}
	if res.Model != model {
		t.Errorf("model = %q, want %q", res.Model, model)
	}
	if !strings.Contains(out, "Choose your LLM provider") {
		t.Errorf("the provider question must be asked: %q", out)
	}
}

func TestChooseProviderByName(t *testing.T) {
	dir := t.TempDir()
	stubDirectAuth(t, "test-token")
	_, res, err := run(context.Background(), t, dir, []string{"gemini", "2", "2", "", "1"}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != "gemini" {
		t.Errorf("provider = %q", res.Provider.ID)
	}
}

// TestChooseProviderTakesTheDefault: pressing Enter must pick the first option.
func TestChooseProviderTakesTheDefault(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"", "", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != Providers()[0].ID {
		t.Errorf("provider = %q, want the first one", res.Provider.ID)
	}
	if res.Model != Providers()[0].Models[0].ID {
		t.Errorf("model = %q, want the first one", res.Model)
	}
}

// TestChooseProviderRejectsGarbage: a wrong answer is explained and asked again,
// and a number outside the list is refused.
func TestChooseProviderRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	out, res, err := run(context.Background(), t, dir, []string{"nonsense", "9", "openai", "1", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != "openai" {
		t.Errorf("provider = %q", res.Provider.ID)
	}
	if !strings.Contains(out, "I do not know") {
		t.Error("the user must be told the answer was not understood")
	}
	if !strings.Contains(out, "There is no option 9") {
		t.Error("an out-of-range number must be refused")
	}
}

// TestChooseProviderGivesUpAfterThreeAttempts: it cannot loop forever.
func TestChooseProviderGivesUpAfterThreeAttempts(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"x", "y", "z"}, Answers{})
	if err == nil {
		t.Fatal("three wrong answers must end the wizard")
	}
	if !strings.Contains(err.Error(), "three attempts") {
		t.Errorf("err = %v", err)
	}
}

// TestChooseModelFallsBackToDefaultBaseURL verifies that chooseModel uses the
// provider's fixed endpoint when no listing URL is supplied.
func TestChooseModelFallsBackToDefaultBaseURL(t *testing.T) {
	old := modelLister
	modelLister = func(_ context.Context, url, _ string) ([]string, error) {
		if url != "https://ollama.com/v1" {
			return nil, fmt.Errorf("expected default URL, got %s", url)
		}
		return []string{"fallback-model"}, nil
	}
	defer func() { modelLister = old }()

	p, _ := Lookup("ollama")
	s := &session{in: bufio.NewReader(strings.NewReader("1\n")), out: io.Discard}
	model, err := s.chooseModel(context.Background(), p, "", "", "key")
	if err != nil {
		t.Fatalf("chooseModel: %v", err)
	}
	if model != "fallback-model" {
		t.Errorf("model = %q", model)
	}
}

// TestOllamaKeyPromptError: an error while reading the API key for Ollama must
// be reported immediately.
func TestOllamaKeyPromptError(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Run(ctx, strings.NewReader("ollama\n"), io.Discard, filepath.Join(dir, "config.yaml"), Answers{}, fixedTime())
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

// menuEntryPresent reports whether a label appears as a numbered menu entry in
// the wizard output — the pattern "  N. Label" — as opposed to a bare substring
// match that would fire inside another provider's longer label.
func menuEntryPresent(out, label string) bool {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// A menu entry looks like "1. Label — note" or "1. Label".
		dot := strings.Index(line, ". ")
		if dot < 0 {
			continue
		}
		rest := strings.TrimSpace(line[dot+2:])
		// Strip the " — note" suffix to compare only the label.
		if dash := strings.Index(rest, " — "); dash >= 0 {
			rest = rest[:dash]
		}
		if rest == label {
			return true
		}
	}
	return false
}

// stubDirectAuth replaces the direct-auth runner with a deterministic stub that
// returns the given token without touching the network. Use it in any test that
// selects a provider with SupportsDirectAuth and would otherwise enter the
// OAuth flow.
func stubDirectAuth(t *testing.T, token string) {
	t.Helper()
	old := directAuthRunner
	directAuthRunner = func(ctx context.Context, out io.Writer, in *bufio.Reader, p Provider) (string, error) {
		fmt.Fprintln(out, "stub: direct auth simulated")
		return token, nil
	}
	t.Cleanup(func() { directAuthRunner = old })
}

// --- choosing the model ------------------------------------------------------

// TestChooseModelIsLimitedToTheProvider: the list must offer only the models of
// the provider just chosen. A model from another provider would fail at runtime.
func TestChooseModelIsLimitedToTheProvider(t *testing.T) {
	stubDirectAuth(t, "stub-token")
	for _, p := range Providers() {
		dir := t.TempDir()
		// Ollama asks for the key before the model and fetches the live catalogue.
		// Providers with SupportsDirectAuth ask an extra question (how to
		// authenticate) before the key prompt.
		var answers []string
		if p.FetchModels {
			stubOllamaModels(t, []string{"llama3.3", "qwen2.5"})
			answers = []string{p.ID, "dummy-key", "", "2", ""}
		} else if p.SupportsDirectAuth {
			// provider, model, anchor(always pass), baseURL(default), auth(direct)
			answers = []string{p.ID, "", "2", "", "1"}
		} else {
			answers = []string{p.ID, "", "2", "", ""}
		}
		out, res, err := run(context.Background(), t, dir, answers, Answers{})
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
		// No model of any other provider may appear in the menu. Model IDs and
		// even label substrings can legitimately overlap across providers (gpt-4o
		// is offered by both openai and copilot), so the check looks for the
		// label as a MENU ENTRY — the exact "  N. Label" pattern — rather than as
		// a bare substring, which would match inside another provider's label.
		for _, other := range Providers() {
			if other.ID == p.ID {
				continue
			}
			for _, m := range other.Models {
				if menuEntryPresent(out, m.Label) {
					t.Errorf("%s: the menu offers %s, from %s", p.ID, m.Label, other.ID)
				}
			}
		}
	}
}

// TestChooseModelAcceptsAFreeTextID: the catalogue is a shortcut, not a limit;
// models appear faster than any list can follow.
func TestChooseModelAcceptsAFreeTextID(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"openai", "gpt-5.2-turbo-experimental", "2", "", ""}, Answers{})
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
	_, res, err := run(context.Background(), t, dir, []string{"ollama", "my-key", "", "2", ""}, Answers{})
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
	out, res, err := run(context.Background(), t, dir, []string{"ollama", "my-key", "2", "2", ""}, Answers{})
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
	_, res, err := run(context.Background(), t, dir, []string{"ollama", "my-key", "custom-model", "2", ""}, Answers{})
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
	out, res, err := run(context.Background(), t, dir, []string{"ollama", "my-key", "", "2", ""}, Answers{})
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
	_, res, err := run(context.Background(), t, dir, []string{"", "2", ""}, Answers{Provider: "ollama", APIKey: "preset-key", BaseURL: "http://localhost:11434/v1"})
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
	_, res, err := run(context.Background(), t, dir, []string{"ollama", "test-key", "", "2", ""}, Answers{BaseURL: srv.URL + "/v1"})
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
	out, res, err := run(context.Background(), t, dir, []string{"", "2", ""}, Answers{Provider: "ollama", APIKey: "preset-key"})
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
	out, _, err := run(context.Background(), t, dir, []string{"ollama", "my-key", "", "2", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	keyIdx := strings.Index(out, "Paste the key")
	modelIdx := strings.Index(out, "Choose a model")
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
	if got := keyVariableFor("ollama"); got != "OLLAMA_API_KEY" {
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
		// ollama: provider, key, model, anchor
		{"ollama", "OLLAMA_API_KEY", []string{"ollama", "k", "", "2"}},
		// openai: provider, model, anchor, base URL, key
		{"openai", "MOTITA_LLM_API_KEY", []string{"openai", "", "2", "", "k"}},
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

// --- choosing the check ------------------------------------------------------

func TestChooseAnchorWithACommand(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "1", "1", "go", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "command: go") {
		t.Errorf("the chosen command must be written:\n%s", cfg)
	}
}

// TestChooseAnchorDefaultIsMakeTest: pressing Enter takes the sensible default.
func TestChooseAnchorDefaultIsMakeTest(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "1", "1", "", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if !strings.Contains(string(cfg), "command: make") || !strings.Contains(string(cfg), "args: [test]") {
		t.Errorf("the default check must be make test:\n%s", cfg)
	}
}

// TestChooseAnchorAlwaysPassIsExplicit: option 2 is the escape hatch, and it is
// written as a real command so the agent's rule (never trust the model) holds.
func TestChooseAnchorAlwaysPassIsExplicit(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	// The string "true" is quoted so YAML does not turn it into a boolean: the
	// anchor runs the command /bin/true, which exits 0 and therefore passes.
	if !strings.Contains(string(cfg), `command: "true"`) {
		t.Errorf("the escape hatch must be written as a real command:\n%s", cfg)
	}
}

func TestChooseAnchorRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	out, _, err := run(context.Background(), t, dir, []string{"openai", "1", "4", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "Choose 1 or 2") {
		t.Error("the user must be told the valid answers")
	}
}

// --- the API key -------------------------------------------------------------

// TestTheKeyGoesToItsOwnFile: when the user pastes a key it is written next to the
// configuration with 0600 permissions and never inside the configuration.
func TestTheKeyGoesToItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "", "sk-secret-value"}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CredentialsPath == "" {
		t.Fatal("a key was given, so a credentials file must be written")
	}

	cfg, _ := os.ReadFile(res.ConfigPath)
	if strings.Contains(string(cfg), "sk-secret-value") {
		t.Error("the key must never be in the configuration")
	}

	cred, err := os.ReadFile(res.CredentialsPath)
	if err != nil {
		t.Fatalf("the credentials file must exist: %v", err)
	}
	if !strings.Contains(string(cred), "sk-secret-value") {
		t.Error("the key must be in the credentials file")
	}
	if !strings.Contains(string(cred), "export MOTITA_LLM_API_KEY=") {
		t.Errorf("the file must be sourceable:\n%s", cred)
	}

	info, err := os.Stat(res.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the credentials file is %o, want 600", perm)
	}
}

// TestNoKeyMeansNoCredentialsFile: pressing Enter leaves the setup to the
// environment, and the summary says which variable to export.
func TestNoKeyMeansNoCredentialsFile(t *testing.T) {
	dir := t.TempDir()
	out, res, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CredentialsPath != "" {
		t.Error("no key was given, so no credentials file")
	}
	if !strings.Contains(out, "export MOTITA_LLM_API_KEY=") {
		t.Errorf("the summary must say what to export: %q", out)
	}
}

// TestAKeyWithQuotesCannotBreakTheFile: the credentials file is a shell script, so
// the value has to be quoted safely.
func TestAKeyWithQuotesCannotBreakTheFile(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "", "it's a 'weird' key"}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cred, _ := os.ReadFile(res.CredentialsPath)
	line := ""
	for _, l := range strings.Split(string(cred), "\n") {
		if strings.HasPrefix(l, "export ") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("no export line")
	}
	// The real property: the shell can read it back. shQuote escapes an
	// apostrophe as '\'' (close, escaped quote, reopen), so counting quotes is
	// the wrong check; running the shell is the right one.
	script := line + "\nprintf '%s' \"$MOTITA_LLM_API_KEY\"\n"
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("the generated line is not valid shell: %v (%s)", err, line)
	}
	if got := string(out); got != "it's a 'weird' key" {
		t.Errorf("the shell read %q, want the original key", got)
	}
}

// --- preset answers (the non-interactive path) -------------------------------

// TestPresetAnswersSkipTheQuestions: every answer given up front must not be
// asked, which is what makes the wizard usable from a script.
func TestPresetAnswersSkipTheQuestions(t *testing.T) {
	dir := t.TempDir()
	stubDirectAuth(t, "preset-token")
	preset := Answers{
		Provider:      "gemini",
		Model:         "gemini-2.5-pro",
		BaseURL:       "https://example.invalid/v1",
		AnchorCommand: "true",
	}
	// The only question left is the auth choice (gemini supports direct auth).
	out, res, err := run(context.Background(), t, dir, []string{"1"}, preset)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out, "Choose your LLM provider") || strings.Contains(out, "Choose a model") {
		t.Errorf("preset answers must not be asked again: %q", out)
	}
	cfg, _ := os.ReadFile(res.ConfigPath)
	if !strings.Contains(string(cfg), "base_url: \"https://example.invalid/v1\"") {
		t.Errorf("the preset endpoint must be written:\n%s", cfg)
	}
}

func TestPresetWithAnUnknownProviderFails(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, nil, Answers{Provider: "not-a-provider"})
	if err == nil {
		t.Fatal("an unknown provider must be refused")
	}
	if !strings.Contains(err.Error(), "openai") || !strings.Contains(err.Error(), "gemini") {
		t.Errorf("the error must list what is accepted: %v", err)
	}
}

// --- the catalogue must match the client -------------------------------------

// TestCatalogueMatchesTheClientProtocols: the wizard must only offer providers the
// LLM client can actually talk to. Adding one here without implementing it in
// internal/llm would be a promise the program cannot keep.
func TestCatalogueMatchesTheClientProtocols(t *testing.T) {
	implemented := map[string]bool{"openai": true, "ollama": true, "anthropic": true, "gemini": true, "codex": true, "copilot": true, "claude-code": true}
	for _, p := range Providers() {
		if !implemented[p.ID] {
			t.Errorf("the wizard offers %q, which the client does not implement", p.ID)
		}
		// A provider with its own login has no endpoint and no key to name.
		if p.Name == "" || p.ConsoleURL == "" || !p.NoKey && (p.DefaultBaseURL == "" || p.EnvKey == "") {
			t.Errorf("%q is incomplete: %+v", p.ID, p)
		}
		if len(p.Models) == 0 && !p.FetchModels {
			t.Errorf("%q offers no model and does not fetch them", p.ID)
		}
	}
	if len(Providers()) != len(implemented) {
		t.Errorf("the client implements %d providers and the wizard offers %d",
			len(implemented), len(Providers()))
	}
}

func TestNamesAndHelpers(t *testing.T) {
	if got := Names(); got != "anthropic, claude-code, codex, copilot, gemini, ollama, openai" {
		t.Errorf("Names() = %q", got)
	}
	if _, ok := Lookup("ANTHROPIC"); !ok {
		t.Error("Lookup must be case-insensitive")
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup must reject an unknown provider")
	}
	if got := DefaultBaseURL("openai"); got != "https://api.openai.com/v1" {
		t.Errorf("DefaultBaseURL = %q", got)
	}
	if got := DefaultBaseURL("nope"); got != "" {
		t.Errorf("an unknown provider has no default endpoint, got %q", got)
	}
	for _, p := range Providers() {
		if p.EnvKey == "" && !p.NoKey {
			t.Errorf("provider %q carries no key variable for the instructions", p.ID)
		}
	}
	if got := Providers()[0].String(); !strings.Contains(got, "openai") {
		t.Errorf("String() = %q", got)
	}
}

// --- rendering ---------------------------------------------------------------

// TestYamlScalarQuotesWhatNeedsQuoting: the generated file must survive an anchor
// command or a model with characters that mean something in YAML.
func TestYamlScalarQuotesWhatNeedsQuoting(t *testing.T) {
	cases := []struct{ in, want string }{
		{"make", "make"},
		{"", `""`},
		{"true", `"true"`}, // a boolean, not a string
		{"null", `"null"`},
		{"a: b", `"a: b"`}, // a colon changes the meaning
		{"#comment", `"#comment"`},
		{"it's", `"it's"`}, // quoting it is harmless and unambiguous
		{`quo"te`, `"quo\"te"`},
		{"line\nbreak", `"line\nbreak"`},
		{" padded ", `" padded "`},
	}
	for _, tc := range cases {
		if got := yamlScalar(tc.in); got != tc.want {
			t.Errorf("yamlScalar(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestWriteFileAtomicLeavesNoTemporaryBehind and is safe when it fails.
func TestWriteFileAtomicLeavesNoTemporaryBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.yaml")
	if err := writeFileAtomic(path, []byte("hello")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "hello" {
		t.Fatalf("content = %q, err = %v", content, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".motita-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// TestWriteFileAtomicFailsWhenTheDirectoryCannotBeCreated.
func TestWriteFileAtomicFailsWhenTheDirectoryCannotBeCreated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the permission check does not apply")
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	err := writeFileAtomic(filepath.Join(blocked, "x", "file.yaml"), []byte("x"))
	if err == nil {
		t.Error("an unwritable directory must be reported")
	}
}

// TestCredentialsPathFor: the credentials live next to the configuration.
func TestCredentialsPathFor(t *testing.T) {
	if got := credentialsPathFor("/a/b/config.yaml"); got != "/a/b/config.env" {
		t.Errorf("credentialsPathFor = %q", got)
	}
	if got := credentialsPathFor("config"); got != "config.env" {
		t.Errorf("credentialsPathFor = %q", got)
	}
}

// --- error paths -------------------------------------------------------------

// failingReader fails after N successful reads, to exercise the branches that
// cannot be reached with a well-behaved terminal.
type failingReader struct {
	remaining int
}

func (f *failingReader) Read(b []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, errors.New("the terminal went away")
	}
	f.remaining--
	b[0] = '\n'
	return 1, nil
}

// TestReadErrorIsReported: a broken terminal must be reported, not swallowed.
func TestReadErrorIsReported(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	_, err := Run(context.Background(), &failingReader{}, &out, filepath.Join(dir, "c.yaml"), Answers{}, fixedTime())
	if err == nil {
		t.Fatal("a read error must surface")
	}
	if !strings.Contains(err.Error(), "terminal went away") {
		t.Errorf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "c.yaml")); !os.IsNotExist(statErr) {
		t.Error("nothing may be written when the terminal fails")
	}
}

// TestReadErrorDuringTheModelQuestion, the anchor question and the key question.
func TestReadErrorAtEachQuestion(t *testing.T) {
	cases := []struct {
		name      string
		remaining int // reads that succeed before the failure
		preset    Answers
	}{
		{"provider", 0, Answers{}},
		{"model", 1, Answers{}},
		{"anchor", 2, Answers{}},
		{"base_url", 3, Answers{}},
		{"key", 4, Answers{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var out bytes.Buffer
			in := &failingReader{remaining: tc.remaining}
			_, err := Run(context.Background(), in, &out, filepath.Join(dir, "c.yaml"), tc.preset, fixedTime())
			if err == nil {
				t.Fatalf("the failure at %s must surface", tc.name)
			}
		})
	}
}

// TestCancellingAtEachQuestion: "q" at any point leaves nothing behind.
func TestCancellingAtEachQuestion(t *testing.T) {
	cases := []struct {
		name    string
		answers []string
	}{
		{"provider", []string{"q"}},
		{"model", []string{"openai", "quit"}},
		{"anchor", []string{"openai", "1", "q"}},
		{"base_url", []string{"openai", "1", "2", "q"}},
		{"key", []string{"openai", "1", "2", "", "q"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "c.yaml")
			in := strings.NewReader(strings.Join(tc.answers, "\n") + "\n")
			var out bytes.Buffer
			_, err := Run(context.Background(), in, &out, path, Answers{}, fixedTime())
			if err != ErrCancelled {
				t.Fatalf("err = %v, want ErrCancelled", err)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Error("nothing may be written after cancelling")
			}
		})
	}
}

// TestModelQuestionGivesUpAfterThreeAttempts.
func TestModelQuestionGivesUpAfterThreeAttempts(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "0", "0", "0"}, Answers{})
	if err == nil || !strings.Contains(err.Error(), "three attempts") {
		t.Errorf("err = %v", err)
	}
}

// TestAnchorQuestionGivesUpAfterThreeAttempts.
func TestAnchorQuestionGivesUpAfterThreeAttempts(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "1", "x", "y", "z"}, Answers{})
	if err == nil || !strings.Contains(err.Error(), "three attempts") {
		t.Errorf("err = %v", err)
	}
}

// TestConfigurationCannotBeWrittenIsReported: an impossible destination must be
// reported and must not leave a partial file.
func TestConfigurationCannotBeWrittenIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the permission check does not apply")
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader("openai\n1\n2\n\n\n")
	var out bytes.Buffer
	_, err := Run(context.Background(), in, &out, filepath.Join(blocked, "x", "config.yaml"), Answers{}, fixedTime())
	if err == nil {
		t.Error("an unwritable configuration path must be reported")
	}
}

// TestCredentialsWriteFailureIsReported: if the configuration can be written but
// the key file cannot, the wizard must say so instead of pretending it worked.
func TestCredentialsWriteFailureIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the permission check does not apply")
	}
	dir := t.TempDir()
	// The credentials go to <base>.env, so occupying that name with a directory
	// makes the write fail while the configuration succeeds.
	if err := os.Mkdir(filepath.Join(dir, "config.env"), 0o500); err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader("openai\n1\n2\n\nsk-abc\n")
	var out bytes.Buffer
	_, err := Run(context.Background(), in, &out, filepath.Join(dir, "config.yaml"), Answers{}, fixedTime())
	if err == nil {
		t.Error("a credentials write failure must be reported")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "config.yaml")); statErr != nil {
		t.Error("the configuration itself was written, so it must be there")
	}
}

// TestRenderIncludesTheSeparatorBetweenArguments exercises the branch that writes
// the comma, which only appears with two arguments or more.
func TestRenderIncludesTheSeparatorBetweenArguments(t *testing.T) {
	out := string(renderConfig(configValues{
		provider: "openai", model: "m", baseURL: "u",
		anchorCommand: "go", anchorArgs: []string{"test", "./..."},
	}))
	if !strings.Contains(out, "args: [test, ./...]") {
		t.Errorf("arguments must be separated:\n%s", out)
	}
}

// --- choosing the API base URL ----------------------------------------------

// TestChooseBaseURLUsesTheDefault: pressing Enter accepts the provider default.
func TestChooseBaseURLUsesTheDefault(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg, _ := os.ReadFile(res.ConfigPath)
	if !strings.Contains(string(cfg), "base_url: \"https://api.openai.com/v1\"") {
		t.Errorf("the default OpenAI-compatible endpoint must be written:\n%s", cfg)
	}
}

// TestChooseBaseURLAcceptsACustomEndpoint: any OpenAI-compatible URL works.
func TestChooseBaseURLAcceptsACustomEndpoint(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "https://ollama.com/v1", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg, _ := os.ReadFile(res.ConfigPath)
	if !strings.Contains(string(cfg), "base_url: \"https://ollama.com/v1\"") {
		t.Errorf("the custom endpoint must be written:\n%s", cfg)
	}
}

// TestChooseBaseURLRejectsGarbage: a URL without scheme is explained and asked again.
func TestChooseBaseURLRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	out, res, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "not-a-url", "https://ollama.com/v1", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg, _ := os.ReadFile(res.ConfigPath)
	if !strings.Contains(string(cfg), "base_url: \"https://ollama.com/v1\"") {
		t.Errorf("the corrected endpoint must be written:\n%s", cfg)
	}
	if !strings.Contains(out, "A URL must start with http:// or https://") {
		t.Error("the user must be told why the URL was rejected")
	}
}

// TestChooseBaseURLGivesUpAfterThreeAttempts.
func TestChooseBaseURLGivesUpAfterThreeAttempts(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "bad", "bad", "bad"}, Answers{})
	if err == nil || !strings.Contains(err.Error(), "three attempts") {
		t.Errorf("err = %v", err)
	}
}

// --- the injectable filesystem seams -----------------------------------------

// TestWriteFileAtomicReportsEveryFilesystemFailure drives the branches that a real
// test cannot provoke: no temporary file, no chmod, no rename, no close.
func TestWriteFileAtomicReportsEveryFilesystemFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")

	t.Run("create", func(t *testing.T) {
		saved := createTemp
		defer func() { createTemp = saved }()
		createTemp = func(string, string) (*os.File, error) {
			return nil, errors.New("no space left on device")
		}
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed create must be reported")
		} else if !strings.Contains(err.Error(), "no space left") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("chmod", func(t *testing.T) {
		saved := chmodFile
		defer func() { chmodFile = saved }()
		chmodFile = func(string, os.FileMode) error { return errors.New("chmod refused") }
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed chmod must be reported")
		}
		if _, statErr := os.Stat(target); statErr == nil {
			t.Error("nothing may be left in place when it fails midway")
		}
	})

	t.Run("rename", func(t *testing.T) {
		saved := renameFile
		defer func() { renameFile = saved }()
		renameFile = func(string, string) error { return errors.New("rename refused") }
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed rename must be reported")
		}
	})

	t.Run("close", func(t *testing.T) {
		saved := closeFile
		defer func() { closeFile = saved }()
		closeFile = func(*os.File) error { return errors.New("close refused") }
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed close must be reported")
		}
	})

	t.Run("close and write", func(t *testing.T) {
		// A directory cannot be written to as a file: this exercises a write that
		// fails after the temporary file exists.
		saved := createTemp
		defer func() { createTemp = saved }()
		createTemp = func(_ string, _ string) (*os.File, error) {
			f, err := os.CreateTemp(t.TempDir(), "readonly-*")
			if err != nil {
				return nil, err
			}
			f.Close()
			return os.Open(f.Name()) // read-only handle: writing fails
		}
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed write must be reported")
		}
	})
}
