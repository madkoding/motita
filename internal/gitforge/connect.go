package gitforge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/oauth"
)

// CallbackAddr is where the code flow listens for the browser's redirect. It is a
// fixed port because Bitbucket and Gitea register the exact callback URL with
// the OAuth application; when it is taken a free port is used, which the hosts
// that accept any loopback port (Gitea) still honour.
const CallbackAddr = "127.0.0.1:8787"

// callbackPath is the path of the redirect.
const callbackPath = "/callback"

// startLoopback opens the listener; a variable so a test can make it fail.
var startLoopback = oauth.StartLoopback

// sleepFor waits between device polls; a variable so a test does not wait.
var sleepFor = time.After

// DeviceFlow is a login in progress through the device grant: the user opens URL,
// types Code, and Wait finishes the job.
type DeviceFlow struct {
	Service Service
	// URL is where the user goes and Code is what they type there.
	URL, Code string
	// ExpiresIn is how many seconds the code lives.
	ExpiresIn int
	store     Store
	dc        oauth.DeviceCode
}

// StartDevice asks the host for a device code.
func (s Store) StartDevice(ctx context.Context, svc Service) (*DeviceFlow, error) {
	if !contains(svc.Methods(), MethodDevice) {
		return nil, fmt.Errorf("%s has no device login configured: connect with a token, or set %s_CLIENT_ID", svc.Name, svc.EnvPrefix)
	}
	dc, err := oauth.DeviceStart(ctx, s.HTTP, svc.Endpoints, svc.Client, svc.Scope)
	if err != nil {
		return nil, fmt.Errorf("could not start the %s login: %w", svc.Name, err)
	}
	return &DeviceFlow{Service: svc, URL: dc.VerificationURL, Code: dc.UserCode, ExpiresIn: dc.ExpiresIn, store: s, dc: dc}, nil
}

// Wait polls until the user approves (or refuses, or the code expires), then
// stores the login and returns the account it belongs to.
func (f *DeviceFlow) Wait(ctx context.Context) (string, error) {
	tok, err := oauth.PollDevice(ctx, f.dc.Interval, sleepFor, func() (oauth.Token, error) {
		return oauth.DevicePoll(ctx, f.store.HTTP, f.Service.Endpoints, f.Service.Client, f.dc)
	})
	if err != nil {
		return "", fmt.Errorf("%s login failed: %w", f.Service.Name, err)
	}
	return f.store.finish(ctx, f.Service, tok, "", false)
}

// CodeFlow is a login in progress through the authorisation-code grant: the user
// opens URL, and the host sends the browser back to this machine.
type CodeFlow struct {
	Service Service
	URL     string
	store   Store
	pkce    oauth.PKCE
	lb      *oauth.Loopback
}

// StartCode opens the loopback listener and builds the URL to send the browser to.
func (s Store) StartCode(svc Service) (*CodeFlow, error) {
	if !contains(svc.Methods(), MethodCode) {
		return nil, fmt.Errorf("%s has no browser login configured: connect with a token, or set %s_CLIENT_ID and %s_CLIENT_SECRET", svc.Name, svc.EnvPrefix, svc.EnvPrefix)
	}
	pkce, err := oauth.NewPKCE()
	if err != nil {
		return nil, err
	}
	lb, err := startLoopback(CallbackAddr, "localhost", callbackPath)
	if err != nil {
		if lb, err = startLoopback("127.0.0.1:0", "localhost", callbackPath); err != nil {
			return nil, err
		}
	}
	return &CodeFlow{
		Service: svc, store: s, pkce: pkce, lb: lb,
		URL: oauth.AuthCodeURL(svc.Endpoints, svc.Client, lb.RedirectURI, svc.Scope, pkce),
	}, nil
}

// RedirectURI is the callback this flow listens on, which the OAuth application
// must list.
func (f *CodeFlow) RedirectURI() string { return f.lb.RedirectURI }

// Wait returns when the browser has come back, or a line has been pasted on
// paste (for a machine whose browser cannot reach this one), and stores the login.
func (f *CodeFlow) Wait(ctx context.Context, paste <-chan string) (string, error) {
	defer f.lb.Close()
	code, _, err := f.lb.Wait(ctx, f.pkce.State, paste)
	if err != nil {
		return "", err
	}
	tok, err := oauth.ExchangeAuthCode(ctx, f.store.HTTP, f.Service.Endpoints, f.Service.Client, code, f.lb.RedirectURI, f.pkce)
	if err != nil {
		return "", fmt.Errorf("could not complete the %s login: %w", f.Service.Name, err)
	}
	return f.store.finish(ctx, f.Service, tok, "", false)
}

// Cancel stops a code flow that will not be waited on.
func (f *CodeFlow) Cancel() { f.lb.Close() }

// ConnectToken connects with a token the user created by hand. It is checked
// against the host before it is stored: a token that does not work is refused
// now, not at the first push. Bitbucket tokens are app passwords and need the
// account name.
func (s Store) ConnectToken(ctx context.Context, svc Service, token, username string) (string, error) {
	token, username = strings.TrimSpace(token), strings.TrimSpace(username)
	if token == "" {
		return "", errors.New("the token is empty")
	}
	basicAuth := svc.Kind == KindBitbucket
	if basicAuth && username == "" {
		return "", errors.New("an account name is needed with a Bitbucket app password")
	}
	return s.finish(ctx, svc, oauth.Token{AccessToken: token}, username, basicAuth)
}

// finish verifies a token by asking the host who it is, then stores it. The login
// is only stored when the host accepts it.
func (s Store) finish(ctx context.Context, svc Service, tok oauth.Token, username string, basicAuth bool) (string, error) {
	api := API{Service: svc, HTTP: s.HTTP, Cred: oauth.Credential{AccessToken: tok.AccessToken, Username: username, Basic: basicAuth}}
	who, err := api.Whoami(ctx)
	if err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.Unauthorized() {
			return "", fmt.Errorf("%s did not accept the token", svc.Name)
		}
		return "", fmt.Errorf("%s could not confirm the account: %w", svc.Name, err)
	}
	if username == "" {
		username = who
	}
	if err := s.Save(svc, tok, username, basicAuth); err != nil {
		return "", err
	}
	return username, nil
}

func contains(ms []Method, m Method) bool {
	for _, x := range ms {
		if x == m {
			return true
		}
	}
	return false
}
