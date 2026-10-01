package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Credential is what a direct login leaves behind for one provider: the token
// the LLM client sends, the token that renews it, and the few facts some
// providers need on every request (the ChatGPT account, the Qwen resource host,
// the Google quota project).
//
// It is stored as JSON with 0600 permissions, one file per provider, and never
// in the configuration: the configuration is meant to be shareable, and a
// credential that renews itself cannot live in a file the user sources once.
type Credential struct {
	Provider     string    `json:"provider"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	// AccountID is the ChatGPT account the Codex backend bills (chatgpt-account-id).
	AccountID string `json:"account_id,omitempty"`
	// BaseURL is the API host the login itself named: Copilot's plan endpoint,
	// Qwen's resource_url. Empty means the provider's default.
	BaseURL string `json:"base_url,omitempty"`
	// ProjectID is the Google Cloud project billed for Gemini (x-goog-user-project).
	ProjectID string `json:"project_id,omitempty"`
	// ClientID and ClientSecret are kept for providers whose refresh needs the
	// client that issued the token (Google: the user's own OAuth client).
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	// UpdatedAt is when the file was last written, for the status line.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// expirySkew is how early a token is treated as expired, so a request is never
// sent with a token that dies on the way.
const expirySkew = 2 * time.Minute

// NeedsRefresh reports whether the access token is missing or about to expire.
// A token with no expiry is used until the server refuses it.
func (c Credential) NeedsRefresh(now time.Time) bool {
	if c.AccessToken == "" {
		return true
	}
	if c.ExpiresAt.IsZero() {
		return false
	}
	return now.Add(expirySkew).After(c.ExpiresAt)
}

// ErrNoCredential means no login was stored for the provider.
var ErrNoCredential = errors.New("no stored login for this provider")

// CredentialPath is where a provider's credential lives inside dir.
func CredentialPath(dir, provider string) string {
	return filepath.Join(dir, strings.ToLower(strings.TrimSpace(provider))+".json")
}

// HasCredential reports whether a usable credential is stored for provider.
func HasCredential(dir, provider string) bool {
	if dir == "" {
		return false
	}
	c, err := LoadCredential(dir, provider)
	return err == nil && (c.AccessToken != "" || c.RefreshToken != "")
}

// LoadCredential reads the stored credential of a provider.
func LoadCredential(dir, provider string) (Credential, error) {
	if dir == "" {
		return Credential{}, ErrNoCredential
	}
	data, err := os.ReadFile(CredentialPath(dir, provider))
	if errors.Is(err, fs.ErrNotExist) {
		return Credential{}, ErrNoCredential
	}
	if err != nil {
		return Credential{}, err
	}
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		return Credential{}, fmt.Errorf("the stored login for %s is unreadable: %w", provider, err)
	}
	if c.Provider == "" {
		c.Provider = provider
	}
	return c, nil
}

// SaveCredential writes a credential atomically with 0600 permissions, in a
// 0700 directory: it is a secret, and a half-written file would be a login lost.
func SaveCredential(dir string, c Credential) error {
	if dir == "" {
		return errors.New("there is no directory to store the login in")
	}
	if c.Provider == "" {
		return errors.New("a credential must name its provider")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}
	c.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return fmt.Errorf("could not write in %s: %w", dir, err)
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, CredentialPath(dir, c.Provider))
}

// DeleteCredential removes a stored login. A missing file is not an error.
func DeleteCredential(dir, provider string) error {
	err := os.Remove(CredentialPath(dir, provider))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
