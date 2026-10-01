package onboard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/oauth"
)

// The review is the last step of an interactive run: nothing is written until the user accepts
// it, and "n" goes through the questions again instead of forcing a cancel and a restart.
func TestTheReviewCanStartOver(t *testing.T) {
	dir := t.TempDir()
	// First pass: anthropic with a key, then "n" at the review; second pass: openai, accepted.
	answers := []string{
		"anthropic", "sk-first-pass-key", "", "3", "maybe", "n",
		"openai", "", "", "", "3", "y",
	}
	out, res, err := run(context.Background(), t, dir, answers, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != "openai" {
		t.Fatalf("provider = %q, want the second pass", res.Provider.ID)
	}
	plain := stripANSI(out)
	for _, want := range []string{"Review and save", "API key sk-…-key", "Answer y to save", "let's go through it again"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the transcript lacks %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "sk-first-pass-key") {
		t.Error("the review must mask the key")
	}
}

func TestTheReviewGivesUpAfterThreeAttempts(t *testing.T) {
	_, _, err := run(context.Background(), t, t.TempDir(), []string{"openai", "", "", "", "3", "a", "b", "c"}, Answers{})
	if err == nil || !strings.Contains(err.Error(), "review") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheEndOfTheInputAtTheReviewWritesNothing(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "", "", "", "3"}, Answers{})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(statErr) {
		t.Error("nothing may be written before the review is accepted")
	}
}

// Running the wizard again keeps the previous file as .bak, and says so in the review.
func TestTheReplacedConfigurationIsKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("# hand-tuned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, res, err := run(context.Background(), t, dir, []string{"openai", "", "", "", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BackupPath != path+".bak" {
		t.Fatalf("backup = %q", res.BackupPath)
	}
	if got, _ := os.ReadFile(res.BackupPath); string(got) != "# hand-tuned\n" {
		t.Errorf("the backup holds %q", got)
	}
	if !strings.Contains(stripANSI(out), "replaces the current one") {
		t.Error("the review must say the file is replaced")
	}
}

func TestAFailedBackupStopsTheWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := writeFileAtomicFn
	writeFileAtomicFn = func(p string, c []byte) error {
		if strings.HasSuffix(p, ".bak") {
			return errors.New("disk full")
		}
		return old(p, c)
	}
	t.Cleanup(func() { writeFileAtomicFn = old })
	_, _, err := run(context.Background(), t, dir, nil, Answers{Provider: "openai", Model: "m", AnchorCommand: "true", APIKey: "k"})
	if err == nil || !strings.Contains(err.Error(), "copy of the current configuration") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Error("the configuration must be left alone when its copy cannot be made")
	}
}

// A key saved for the previous setup is removed when the new one has none: the loader would
// otherwise read it for a provider it was never given to.
func TestAStaleKeyIsRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cred := credentialsPathFor(path)
	if err := os.WriteFile(cred, []byte("export MOTITA_LLM_API_KEY='old'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(context.Background(), t, dir, nil, Answers{Provider: "claude-code", Model: "sonnet", AnchorCommand: "true"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(cred); !os.IsNotExist(err) {
		t.Errorf("the stale key must be removed: %v", err)
	}
}

func TestAStaleKeyThatCannotBeRemovedIsReported(t *testing.T) {
	old := removeFile
	removeFile = func(string) error { return errors.New("read-only") }
	t.Cleanup(func() { removeFile = old })
	out, _, err := run(context.Background(), t, t.TempDir(), nil, Answers{Provider: "claude-code", Model: "sonnet", AnchorCommand: "true"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stripANSI(out), "could not be removed") {
		t.Errorf("the failure must be said:\n%s", stripANSI(out))
	}
}

// writeOldSetup writes a configuration naming provider and a credentials file holding vars.
func writeOldSetup(t *testing.T, dir, provider, creds string) {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, renderConfig(configValues{provider: provider, model: "m", baseURL: DefaultBaseURL(provider), anchorAuto: true}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialsPathFor(path), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Running the wizard again to change the model must not mean pasting the key again.
func TestTheSavedKeyIsOfferedBack(t *testing.T) {
	dir := t.TempDir()
	writeOldSetup(t, dir, "openai", "export MOTITA_LLM_API_KEY='sk-kept-key-123'\n")
	// provider, endpoint, sign-in (1 = keep the saved key), model, check, save
	out, res, err := run(context.Background(), t, dir, []string{"openai", "", "", "2", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CredentialsPath == "" {
		t.Fatal("the kept key must be written again")
	}
	if cred, _ := os.ReadFile(res.CredentialsPath); !strings.Contains(string(cred), "sk-kept-key-123") {
		t.Errorf("credentials = %s", cred)
	}
	if !strings.Contains(stripANSI(out), "(the one already saved)") {
		t.Errorf("the review must say the key is the saved one:\n%s", stripANSI(out))
	}
}

func TestStoredKeyBelongsToItsProvider(t *testing.T) {
	cases := []struct {
		name, oldProvider, creds, provider, want string
	}{
		{"own variable", "gemini", "export GEMINI_API_KEY='g'\n", "gemini", "g"},
		{"generic, same account", "codex", "export MOTITA_LLM_API_KEY='sk'\n", "openai", "sk"},
		{"generic, another vendor", "anthropic", "export MOTITA_LLM_API_KEY='sk'\n", "openai", ""},
		{"nothing stored", "openai", "", "openai", ""},
		{"no variable at all", "openai", "export MOTITA_LLM_API_KEY='sk'\n", "claude-code", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeOldSetup(t, dir, tc.oldProvider, tc.creds)
			s := &session{configPath: filepath.Join(dir, "config.yaml")}
			p, _ := Lookup(tc.provider)
			if got := s.storedKey(p); got != tc.want {
				t.Errorf("storedKey = %q, want %q", got, tc.want)
			}
		})
	}
	// An old file that does not load offers nothing for the shared variable.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("llm: [not, a, map\n"), 0o600)
	os.WriteFile(credentialsPathFor(path), []byte("export MOTITA_LLM_API_KEY='sk'\n"), 0o600)
	p, _ := Lookup("openai")
	if got := (&session{configPath: path}).storedKey(p); got != "" {
		t.Errorf("an unreadable old setup must offer no key, got %q", got)
	}
}

// A provider signed in already offers to keep that login.
func TestTheCurrentSignInIsOfferedBack(t *testing.T) {
	dir := t.TempDir()
	old := authDir
	authDir = func() string { return dir }
	t.Cleanup(func() { authDir = old })
	if err := oauth.SaveCredential(dir, oauth.Credential{Provider: "qwen", AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	called := false
	oldRunner := directAuthRunner
	directAuthRunner = func(context.Context, io.Writer, *bufio.Reader, Provider) (string, error) {
		called = true
		return "", nil
	}
	t.Cleanup(func() { directAuthRunner = oldRunner })

	out, res, err := run(context.Background(), t, t.TempDir(), []string{"qwen", "", "", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called || !res.LoggedIn {
		t.Errorf("keeping the login must not sign in again: called=%v res=%+v", called, res)
	}
	if !strings.Contains(stripANSI(out), "stored login") {
		t.Errorf("the review must describe the login:\n%s", stripANSI(out))
	}
}

// A sign-in that fails is said, and the menu is offered again: a closed browser is not a reason
// to lose every answer given so far.
func TestAFailedSignInIsOfferedAgain(t *testing.T) {
	calls := 0
	old := directAuthRunner
	directAuthRunner = func(context.Context, io.Writer, *bufio.Reader, Provider) (string, error) {
		calls++
		return "", errors.New("the code expired")
	}
	t.Cleanup(func() { directAuthRunner = old })

	// sign in (fails), then paste a key instead.
	out, res, err := run(context.Background(), t, t.TempDir(), []string{"gemini", "1", "2", "AIza-key-0123456", "", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 || res.CredentialsPath == "" {
		t.Errorf("calls=%d res=%+v", calls, res)
	}
	if !strings.Contains(stripANSI(out), "The sign-in did not finish: the code expired") {
		t.Errorf("the failure must be said:\n%s", stripANSI(out))
	}

	// Three failures end the step.
	if _, _, err := run(context.Background(), t, t.TempDir(), []string{"gemini", "1", "1", "1"}, Answers{}); err == nil || !strings.Contains(err.Error(), "no sign-in") {
		t.Errorf("err = %v", err)
	}
}

// Ctrl+C during a sign-in is a cancellation, not a failure to retry.
func TestACancelledSignInCancelsTheWizard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	old := directAuthRunner
	directAuthRunner = func(context.Context, io.Writer, *bufio.Reader, Provider) (string, error) {
		cancel()
		return "", fmt.Errorf("waiting: %w", context.Canceled)
	}
	t.Cleanup(func() { directAuthRunner = old })
	if _, _, err := run(ctx, t, t.TempDir(), []string{"gemini", "1"}, Answers{}); !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

// A failure of the terminal itself while a key is pasted is not something a retry fixes.
func TestAReadFailureWhilePastingAKeyIsReported(t *testing.T) {
	dir := t.TempDir()
	in := &failingReader{remaining: 2} // provider, sign-in choice; the key read fails
	var out strings.Builder
	_, err := Run(context.Background(), in, &out, filepath.Join(dir, "c.yaml"), Answers{}, fixedTime())
	if err == nil || errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want the read failure", err)
	}
}

func TestSignInDescriptions(t *testing.T) {
	anthropic, _ := Lookup("anthropic")
	for _, tc := range []struct {
		st   setup
		want string
	}{
		{setup{provider: anthropic, loggedIn: true}, "stored login"},
		{setup{provider: anthropic, keyless: true}, "none needed"},
		{setup{provider: anthropic, key: "sk-0123456789abc"}, "API key sk-…9abc"},
		{setup{provider: anthropic, key: "short"}, "API key ••••••"},
		{setup{provider: anthropic}, "no key yet"},
	} {
		if got := stripANSI(signInDescription(tc.st)); !strings.Contains(got, tc.want) {
			t.Errorf("signInDescription = %q, want %q", got, tc.want)
		}
	}
}

func TestTheSummaryForEachWayIn(t *testing.T) {
	ollama, _ := Lookup("ollama")
	openai, _ := Lookup("openai")
	for _, tc := range []struct {
		res  Result
		want string
	}{
		{Result{Provider: ollama, Model: "llama3", Keyless: true}, "ollama pull llama3"},
		{Result{Provider: openai, LoggedIn: true}, "renewed automatically"},
		{Result{Provider: openai, CredentialsPath: "/x.env"}, "reads it on its own"},
	} {
		var b strings.Builder
		printSummary(&b, tc.res)
		if !strings.Contains(stripANSI(b.String()), tc.want) {
			t.Errorf("summary lacks %q:\n%s", tc.want, stripANSI(b.String()))
		}
	}
}

func TestTheModelMenuSaysWhereTheLiveListCameFrom(t *testing.T) {
	stubOllamaModels(t, []string{"a", "b"})
	p, _ := Lookup("ollama")
	var out strings.Builder
	s := &session{in: bufio.NewReader(strings.NewReader("\n")), out: &out}
	if _, err := s.chooseModel(context.Background(), p, "", "http://h/v1", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripANSI(out.String()), "available at http://h/v1") {
		t.Errorf("out:\n%s", stripANSI(out.String()))
	}
}

// A read failure after choosing to paste a key, on a provider that also offers a sign-in, is a
// failure of the terminal: it is reported, not offered again as a sign-in to retry.
func TestAReadFailureAfterChoosingToPasteIsNotRetried(t *testing.T) {
	in := io.MultiReader(strings.NewReader("gemini\n2\n"), &failingReader{})
	var out strings.Builder
	_, err := Run(context.Background(), in, &out, filepath.Join(t.TempDir(), "c.yaml"), Answers{}, fixedTime())
	if err == nil || strings.Contains(err.Error(), "sign-in") {
		t.Fatalf("err = %v, want the read failure itself", err)
	}
}

func TestAModelNumberOutsideTheMenuIsRefused(t *testing.T) {
	out, res, err := run(context.Background(), t, t.TempDir(), []string{"openai", "", "", "99", "2", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "There is no option 99") || res.Model != "gpt-5" {
		t.Errorf("model = %q\n%s", res.Model, stripANSI(out))
	}
}

func TestAnUnreachableCatalogueIsExplained(t *testing.T) {
	if got := unreachable(errors.New(`Get "http://localhost:11434/v1/models": dial tcp 127.0.0.1:11434: connect: connection refused`)); !strings.Contains(got, "ollama serve") {
		t.Errorf("unreachable = %q", got)
	}
	if got := unreachable(errors.New("HTTP 401")); got != "HTTP 401" {
		t.Errorf("any other failure is reported as it is, got %q", got)
	}
}
