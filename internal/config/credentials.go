package config

import (
	"os"
	"path/filepath"
	"strings"
)

// CredentialsPath is the file the first-run wizard keeps the key in: beside the configuration,
// with the same base name and the .env extension (~/.motita/motita.env next to
// ~/.motita/motita.yaml).
//
// It is exported because two packages have to agree on it: the wizard writes it and the loader
// reads it. A second derivation of the name is how the key ended up written to one file and looked
// for in none.
func CredentialsPath(configPath string) string {
	return strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".env"
}

// credentialKey is the key the credentials file beside a configuration holds for its provider,
// or "" when there is no file or no key in it.
//
// This is what makes the wizard's promise true. It used to write the key to motita.env and then
// tell the user to `source` the file - and the very next thing it did, on a first run, was start
// the interface in the same process, where nothing had been sourced: the first screen a new user
// saw after pasting their key was "the LLM key is missing". The file is now read the way the YAML
// is, as the value the environment overrides, so an exported variable still wins over it.
//
// The provider's own variable comes first, then the generic MOTITA_LLM_API_KEY, which belongs to
// whichever provider the configuration names - the same precedence the environment has.
func credentialKey(configPath, provider string) string {
	creds := readCredentials(CredentialsPath(configPath))
	if v := creds[ProviderKeyVariable(provider)]; v != "" {
		return v
	}
	return creds["MOTITA_LLM_API_KEY"]
}

// homeCredentialKey is the key the motita home's credentials file holds under a provider's OWN
// variable, for a switch to that provider. The generic variable is deliberately not consulted,
// for the reason ProviderKeyFromEnv gives: it belongs to the configured provider, and handing it
// to another one sends one vendor's key to another vendor.
func homeCredentialKey(provider string) string {
	name := ProviderKeyVariable(provider)
	if name == "MOTITA_LLM_API_KEY" || File() == "" {
		return ""
	}
	return readCredentials(CredentialsPath(File()))[name]
}

// readCredentials parses the file the wizard writes: comments, blank lines and
// `export NAME='value'` lines. A missing or unreadable file is an empty map - having no stored key
// is the ordinary case, not an error.
//
// It is NOT a shell. The file is meant to be sourced by one, so it accepts what the wizard writes
// and the obvious hand edits of it (no `export`, double quotes, a bare value), and nothing is ever
// executed or expanded.
func readCredentials(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(unquoteShell(strings.TrimSpace(value)))
	}
	return out
}

// unquoteShell reads one shell word the way a POSIX shell would for the quoting the wizard
// produces: single-quoted runs are literal, double-quoted runs honour \" and \\, and a
// backslash outside quotes escapes the next character - so the wizard's quoting of a key with an
// apostrophe in it reads back as the key it wrote.
func unquoteShell(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				b.WriteString(s[i+1:])
				return b.String()
			}
			b.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
					i++
				}
				b.WriteByte(s[i])
			}
		case '\\':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// SecretFiles are the files and directories of motita's that hold secrets, for the sandbox to
// keep from the commands it confines: the logins, and the configuration with the key file beside
// it, both the motita home's and, when another one was loaded, that one. A configuration can hold
// llm.api_key and the gateway's tokens, so it counts as one. A path that cannot be made absolute
// comes back empty and is ignored by the sandbox.
func SecretFiles(loaded string) []string {
	files := []string{AuthDir(), File(), CredentialsPath(File())}
	if loaded != "" {
		abs, _ := filepath.Abs(loaded)
		files = append(files, abs, CredentialsPath(abs))
	}
	return files
}

// StoredCredentials is every variable the credentials file beside a configuration holds, for the
// wizard: a user who runs it again to change the model should not have to paste their key again,
// and the wizard can only offer to keep a key it can see.
func StoredCredentials(configPath string) map[string]string {
	return readCredentials(CredentialsPath(configPath))
}
