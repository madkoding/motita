package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/oauth"
)

// clearKeyEnv unsets every variable a provider key can come from, so a test reads only the files.
func clearKeyEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"MOTITA_LLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY",
		"DASHSCOPE_API_KEY", "OLLAMA_API_KEY", "GITHUB_COPILOT_TOKEN"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	t.Setenv("MOTITA_AUTH_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	UseCredentialsBeside("")
	t.Cleanup(func() { UseCredentialsBeside("") })
}

func TestProviderStoreAndBaseURLVariables(t *testing.T) {
	for provider, want := range map[string]string{
		"openai": "OPENAI_API_KEY", "codex": "CODEX_API_KEY", "anthropic": "ANTHROPIC_API_KEY",
		"ollama": "MOTITA_OLLAMA_API_KEY", "ollama-cloud": "OLLAMA_API_KEY", "claude-code": "",
	} {
		if got := ProviderStoreVariable(provider); got != want {
			t.Errorf("%s: store variable %q, want %q", provider, got, want)
		}
	}
	if got := ProviderBaseURLVariable(" claude-code "); got != "MOTITA_CLAUDE_CODE_BASE_URL" {
		t.Errorf("base url variable = %q", got)
	}
}

func TestSetCredentialEditsOneLineAndKeepsTheRest(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	if err := SetCredential(cfg, "ANTHROPIC_API_KEY", "sk-'ant"); err != nil {
		t.Fatal(err)
	}
	path := CredentialsPath(cfg)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the credentials file must be owner-only: %v %v", info, err)
	}
	if got := readCredentials(path)["ANTHROPIC_API_KEY"]; got != "sk-'ant" {
		t.Errorf("read back %q", got)
	}
	body, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(body), "# Written by motita.") {
		t.Errorf("a new file says what it is:\n%s", body)
	}

	// A hand-written file: its comment and its other variables stay, a duplicate line goes.
	hand := "# mine\nexport OPENAI_API_KEY='sk-a'\nGEMINI_API_KEY=g\nexport OPENAI_API_KEY='sk-old'\n"
	if err := os.WriteFile(path, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetCredential(cfg, "OPENAI_API_KEY", "sk-new"); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	want := "# mine\nexport OPENAI_API_KEY='sk-new'\nGEMINI_API_KEY=g\n"
	if string(body) != want {
		t.Errorf("got\n%s\nwant\n%s", body, want)
	}
	if err := SetCredential(cfg, "GEMINI_API_KEY", ""); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	if strings.Contains(string(body), "GEMINI") || !strings.Contains(string(body), "sk-new") {
		t.Errorf("an empty value removes only its line:\n%s", body)
	}
	// Removing a variable the file does not hold changes nothing.
	if err := SetCredential(cfg, "QWEN", ""); err != nil {
		t.Fatal(err)
	}
}

func TestSetCredentialRefusals(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "motita.yaml")
	if SetCredential("", "A", "b") == nil {
		t.Error("no configuration file, nowhere to write")
	}
	if SetCredential(cfg, "lower", "b") == nil || SetCredential(cfg, "A=B", "b") == nil {
		t.Error("a name that is not a variable is refused")
	}
	if SetCredential(cfg, "A", "x\ny") == nil {
		t.Error("a value with a line break would add a line")
	}
	// The directory cannot be made: a file stands where it would be.
	blocker := filepath.Join(dir, "blocker")
	_ = os.WriteFile(blocker, nil, 0o600)
	if SetCredential(filepath.Join(blocker, "motita.yaml"), "A", "b") == nil {
		t.Error("a directory that cannot be made is an error")
	}
	// The credentials path is a directory: it cannot be read.
	if err := os.Mkdir(CredentialsPath(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if SetCredential(cfg, "A", "b") == nil {
		t.Error("an unreadable file is an error")
	}
}

func TestCredentialLineName(t *testing.T) {
	for line, want := range map[string]string{
		"# export A=1": "", "nothing": "", "export A=1": "A", " B = 2": "B",
	} {
		if got := credentialLineName(line); got != want {
			t.Errorf("%q: %q", line, got)
		}
	}
}

func TestStoredValuesComeFromTheGatewayFileThenTheHome(t *testing.T) {
	clearKeyEnv(t)
	home := os.Getenv("HOME")
	if err := SetCredential(File(), "ANTHROPIC_API_KEY", "home-key"); err != nil {
		t.Fatal(err)
	}
	if err := SetCredential(File(), "OPENAI_API_KEY", "home-openai"); err != nil {
		t.Fatal(err)
	}
	if got := homeCredentialKey("anthropic"); got != "home-key" {
		t.Errorf("home key = %q", got)
	}
	if got := ProviderKeyFromEnv("openai"); got != "home-openai" {
		t.Errorf("openai reads OPENAI_API_KEY from the file: %q", got)
	}
	if got := ProviderKeyFromEnv("codex"); got != "" {
		t.Errorf("codex must not read OpenAI's key from the file: %q", got)
	}
	if homeCredentialKey("claude-code") != "" {
		t.Error("claude-code takes no key")
	}

	other := filepath.Join(t.TempDir(), "other.yaml")
	if err := SetCredential(other, "ANTHROPIC_API_KEY", "gateway-key"); err != nil {
		t.Fatal(err)
	}
	if err := SetCredential(other, ProviderBaseURLVariable("ollama"), "http://box:11434/v1"); err != nil {
		t.Fatal(err)
	}
	UseCredentialsBeside(other)
	if got := homeCredentialKey("anthropic"); got != "gateway-key" {
		t.Errorf("the gateway's file comes first: %q", got)
	}
	if got := StoredBaseURL("ollama"); got != "http://box:11434/v1" {
		t.Errorf("stored base url = %q", got)
	}
	if len(credentialFiles()) != 2 {
		t.Errorf("files = %v", credentialFiles())
	}
	UseCredentialsBeside(File())
	if files := credentialFiles(); len(files) != 1 {
		t.Errorf("the home's file is read once: %v", files)
	}
	t.Setenv("HOME", "")
	restore := osUserHomeDir
	osUserHomeDir = func() (string, error) { return "", os.ErrNotExist }
	defer func() { osUserHomeDir = restore }()
	UseCredentialsBeside("")
	if len(credentialFiles()) != 0 || StoredBaseURL("ollama") != "" {
		t.Error("no home and no gateway file: nothing is stored")
	}
	_ = home
}

func TestCredentialKeyReadsTheStoreVariable(t *testing.T) {
	clearKeyEnv(t)
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	if err := SetCredential(cfg, "OPENAI_API_KEY", "sk-web"); err != nil {
		t.Fatal(err)
	}
	if got := credentialKey(cfg, "openai"); got != "sk-web" {
		t.Errorf("credentialKey = %q", got)
	}
}

func TestKeySourceNamesWhereTheKeyComesFrom(t *testing.T) {
	clearKeyEnv(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "motita.yaml")
	if err := os.WriteFile(cfg, []byte("llm:\n  provider: openai\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	openai := LLM{Provider: "openai"}
	check := func(provider, want string) {
		t.Helper()
		if got := KeySource(cfg, openai, provider); got != want {
			t.Errorf("%s: %q, want %q", provider, got, want)
		}
	}
	check("claude-code", KeyNotNeeded)
	check("openai", KeyMissing)
	check("anthropic", KeyMissing)

	// The generic variable in the file belongs to the configured provider only.
	if err := SetCredential(cfg, genericKeyVariable, "sk-gen"); err != nil {
		t.Fatal(err)
	}
	check("openai", KeyStored)
	check("codex", KeyMissing)
	if err := SetCredential(cfg, "ANTHROPIC_API_KEY", "sk-ant"); err != nil {
		t.Fatal(err)
	}
	check("anthropic", KeyStored)

	t.Setenv("MOTITA_LLM_API_KEY", "env-gen")
	check("openai", KeyEnv)
	check("codex", KeyMissing)
	t.Setenv("OPENAI_API_KEY", "env-openai")
	check("codex", KeyMissing)
	t.Setenv("CODEX_API_KEY", "env-codex")
	check("codex", KeyEnv)
	os.Unsetenv("CODEX_API_KEY")
	os.Unsetenv("MOTITA_LLM_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")

	if err := os.WriteFile(cfg, []byte("llm:\n  provider: openai\n  api_key: inline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("openai", KeyConfig)

	cred := oauth.Credential{Provider: "qwen", RefreshToken: "r"}
	if err := oauth.SaveCredential(os.Getenv("MOTITA_AUTH_DIR"), cred); err != nil {
		t.Fatal(err)
	}
	check("qwen", KeyLogin)

	check("ollama", KeyMissing)
	if KeySource("", LLM{Provider: "ollama", BaseURL: "http://localhost:11434"}, "ollama") != KeyNotNeeded {
		t.Error("the configured self-hosted ollama needs no key")
	}
	UseCredentialsBeside(cfg)
	if err := SetCredential(cfg, ProviderBaseURLVariable("ollama"), "http://box.lan/v1"); err != nil {
		t.Fatal(err)
	}
	check("ollama", KeyNotNeeded)
	if KeySource("", openai, "anthropic") != KeyStored {
		t.Error("without a configuration file the shared files still count")
	}
	if KeySource("", openai, "gemini") != KeyMissing {
		t.Error("gemini has nothing stored")
	}
}

func TestYAMLAPIKey(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if YAMLAPIKey("") != "" || YAMLAPIKey(filepath.Join(dir, "none")) != "" {
		t.Error("no file, no key")
	}
	if YAMLAPIKey(write("llm: [\n")) != "" {
		t.Error("a file that does not parse has no key")
	}
	if YAMLAPIKey(write("llm:\n  max_tokens: many\n")) != "" {
		t.Error("a file that does not decode has no key")
	}
	if got := YAMLAPIKey(write("llm:\n  api_key: k\n")); got != "k" {
		t.Errorf("key = %q", got)
	}
}

func TestYAMLQuoted(t *testing.T) {
	for in, want := range map[string]string{
		"gpt-5":                   "gpt-5",
		"https://ollama.com/v1":   "https://ollama.com/v1",
		"qwen3.5:397b":            "qwen3.5:397b",
		"":                        `""`,
		"a b":                     `"a b"`,
		`say "hi" \o/`:            `"say \"hi\" \\o/"`,
		"-dash":                   `"-dash"`,
		"key: value":              `"key: value"`,
		"models/gemini-2.5-flash": "models/gemini-2.5-flash",
	} {
		if got := yamlQuoted(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

func TestSetLLMDefaultEditsTheLLMBlockInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "motita.yaml")
	body := "# top\nllm:\n  provider: openai   # who\n  model: gpt-5-mini\n  base_url: https://api.openai.com/v1\n" +
		"  reasoning:\n    level: low\n  max_tokens: 100\n\nagent:\n  max_retries: 3\n"
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SetLLMDefault(path, "anthropic", "claude-sonnet-5-5", "https://api.anthropic.com"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "# top\nllm:\n  provider: anthropic   # who\n  model: claude-sonnet-5-5\n" +
		"  base_url: https://api.anthropic.com\n" +
		"  reasoning:\n    level: low\n  max_tokens: 100\n\nagent:\n  max_retries: 3\n"
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", info.Mode())
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil || cfg.LLM.Provider != "anthropic" || cfg.LLM.Model != "claude-sonnet-5-5" {
		t.Errorf("the file must still load: %v %+v", err, cfg.LLM)
	}
}

func TestSetBlockValuesAddsWhatIsMissing(t *testing.T) {
	kv := [][2]string{{"provider", "x"}, {"model", "m"}}
	cases := map[string]string{
		"":                              "llm:\n  provider: x\n  model: m\n",
		"ui:\n  language: en\n":         "ui:\n  language: en\n\nllm:\n  provider: x\n  model: m\n",
		"llm:\n    model: old\n":        "llm:\n    provider: x\n    model: m\n",
		"llm:\n# c\n\nagent:\n  a: 1\n": "llm:\n  provider: x\n  model: m\n# c\n\nagent:\n  a: 1\n",
		"llm: # note\n  provider: y":    "llm: # note\n  model: m\n  provider: x",
	}
	for in, want := range cases {
		got, err := setBlockValues(in, "llm", kv)
		if err != nil || got != want {
			t.Errorf("%q:\ngot  %q (%v)\nwant %q", in, got, err, want)
		}
	}
	if _, err := setBlockValues("llm: {provider: x}\n", "llm", kv); err == nil {
		t.Error("an inline block cannot be edited one line at a time")
	}
}

func TestSetLLMDefaultFailures(t *testing.T) {
	dir := t.TempDir()
	if SetLLMDefault(filepath.Join(dir, "none.yaml"), "openai", "m", "") == nil {
		t.Error("a missing file is an error")
	}
	if SetLLMDefault(dir, "openai", "m", "") == nil {
		t.Error("a directory cannot be read as the file")
	}
	path := filepath.Join(dir, "inline.yaml")
	_ = os.WriteFile(path, []byte("llm: {provider: x}\n"), 0o600)
	if SetLLMDefault(path, "openai", "m", "") == nil {
		t.Error("an inline block is refused")
	}
	path = filepath.Join(dir, "ok.yaml")
	_ = os.WriteFile(path, []byte("llm:\n  provider: x\n"), 0o600)
	restore := uiCreateTemp
	uiCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk full") }
	defer func() { uiCreateTemp = restore }()
	if SetLLMDefault(path, "openai", "m", "") == nil {
		t.Error("a failed write is reported")
	}
}

func TestProviderKeyFollowsTheOrderKeySourceDoes(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "")
	cfgPath := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(cfgPath, []byte("llm:\n  provider: ollama-cloud\n  api_key: yaml-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	same := LLM{Provider: "ollama-cloud"}
	if got := ProviderKey(cfgPath, same, "ollama-cloud"); got != "yaml-key" {
		t.Errorf("the configuration's key for its provider: %q", got)
	}
	t.Setenv("OLLAMA_API_KEY", "env-key")
	if got := ProviderKey(cfgPath, same, "ollama-cloud"); got != "env-key" {
		t.Errorf("an exported variable wins: %q", got)
	}
	t.Setenv("OLLAMA_API_KEY", "")
	if err := SetCredential(cfgPath, ProviderStoreVariable("openai"), "stored"); err != nil {
		t.Fatal(err)
	}
	if got := ProviderKey(cfgPath, same, "openai"); got != "stored" {
		t.Errorf("a stored key for another provider: %q", got)
	}
	if got := ProviderKey("", LLM{}, "gemini"); got != "" {
		t.Errorf("no key: %q", got)
	}
}

// A setup made before Codex and Ollama had keys of their own keeps working for the provider the
// configuration names, and is never lent to the other one.
func TestLegacySharedKeysServeOnlyTheConfiguredProvider(t *testing.T) {
	clearKeyEnv(t)
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(cfg, []byte("llm:\n  provider: codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetCredential(cfg, "OPENAI_API_KEY", "sk-old"); err != nil {
		t.Fatal(err)
	}
	if err := SetCredential(cfg, "OLLAMA_API_KEY", "ol-old"); err != nil {
		t.Fatal(err)
	}
	if got := credentialKey(cfg, "codex"); got != "sk-old" {
		t.Errorf("configured codex = %q, want the legacy key", got)
	}
	if got := credentialKey(cfg, "ollama"); got != "ol-old" {
		t.Errorf("configured ollama = %q, want the legacy key", got)
	}
	if got := storedKey(cfg, "codex", true); got != "sk-old" {
		t.Errorf("stored codex = %q", got)
	}
	if got := storedKey(cfg, "codex", false); got != "" {
		t.Errorf("a codex that is not configured must not read OpenAI's key: %q", got)
	}
	if got := storedKey(cfg, "ollama", false); got != "" {
		t.Errorf("a local ollama that is not configured must not read the cloud's key: %q", got)
	}
	if got := storedKey(cfg, "ollama-cloud", false); got != "ol-old" {
		t.Errorf("ollama-cloud keeps OLLAMA_API_KEY: %q", got)
	}
	t.Setenv("OPENAI_API_KEY", "sk-env")
	t.Setenv("OLLAMA_API_KEY", "ol-env")
	if envKey("codex", true) != "sk-env" || envKey("codex", false) != "" {
		t.Error("codex reads OPENAI_API_KEY only as the configured provider")
	}
	if envKey("ollama", true) != "ol-env" || envKey("ollama", false) != "" {
		t.Error("ollama reads OLLAMA_API_KEY only as the configured provider")
	}
	if legacyKeyVariable("openai") != "" {
		t.Error("openai has no legacy variable")
	}
}
