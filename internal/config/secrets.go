package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// THE VARIABLES THE PERSON KEEPS FOR THE AGENT.
//
// Settings → Secrets stores NAME=value pairs (a deploy token, a database URL) that every command
// the agent runs receives as environment variables. They live in their own owner-only file beside
// the configuration, apart from the provider keys in the credentials file, and the sandbox keeps
// that file out of reach of a confined command: the command gets the values it was given, not the
// means to list the rest. The gateway never returns a value, only the name and the last characters.

// secretName is what a stored variable may be called.
var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// reservedSecrets are variables a command needs from the sandbox itself: overriding one would break
// the isolation or the tools, so they cannot be used as a secret's name.
var reservedSecrets = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "TEMP": true, "TMP": true, "SHELL": true,
	"USER": true, "LANG": true, "TERM": true, "PWD": true, "IFS": true, "GNUPGHOME": true,
}

// MaxSecrets and MaxSecretBytes bound what the page can store.
const (
	MaxSecrets     = 64
	MaxSecretBytes = 8 << 10
)

// SecretsPath is the file the person's variables are kept in: beside the configuration, with the
// .secrets.env extension (~/.motita/motita.secrets.env next to ~/.motita/motita.yaml).
func SecretsPath(configPath string) string {
	return strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".secrets.env"
}

// CheckSecretName says why name cannot be a secret's name, or "".
func CheckSecretName(name string) string {
	switch {
	case !secretName.MatchString(name):
		return "a name is letters, digits and _, not starting with a digit, up to 64 long"
	case reservedSecrets[strings.ToUpper(name)], strings.HasPrefix(strings.ToUpper(name), "LD_"),
		strings.HasPrefix(strings.ToUpper(name), "MOTITA_"):
		return name + " is reserved for the sandbox"
	}
	return ""
}

// ReadSecrets is every variable the secrets file beside configPath holds.
func ReadSecrets(configPath string) map[string]string {
	if configPath == "" {
		return nil
	}
	return readCredentials(SecretsPath(configPath))
}

// SecretNames is the names held in secrets, sorted.
func SecretNames(secrets map[string]string) []string {
	names := make([]string, 0, len(secrets))
	for n := range secrets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SetSecret writes name=value into the secrets file beside configPath, in place; an empty value
// removes the variable. The file is owner-only.
func SetSecret(configPath, name, value string) error {
	if configPath == "" {
		return fmt.Errorf("there is no configuration file to keep the variables beside")
	}
	if msg := CheckSecretName(name); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("the value of %s cannot span lines", name)
	}
	if len(value) > MaxSecretBytes {
		return fmt.Errorf("the value of %s is longer than %d bytes", name, MaxSecretBytes)
	}
	path := SecretsPath(configPath)
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("the variables file cannot be written: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("the variables file cannot be read: %w", err)
	}
	have := readCredentials(path)
	if _, exists := have[name]; !exists && value != "" && len(have) >= MaxSecrets {
		return fmt.Errorf("at most %d variables can be kept", MaxSecrets)
	}
	return writeConfigAtomic(path, []byte(setCredentialText(string(body), name, value)), 0o600)
}

// UseSecretsBeside makes the secrets file beside configPath the one commands get their variables
// from, besides the motita home's. The gateway calls it once, with the file it was started with.
func UseSecretsBeside(configPath string) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	sharedSecrets = ""
	if configPath != "" {
		sharedSecrets = SecretsPath(configPath)
	}
}

// sharedSecrets is the secrets file of the configuration the gateway runs on.
var sharedSecrets string

// CurrentSecrets is every variable a command gets: the gateway's file first, then the motita
// home's, a name in both keeping the first.
func CurrentSecrets() map[string]string {
	sharedMu.Lock()
	shared := sharedSecrets
	sharedMu.Unlock()
	out := map[string]string{}
	paths := []string{shared}
	if f := File(); f != "" {
		paths = append(paths, SecretsPath(f))
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		for n, v := range readCredentials(p) {
			if _, ok := out[n]; !ok && v != "" && CheckSecretName(n) == "" {
				out[n] = v
			}
		}
	}
	return out
}
