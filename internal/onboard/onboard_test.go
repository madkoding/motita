package onboard

import (
	"bytes"
	"context"
	"errors"
	"io"
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

// --- the generated file must be loadable by the real parser ------------------

// TestGeneratedConfigIsAcceptedByTheProgram: the whole point of the wizard is that
// the file it writes works. This loads it with the same validation the CLI uses.
func TestGeneratedConfigIsAcceptedByTheProgram(t *testing.T) {
	dir := t.TempDir()
	preset := Answers{Provider: "anthropic", Model: "claude-3-5-haiku-latest"}
	// The anchor and the key are still asked (presets left empty on purpose).
	_, res, err := run(context.Background(), t, dir, []string{"1", "true", "", ""}, preset)
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
	provider := Providers()[1] // anthropic
	model := provider.Models[0].ID
	out, res, err := run(context.Background(), t, dir, []string{"2", "1", "2", "", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != provider.ID {
		t.Errorf("provider = %q, want %q", res.Provider.ID, provider.ID)
	}
	if res.Model != model {
		t.Errorf("model = %q, want %q", res.Model, model)
	}
	if !strings.Contains(out, "Which provider") {
		t.Errorf("the provider question must be asked: %q", out)
	}
}

func TestChooseProviderByName(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"gemini", "2", "2", "", ""}, Answers{})
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

// --- choosing the model ------------------------------------------------------

// TestChooseModelIsLimitedToTheProvider: the list must offer only the models of
// the provider just chosen. A model from another provider would fail at runtime.
func TestChooseModelIsLimitedToTheProvider(t *testing.T) {
	for _, p := range Providers() {
		dir := t.TempDir()
		out, res, err := run(context.Background(), t, dir, []string{p.ID, "", "2", "", ""}, Answers{})
		if err != nil {
			t.Fatalf("%s: Run: %v", p.ID, err)
		}
		if res.Model != p.Models[0].ID {
			t.Errorf("%s: model = %q, want %q", p.ID, res.Model, p.Models[0].ID)
		}
		// No model of any other provider may appear in the menu.
		for _, other := range Providers() {
			if other.ID == p.ID {
				continue
			}
			for _, m := range other.Models {
				if strings.Contains(out, m.ID) {
					t.Errorf("%s: the menu offers %s, from %s", p.ID, m.ID, other.ID)
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
	if !strings.Contains(string(cred), "export STARLIGHT_LLM_API_KEY=") {
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
	if !strings.Contains(out, "export STARLIGHT_LLM_API_KEY=") {
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
	script := line + "\nprintf '%s' \"$STARLIGHT_LLM_API_KEY\"\n"
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
	preset := Answers{
		Provider:      "gemini",
		Model:         "gemini-2.5-pro",
		BaseURL:       "https://example.invalid/v1",
		AnchorCommand: "true",
	}
	// The only question left is the key.
	out, res, err := run(context.Background(), t, dir, []string{""}, preset)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out, "Which provider") || strings.Contains(out, "Which model") {
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
	implemented := map[string]bool{"openai": true, "anthropic": true, "gemini": true}
	for _, p := range Providers() {
		if !implemented[p.ID] {
			t.Errorf("the wizard offers %q, which the client does not implement", p.ID)
		}
		if p.Name == "" || p.DefaultBaseURL == "" || p.EnvKey == "" || p.ConsoleURL == "" {
			t.Errorf("%q is incomplete: %+v", p.ID, p)
		}
		if len(p.Models) == 0 {
			t.Errorf("%q offers no model", p.ID)
		}
	}
	if len(Providers()) != len(implemented) {
		t.Errorf("the client implements %d providers and the wizard offers %d",
			len(implemented), len(Providers()))
	}
}

func TestNamesAndHelpers(t *testing.T) {
	if got := Names(); got != "anthropic, gemini, openai" {
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
	if got := EnvKey("nope"); got != "STARLIGHT_LLM_API_KEY" {
		t.Errorf("EnvKey fallback = %q", got)
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
		if strings.HasPrefix(e.Name(), ".starlight-") {
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

// TestEnvKeyOfAnUnknownProviderFallsBack: the caller may ask before validating the
// id, so it must answer with the variable that always exists.
func TestEnvKeyOfAnUnknownProviderFallsBack(t *testing.T) {
	// The known providers answer with their own variable...
	for _, p := range Providers() {
		if got := EnvKey(p.ID); got != p.EnvKey {
			t.Errorf("EnvKey(%q) = %q, want %q", p.ID, got, p.EnvKey)
		}
	}
	// ...and an unknown one gets the variable that always exists.
	if got := EnvKey("whatever"); got != "STARLIGHT_LLM_API_KEY" {
		t.Errorf("EnvKey = %q", got)
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
