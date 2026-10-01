package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Loopback receives the redirect of an authorisation-code flow on this machine:
// the browser is sent to http://<host>:<port><path>?code=...&state=..., and the
// code is handed to whoever waits on it.
//
// A machine reached over SSH has no browser that can reach its loopback, so the
// redirect can also be completed by hand: the user pastes the URL the browser
// failed to open (or the bare code) and Wait accepts that instead.
type Loopback struct {
	RedirectURI string
	srv         *http.Server
	ln          net.Listener
	results     chan callbackResult
}

type callbackResult struct {
	code, state, err string
}

// StartLoopback listens on addr ("127.0.0.1:1455", or "127.0.0.1:0" for any
// free port) and serves path. redirectHost is the host spelled in the redirect
// URI, which must match what the OAuth client registered ("localhost" or
// "127.0.0.1").
func StartLoopback(addr, redirectHost, path string) (*Loopback, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("could not listen on %s for the login callback: %w", addr, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	l := &Loopback{
		RedirectURI: fmt.Sprintf("http://%s:%d%s", redirectHost, port, path),
		ln:          ln,
		results:     make(chan callbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := callbackResult{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}
		if d := q.Get("error_description"); d != "" {
			res.err += ": " + d
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != "" || res.code == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<!doctype html><title>motita</title><p>The login did not complete. You can close this tab and look at the terminal.</p>")
		} else {
			fmt.Fprint(w, "<!doctype html><title>motita</title><p>Logged in. You can close this tab and go back to the terminal.</p>")
		}
		select {
		case l.results <- res:
		default:
		}
	})
	l.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = l.srv.Serve(ln) }()
	return l, nil
}

// Close stops the listener.
func (l *Loopback) Close() {
	if l == nil || l.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = l.srv.Shutdown(ctx)
}

// Wait returns the authorisation code from whichever arrives first: the browser
// redirect, or a line pasted on paste. The state must match: a code that comes
// back with another state answers a request this process did not make.
//
// pasted reports whether the answer came from paste, so a caller knows whether
// the reader feeding it is still waiting for a line.
func (l *Loopback) Wait(ctx context.Context, wantState string, paste <-chan string) (code string, pasted bool, err error) {
	var browser <-chan callbackResult
	if l != nil {
		browser = l.results
	}
	for {
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case res := <-browser:
			if res.err != "" {
				return "", false, fmt.Errorf("the provider refused the login: %s", res.err)
			}
			if res.state != wantState {
				return "", false, errors.New("the login callback carried a state this login did not issue")
			}
			return res.code, false, nil
		case line, ok := <-paste:
			if !ok {
				paste = nil
				if browser == nil {
					return "", true, errors.New("no code was given")
				}
				continue
			}
			code, err := ParseAuthCode(line, wantState)
			return code, true, err
		}
	}
}

// ParseAuthCode reads what a user pasted after a login: the whole redirect URL,
// its query string, a "code#state" pair, or the bare code. When a state is
// present it must be wantState.
func ParseAuthCode(input, wantState string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", errors.New("no code was given")
	}
	var code, state string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("the pasted URL is not valid: %w", err)
		}
		q := u.Query()
		if e := q.Get("error"); e != "" {
			return "", fmt.Errorf("the provider refused the login: %s", e)
		}
		code, state = q.Get("code"), q.Get("state")
	case strings.Contains(s, "code="):
		q, err := url.ParseQuery(strings.TrimPrefix(s, "?"))
		if err != nil {
			return "", fmt.Errorf("the pasted text is not a query string: %w", err)
		}
		code, state = q.Get("code"), q.Get("state")
	case strings.Contains(s, "#"):
		code, state, _ = strings.Cut(s, "#")
	default:
		code = s
	}
	if code == "" {
		return "", errors.New("the pasted text carries no code")
	}
	if state != "" && wantState != "" && state != wantState {
		return "", errors.New("the pasted code belongs to another login (the state does not match)")
	}
	return code, nil
}
