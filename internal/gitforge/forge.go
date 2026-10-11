// Package gitforge is motita's knowledge of git hosts: GitHub, GitLab,
// Bitbucket and the Gitea family (self-hosted Gitea, Forgejo).
//
// It exists because three things the user asked for are the SAME thing seen from
// three sides. Connecting to a host (OAuth, or a pasted token), letting `git`
// use that connection when it clones or pushes - including from a session's
// worktree - and talking to the host's API to list the user's repositories, open
// a pull request and read its CI all need the same answer to "which host is
// this, and what is the user's token for it". The answer lives here once.
//
// The package produces credentials and answers; it does not decide what to do
// with them. The gateway, the TUI and the `motita git-credential` and
// `motita forge` commands are its callers.
package gitforge

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/madkoding/motita/internal/oauth"
)

// Kind is the API dialect of a host.
type Kind string

// The four dialects. GitHub Enterprise speaks github, a self-hosted GitLab
// speaks gitlab, and Forgejo speaks gitea.
const (
	KindGitHub    Kind = "github"
	KindGitLab    Kind = "gitlab"
	KindBitbucket Kind = "bitbucket"
	KindGitea     Kind = "gitea"
)

// Method is a way of connecting.
type Method string

// The ways to connect. Token is always possible; the others need the host to
// offer them and an OAuth application (a client id) to exist.
const (
	MethodDevice Method = "device"
	MethodCode   Method = "code"
	MethodToken  Method = "token"
)

// ghCLIClientID is the public client id of GitHub's own command line, the login
// every `gh auth login` uses. It is a public client of a device flow (no secret)
// and it is only the DEFAULT: MOTITA_GITHUB_CLIENT_ID replaces it with an OAuth
// application of the user's own.
const ghCLIClientID = "178c6fc778ccc68e1d6a"

// Service is one git host motita can connect to.
type Service struct {
	// ID names the login on disk and in the API: "github", "gitlab", "bitbucket",
	// or "<kind>@<host>" for a self-hosted one.
	ID   string
	Name string
	Kind Kind
	// Host is the host git talks to ("github.com"), the key a remote URL is
	// matched against.
	Host string
	// APIBase is where the REST API lives, without a trailing slash.
	APIBase   string
	Endpoints oauth.Endpoints
	Client    oauth.Client
	Scope     string
	// TokenURL is the page where a user creates a token by hand, shown when the
	// host has no OAuth application configured.
	TokenURL string
	// EnvPrefix is the prefix of the environment variables that configure this
	// service's OAuth application: <prefix>_CLIENT_ID and <prefix>_CLIENT_SECRET.
	EnvPrefix string
}

// Methods lists how this service can be connected, best first. A device flow
// needs a client id and an endpoint; a code flow needs both and a secret (the
// hosts that have no device flow all require one); a token always works.
func (s Service) Methods() []Method {
	var m []Method
	if s.Client.ID != "" && s.Endpoints.DeviceURL != "" {
		m = append(m, MethodDevice)
	}
	if s.Client.ID != "" && s.Endpoints.DeviceURL == "" && s.Endpoints.AuthorizeURL != "" {
		m = append(m, MethodCode)
	}
	return append(m, MethodToken)
}

// OAuthReady reports whether the service can be connected without pasting a token.
func (s Service) OAuthReady() bool { return len(s.Methods()) > 1 }

// Lookup reads an environment variable. It is a parameter so the registry can be
// tested without touching the process environment.
type Lookup func(string) string

// OSEnv is Lookup over the real environment.
func OSEnv(key string) string { return os.Getenv(key) }

// Defaults is the services motita knows without being told: the four public
// hosts.
func Defaults(env Lookup) []Service {
	if env == nil {
		env = OSEnv
	}
	return []Service{
		configure(Service{
			ID: "github", Name: "GitHub", Kind: KindGitHub, Host: "github.com",
			APIBase: "https://api.github.com",
			Endpoints: oauth.Endpoints{
				DeviceURL: "https://github.com/login/device/code",
				TokenURL:  "https://github.com/login/oauth/access_token",
			},
			Client:    oauth.Client{ID: ghCLIClientID},
			Scope:     "repo read:org workflow",
			TokenURL:  "https://github.com/settings/tokens/new?scopes=repo,workflow&description=motita",
			EnvPrefix: "MOTITA_GITHUB",
		}, env),
		configure(Service{
			ID: "gitlab", Name: "GitLab", Kind: KindGitLab, Host: "gitlab.com",
			APIBase: "https://gitlab.com/api/v4",
			Endpoints: oauth.Endpoints{
				DeviceURL: "https://gitlab.com/oauth/authorize_device",
				TokenURL:  "https://gitlab.com/oauth/token",
			},
			Scope:     "api read_repository write_repository",
			TokenURL:  "https://gitlab.com/-/user_settings/personal_access_tokens?name=motita&scopes=api,read_repository,write_repository",
			EnvPrefix: "MOTITA_GITLAB",
		}, env),
		configure(Service{
			ID: "bitbucket", Name: "Bitbucket", Kind: KindBitbucket, Host: "bitbucket.org",
			APIBase: "https://api.bitbucket.org/2.0",
			Endpoints: oauth.Endpoints{
				AuthorizeURL: "https://bitbucket.org/site/oauth2/authorize",
				TokenURL:     "https://bitbucket.org/site/oauth2/access_token",
				BasicAuth:    true,
			},
			Scope:     "repository:write pullrequest:write account",
			TokenURL:  "https://bitbucket.org/account/settings/app-passwords/new",
			EnvPrefix: "MOTITA_BITBUCKET",
		}, env),
	}
}

// configure applies the user's OAuth application, when they set one.
func configure(s Service, env Lookup) Service {
	if id := strings.TrimSpace(env(s.EnvPrefix + "_CLIENT_ID")); id != "" {
		s.Client.ID = id
	}
	if secret := strings.TrimSpace(env(s.EnvPrefix + "_CLIENT_SECRET")); secret != "" {
		s.Client.Secret = secret
	}
	return s
}

// Custom builds the service of a self-hosted instance: a GitLab on the user's
// own server, a Gitea, a GitHub Enterprise. It can only be connected with a
// token unless the user configured an OAuth application for it, because there
// is no public client id for a server nobody but them runs.
func Custom(kind Kind, host string, env Lookup) (Service, error) {
	if env == nil {
		env = OSEnv
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if !validHost(host) {
		return Service{}, fmt.Errorf("%q is not a host name", host)
	}
	for _, d := range Defaults(env) {
		if d.Host == host {
			return d, nil
		}
	}
	base := "https://" + host
	s := Service{
		ID: string(kind) + "@" + host, Kind: kind, Host: host,
		EnvPrefix: "MOTITA_" + strings.ToUpper(string(kind)),
	}
	switch kind {
	case KindGitHub:
		s.Name = "GitHub Enterprise (" + host + ")"
		s.APIBase = base + "/api/v3"
		s.Endpoints = oauth.Endpoints{DeviceURL: base + "/login/device/code", TokenURL: base + "/login/oauth/access_token"}
		s.Scope = "repo read:org workflow"
		s.TokenURL = base + "/settings/tokens/new"
	case KindGitLab:
		s.Name = "GitLab (" + host + ")"
		s.APIBase = base + "/api/v4"
		s.Endpoints = oauth.Endpoints{DeviceURL: base + "/oauth/authorize_device", TokenURL: base + "/oauth/token"}
		s.Scope = "api read_repository write_repository"
		s.TokenURL = base + "/-/user_settings/personal_access_tokens"
	case KindGitea:
		s.Name = "Gitea (" + host + ")"
		s.APIBase = base + "/api/v1"
		s.Endpoints = oauth.Endpoints{AuthorizeURL: base + "/login/oauth/authorize", TokenURL: base + "/login/oauth/access_token"}
		s.TokenURL = base + "/user/settings/applications"
	default:
		return Service{}, fmt.Errorf("a self-hosted %s is not supported: use github, gitlab or gitea", kind)
	}
	return configure(s, env), nil
}

// validHost accepts a bare DNS name or IPv4: nothing that could smuggle a path, a
// port, credentials or a scheme into a URL built from it, and nothing that is not
// a valid file name part (the host is part of a credential's file name).
func validHost(h string) bool {
	if h == "" || len(h) > 253 || strings.HasPrefix(h, ".") || strings.HasPrefix(h, "-") {
		return false
	}
	for _, r := range h {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// ErrNoService means a URL names a host motita has no connection for.
var ErrNoService = errors.New("no git host is connected for this address")

// Remote is a repository address taken apart.
type Remote struct {
	Host string
	// Path is "owner/repo" for GitHub and Gitea, "group/subgroup/repo" for GitLab,
	// "workspace/repo" for Bitbucket, without ".git".
	Path string
}

// Owner is everything before the last path element.
func (r Remote) Owner() string {
	i := strings.LastIndexByte(r.Path, '/')
	if i < 0 {
		return ""
	}
	return r.Path[:i]
}

// Name is the last path element: the repository.
func (r Remote) Name() string {
	return r.Path[strings.LastIndexByte(r.Path, '/')+1:]
}

// ParseRemote reads the three spellings a remote has: https://host/o/r(.git),
// ssh://git@host/o/r.git and the scp form git@host:o/r.git.
func ParseRemote(raw string) (Remote, error) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return Remote{}, fmt.Errorf("%q is not a repository address", raw)
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(raw, "@") && strings.Contains(raw, ":"):
		rest := raw[strings.IndexByte(raw, '@')+1:]
		host, path, _ = strings.Cut(rest, ":")
	default:
		return Remote{}, fmt.Errorf("%q is not a repository address", raw)
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || !strings.Contains(path, "/") {
		return Remote{}, fmt.Errorf("%q does not name an owner and a repository", raw)
	}
	// The path goes into API URLs as it is, so each element must be a plain name: a ".." or a
	// "?" would point a request carrying the user's token at another endpoint.
	for _, seg := range strings.Split(path, "/") {
		if !plainSegment(seg) {
			return Remote{}, fmt.Errorf("%q does not name an owner and a repository", raw)
		}
	}
	return Remote{Host: strings.ToLower(host), Path: path}, nil
}

// plainSegment reports whether s is a name every host allows in an owner or a repository:
// letters, digits, '.', '-' and '_', and not "." or "..".
func plainSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
