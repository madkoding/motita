package gitforge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/oauth"
)

// credentialPrefix keeps a git login apart from the LLM logins that share the
// directory: "git-github.json" next to "copilot.json".
const credentialPrefix = "git-"

// Store keeps the git logins, one 0600 file per service, in the same directory as
// the LLM logins and with the same atomic writes.
type Store struct {
	// Dir is where the files live (config.AuthDir()).
	Dir string
	// HTTP is the transport of every request; nil means the package default.
	HTTP oauth.HTTPClient
	// Env resolves the OAuth applications of the services; nil is the process
	// environment.
	Env Lookup
	// Now is the clock the expiry is judged against; nil is time.Now.
	Now func() time.Time
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Store) key(svc Service) string { return credentialPrefix + svc.ID }

// Connected reports whether a login is stored for the service.
func (s Store) Connected(svc Service) bool {
	return oauth.HasCredential(s.Dir, s.key(svc))
}

// Save stores a login for the service. username is the account it belongs to.
func (s Store) Save(svc Service, tok oauth.Token, username string, basic bool) error {
	return oauth.SaveCredential(s.Dir, oauth.Credential{
		Provider:     s.key(svc),
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAt,
		BaseURL:      svc.Host,
		Username:     username,
		Basic:        basic,
		// The client that issued the token is kept: renewing it needs the same one,
		// and the user may have changed the environment since.
		ClientID:     svc.Client.ID,
		ClientSecret: svc.Client.Secret,
	})
}

// Remove forgets the login. A missing one is not an error.
func (s Store) Remove(svc Service) error {
	return oauth.DeleteCredential(s.Dir, s.key(svc))
}

// Credential returns the login of a service, renewed first when its token is
// about to expire. A login that cannot be renewed is returned as it is: the host
// will refuse it, and that refusal is what the user needs to see.
func (s Store) Credential(ctx context.Context, svc Service) (oauth.Credential, error) {
	cred, err := oauth.LoadCredential(s.Dir, s.key(svc))
	if err != nil {
		return oauth.Credential{}, err
	}
	if !cred.NeedsRefresh(s.now()) || cred.RefreshToken == "" {
		return cred, nil
	}
	client := oauth.Client{ID: cred.ClientID, Secret: cred.ClientSecret}
	tok, err := oauth.RefreshAccessToken(ctx, s.HTTP, svc.Endpoints, client, cred.RefreshToken)
	if err != nil {
		return cred, nil
	}
	cred.AccessToken, cred.RefreshToken, cred.ExpiresAt = tok.AccessToken, tok.RefreshToken, tok.ExpiresAt
	// Failing to persist the renewed token must not fail the caller that holds a
	// good one now; the next call renews again.
	_ = oauth.SaveCredential(s.Dir, cred)
	return cred, nil
}

// All is every service a login is stored for, plus the four public hosts whether
// or not they are connected: the settings screen lists them all.
func (s Store) All() []Service {
	all := Defaults(s.Env)
	have := map[string]bool{}
	for _, d := range all {
		have[d.ID] = true
	}
	matches, _ := filepath.Glob(filepath.Join(s.Dir, credentialPrefix+"*@*.json"))
	for _, m := range matches {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), credentialPrefix), ".json")
		kind, host, _ := strings.Cut(id, "@")
		// Custom answers a default host with the default service, so the id is
		// checked after it: a stray "gitlab@gitlab.com" file must not list GitLab twice.
		if svc, err := Custom(Kind(kind), host, s.Env); err == nil && !have[svc.ID] {
			have[svc.ID] = true
			all = append(all, svc)
		}
	}
	return all
}

// ByID finds a service by its id among All.
func (s Store) ByID(id string) (Service, bool) {
	for _, svc := range s.All() {
		if svc.ID == id {
			return svc, true
		}
	}
	return Service{}, false
}

// ForHost finds the service whose git host is host.
func (s Store) ForHost(host string) (Service, bool) {
	host = strings.ToLower(host)
	for _, svc := range s.All() {
		if svc.Host == host {
			return svc, true
		}
	}
	return Service{}, false
}

// ForRemote finds the service for a repository address.
func (s Store) ForRemote(raw string) (Service, Remote, error) {
	r, err := ParseRemote(raw)
	if err != nil {
		return Service{}, Remote{}, err
	}
	svc, ok := s.ForHost(r.Host)
	if !ok {
		return Service{}, r, ErrNoService
	}
	return svc, r, nil
}

// Account is what the settings screen shows for one service.
type Account struct {
	Service   Service
	Connected bool
	Username  string
}

// Accounts lists every service and who the user is on it.
func (s Store) Accounts() []Account {
	var out []Account
	for _, svc := range s.All() {
		a := Account{Service: svc}
		if cred, err := oauth.LoadCredential(s.Dir, s.key(svc)); err == nil && (cred.AccessToken != "" || cred.RefreshToken != "") {
			a.Connected, a.Username = true, cred.Username
		}
		out = append(out, a)
	}
	return out
}

// IsNotConnected reports whether err means "no login stored".
func IsNotConnected(err error) bool { return errors.Is(err, oauth.ErrNoCredential) }
