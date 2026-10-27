package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// THE PROVIDERS A FRONT END CONNECTS.
//
// The browser's settings can connect a provider without the setup wizard: a key and, for the
// providers that are spoken by many hosts, an endpoint. Both go where the wizard already puts the
// key - the credentials file beside the configuration, owner-only - under the provider's OWN
// variable, so a key typed for one vendor can never be read as another vendor's. The choice of the
// default provider and model is written into the configuration itself, one line at a time, the way
// SetUILanguage writes the language.

// Where a provider's key comes from, as KeySource reports it.
const (
	KeyEnv       = "env"        // an exported variable: it wins, and a front end cannot replace it
	KeyStored    = "stored"     // the credentials file beside the configuration
	KeyConfig    = "config"     // llm.api_key written in the configuration file itself
	KeyLogin     = "login"      // a stored login (device code or OAuth) instead of a key
	KeyNotNeeded = "not_needed" // claude-code, or a self-hosted Ollama
	KeyMissing   = "missing"
)

// genericKeyVariable is the variable that belongs to whichever provider the configuration names.
const genericKeyVariable = "MOTITA_LLM_API_KEY"

// sharedCredentials is the credentials file a running gateway reads and writes besides the motita
// home's: the one beside the configuration it was started with, when that is another file.
var (
	sharedMu          sync.Mutex
	sharedCredentials string
)

// UseCredentialsBeside makes the credentials file beside configPath one of the files a provider
// switch reads its key from. The gateway calls it once, with the file it was started with, so a key
// saved from the browser is found whichever configuration the gateway runs on.
func UseCredentialsBeside(configPath string) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	sharedCredentials = ""
	if configPath != "" {
		sharedCredentials = CredentialsPath(configPath)
	}
}

// credentialFiles are the credentials files a provider switch reads, the gateway's first.
func credentialFiles() []string {
	sharedMu.Lock()
	shared := sharedCredentials
	sharedMu.Unlock()
	var out []string
	if shared != "" {
		out = append(out, shared)
	}
	if f := File(); f != "" && CredentialsPath(f) != shared {
		out = append(out, CredentialsPath(f))
	}
	return out
}

// storedValue is the first value a credentials file holds under name.
func storedValue(name string) string {
	for _, path := range credentialFiles() {
		if v := readCredentials(path)[name]; v != "" {
			return v
		}
	}
	return ""
}

// ProviderStoreVariable is the variable a provider's key is stored under: its own one, and
// OPENAI_API_KEY for OpenAI, whose own one is the generic variable. Codex, Ollama and Ollama Cloud
// each have one of their own. Empty for a provider that takes no key.
func ProviderStoreVariable(provider string) string {
	switch {
	case !ProviderNeedsKey(provider):
		return ""
	case speaksOpenAIKey(provider):
		return "OPENAI_API_KEY"
	default:
		return ProviderKeyVariable(provider)
	}
}

// ProviderBaseURLVariable is the variable a provider's endpoint is stored under:
// MOTITA_OLLAMA_BASE_URL for ollama.
func ProviderBaseURLVariable(provider string) string {
	id := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(provider), "-", "_"))
	return "MOTITA_" + id + "_BASE_URL"
}

// StoredBaseURL is the endpoint stored for a provider, or "" when none was.
func StoredBaseURL(provider string) string {
	return storedValue(ProviderBaseURLVariable(provider))
}

// KeySource says where the key a provider runs with comes from, for a gateway started with the
// configuration at configPath whose LLM block is configured. The order is the loader's: an exported
// variable wins over everything, then the configuration's own llm.api_key, then the stored file.
func KeySource(configPath string, configured LLM, provider string) string {
	same := strings.EqualFold(configured.Provider, provider)
	switch {
	case !ProviderNeedsKey(provider):
		return KeyNotNeeded
	case same && IsSelfHostedOllama(configured):
		return KeyNotNeeded
	case !same && IsSelfHostedOllama(LLM{Provider: provider, BaseURL: StoredBaseURL(provider)}):
		return KeyNotNeeded
	case envKey(provider, same) != "":
		return KeyEnv
	case same && YAMLAPIKey(configPath) != "":
		return KeyConfig
	case storedKey(configPath, provider, same) != "":
		return KeyStored
	case HasLogin(provider):
		return KeyLogin
	default:
		return KeyMissing
	}
}

// ProviderKey is the key provider runs with for a gateway started with the configuration at
// configPath whose LLM block is configured, in the order KeySource follows; "" when there is none.
func ProviderKey(configPath string, configured LLM, provider string) string {
	same := strings.EqualFold(configured.Provider, provider)
	if v := envKey(provider, same); v != "" {
		return v
	}
	if same {
		if v := YAMLAPIKey(configPath); v != "" {
			return v
		}
	}
	return storedKey(configPath, provider, same)
}

// envKey is the key an exported variable holds for provider. The generic variable counts only for
// the configured provider, which it belongs to.
func envKey(provider string, configured bool) string {
	if v := strings.TrimSpace(os.Getenv(ProviderKeyVariable(provider))); v != "" &&
		(configured || ProviderKeyVariable(provider) != genericKeyVariable) {
		return v
	}
	if speaksOpenAIKey(provider) {
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if legacy := legacyKeyVariable(provider); legacy != "" && configured {
		return strings.TrimSpace(os.Getenv(legacy))
	}
	return ""
}

// storedKey is the key a credentials file holds for provider: beside the configuration (with the
// generic variable for the configured provider, as the loader reads it), then the shared files.
func storedKey(configPath, provider string, configured bool) string {
	if configPath != "" {
		creds := readCredentials(CredentialsPath(configPath))
		if v := creds[ProviderStoreVariable(provider)]; v != "" {
			return v
		}
		variable := ProviderKeyVariable(provider)
		if v := creds[variable]; v != "" && (configured || variable != genericKeyVariable) {
			return v
		}
		if legacy := legacyKeyVariable(provider); legacy != "" && configured {
			if v := creds[legacy]; v != "" {
				return v
			}
		}
	}
	return homeCredentialKey(provider)
}

// YAMLAPIKey is llm.api_key as the configuration file writes it, before any other source is
// applied: "" when the file has none or cannot be read.
func YAMLAPIKey(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	m, err := ParseYAML(data)
	if err != nil {
		return ""
	}
	cfg := Default()
	if Decode(m, &cfg) != nil {
		return ""
	}
	return cfg.LLM.APIKey
}

var credentialName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// credentialsMu serialises SetCredential's read-modify-write of the credentials file: two saves at
// once would both start from the same text, and the second would erase the first one's key. It is
// not configWriteMu because SetVoice calls SetCredential while it holds that one.
var credentialsMu sync.Mutex

// SetCredential writes name=value into the credentials file beside configPath, IN PLACE: the line
// that holds name is replaced, or one is added, and every other line stays as it was. An empty value
// removes the line. The file is owner-only, as the wizard writes it.
func SetCredential(configPath, name, value string) error {
	if configPath == "" {
		return fmt.Errorf("there is no configuration file to keep the credentials beside")
	}
	if !credentialName.MatchString(name) {
		return fmt.Errorf("%q is not a variable name", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("the value of %s cannot span lines", name)
	}
	path := CredentialsPath(configPath)
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	// The motita home may not exist yet when the first key arrives from the browser.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("the credentials file cannot be written: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("the credentials file cannot be read: %w", err)
	}
	return writeConfigAtomic(path, []byte(setCredentialText(string(body), name, value)), 0o600)
}

// setCredentialText is SetCredential on the text alone.
func setCredentialText(body, name, value string) string {
	line := "export " + name + "=" + shellQuoted(value)
	var out []string
	done := false
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if credentialLineName(l) != name {
			out = append(out, l)
			continue
		}
		if !done && value != "" {
			out = append(out, line)
		}
		done = true
	}
	if !done && value != "" {
		if len(out) == 1 && out[0] == "" {
			out = []string{
				"# Written by motita. This file holds secrets: keep it out of the repository",
				"# and out of any backup you share. motita reads it automatically.",
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// credentialLineName is the variable a line of the credentials file sets, or "".
func credentialLineName(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#") {
		return ""
	}
	name, _, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(name)
}

// shellQuoted is a value as one single-quoted shell word, which readCredentials reads back.
func shellQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var yamlBare = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// yamlQuoted is a value as a YAML scalar: bare when that reads back the same, double-quoted
// otherwise.
func yamlQuoted(s string) string {
	if yamlBare.MatchString(s) && !strings.Contains(s, ": ") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// SetLLMDefault writes the provider, the model and the endpoint into the llm block of the
// configuration at path, in place and atomically, keeping the file's mode and every other line.
func SetLLMDefault(path, provider, model, baseURL string) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	updated, err := setBlockValues(string(body), "llm", [][2]string{
		{"provider", yamlQuoted(provider)},
		{"model", yamlQuoted(model)},
		{"base_url", yamlQuoted(baseURL)},
	})
	if err != nil {
		return err
	}
	return writeConfigAtomic(path, []byte(updated), info.Mode().Perm())
}

// setBlockValues sets keys directly under a top-level block: each existing line keeps its
// indentation and its comment, a missing key is added after the block's opening line, and a
// missing block is added at the end.
func setBlockValues(body, block string, kv [][2]string) (string, error) {
	lines := strings.Split(body, "\n")
	start, inline := findBlockStart(lines, block)
	if inline != "" {
		return "", fmt.Errorf("the configuration writes %s as an inline value (%q): change it by hand", block, inline)
	}
	if start < 0 {
		var b strings.Builder
		if trimmed := strings.TrimRight(body, "\n"); trimmed != "" {
			b.WriteString(trimmed + "\n\n")
		}
		b.WriteString(block + ":\n")
		for _, p := range kv {
			b.WriteString("  " + p[0] + ": " + p[1] + "\n")
		}
		return b.String(), nil
	}
	end, indent := blockExtent(lines, start)
	var missing []string
	for _, p := range kv {
		if !replaceKeyLine(lines[start+1:end], indent, p[0], p[1]) {
			missing = append(missing, indent+p[0]+": "+p[1])
		}
	}
	out := append([]string{}, lines[:start+1]...)
	out = append(out, missing...)
	out = append(out, lines[start+1:]...)
	return strings.Join(out, "\n"), nil
}

// replaceKeyLine sets the first `key:` line at indent in lines to value, keeping its comment, and
// reports whether there was one.
func replaceKeyLine(lines []string, indent, key, value string) bool {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(indent) + regexp.QuoteMeta(key) + `:(\s*)(.*?)(\s+#.*)?$`)
	for i, l := range lines {
		if m := re.FindStringSubmatch(l); m != nil {
			lines[i] = indent + key + ": " + value + m[3]
			return true
		}
	}
	return false
}

// blockExtent is the line after the last one of the block that opens at start, and the
// indentation of its keys.
func blockExtent(lines []string, start int) (int, string) {
	indent := ""
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if t := strings.TrimSpace(l); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if lead == "" {
			return i, keyIndent(indent)
		}
		if indent == "" {
			indent = lead
		}
	}
	return len(lines), keyIndent(indent)
}

// keyIndent is the block's indentation, two spaces when it has no key yet.
func keyIndent(indent string) string {
	if indent == "" {
		return "  "
	}
	return indent
}
