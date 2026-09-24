package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/netrules"
	"github.com/madkoding/starlight/internal/webui"
)

// defaultMaxBodyKB caps a request body when nothing else is configured. A task or a prompt is a
// sentence; 256 KB is generous for one and small enough that a client cannot make the agent
// chew on a novel.
const defaultMaxBodyKB = 256

// defaultHeartbeat is how often a long-lived stream writes a comment line to keep the connection
// alive across a middlebox.
//
// A mobile carrier NAT commonly drops a connection that has been idle for 30 to 60 seconds, and a
// turn can easily run longer than that with nothing to say. The failure it prevents is silent and
// looks like the RUN died rather than the connection: the client sees the stream close, reattaches,
// and the user loses the tail of work that was still going.
//
// 15 seconds leaves generous room under the shortest timeout in common use while costing one
// 16-byte line - nothing against an event stream that carries a run's output.
const defaultHeartbeat = 15 * time.Second

// Options configure a Server.
type Options struct {
	// Service is the agent this gateway speaks for.
	Service Service
	// Listen is the address to bind, "host:port". Port 0 asks the kernel for a free one, which
	// is what an embedded gateway wants: no conflict with anything, and the address that was
	// actually granted is read back with Addr().
	Listen string
	// Token is the bearer token. Empty refuses to start: see Start.
	Token string
	// Allow is the ordered origin rule list, already parsed. Nil means every origin may connect,
	// which is the documented default: the gateway comes up the way a machine with a fresh, empty
	// firewall table accepts everything, and the operator narrows it by adding rules.
	//
	// It is the PARSED policy rather than the strings, because the parsing has to have happened
	// before this point: a malformed rule must be refused while the configuration is read, not
	// discovered request by request, where its only symptom is an origin being refused for a
	// reason nobody can see.
	Allow *netrules.Policy
	// MaxBodyKB caps a request body. Zero means defaultMaxBodyKB.
	MaxBodyKB int
	// Version is reported by /v1/health, so a client can tell which build answered.
	Version string
	// NewService builds one more conversation when a client asks for one. Nil means this
	// gateway serves exactly one conversation, which is a real deployment: the embedded case
	// where the terminal that started this process is the only front end there will ever be.
	//
	// It is a factory rather than a list because a conversation holds a transcript and a
	// session, and building all of them up front would build transcripts nobody asked for. It
	// must NOT call back into the gateway: it is called with the registry lock held.
	NewService func() (Service, error)
	// MaxSessions caps how many conversations this process will hold. Zero means
	// defaultMaxSessions.
	MaxSessions int
	// Log receives the one line a gateway has to say when it starts: where it is listening.
	// That line is the only way an operator learns the port when 0 was asked for.
	Log *logx.Logger
	// Heartbeat is how often a stream writes a keepalive comment. Zero means defaultHeartbeat.
	// Negative turns it off, which is what a test needs in order to assert that the stream is
	// quiet: the tick only fires on a real timer, so a test that waits for one is a test that
	// sleeps for the heartbeat interval.
	Heartbeat time.Duration
	// WebUI serves the browser interface from this same mux. The page and the API therefore
	// share an origin, which is why no proxy and no CORS header are involved anywhere: the
	// browser asks this server for everything.
	//
	// The page is served WITHOUT a token - it holds no secret and it is the only way a browser
	// can obtain one - and the API it calls is authorised exactly as before.
	WebUI bool
}

// Server is the HTTP face of the conversations this process holds.
type Server struct {
	opts     Options
	listener net.Listener
	server   *http.Server
	mux      http.Handler

	// closeOnce makes Close idempotent. Close is called from a defer in the app AND from the
	// shutdown path, and a second Shutdown on an already-closed listener returns an error the
	// caller would have to know to ignore.
	closeOnce sync.Once
	closeErr  error

	// baseCtx is cancelled when this server closes, and every run derives its context from it.
	//
	// A run is NOT bounded by its connection - that is the whole point of it being an object - so
	// it needs some other bound or a gateway that shut down would leave turns running with
	// nowhere to report. The process's own lifetime is that bound, and it is deliberately not the
	// request's.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	// sessionsMu guards the registry. The conversations themselves are safe to use without it:
	// each is guarded by its own locks, and this is only read to find one.
	// heartbeat is the resolved keepalive interval, always positive: see Options.Heartbeat.
	heartbeat time.Duration

	sessionsMu sync.Mutex
	sessions   map[string]*conversation
}

// Start binds the listener and returns a Server that is ready to Serve.
//
// It binds rather than calling ListenAndServe because a client has to be TOLD the address it
// must speak to, and with port 0 that address only exists after the bind has happened.
func Start(opts Options) (*Server, error) {
	if opts.Service == nil {
		return nil, errors.New("the gateway needs a service to speak for")
	}
	if strings.TrimSpace(opts.Token) == "" {
		// Refusing here rather than serving everyone is the whole point: an unauthenticated
		// gateway is a remote shell with a JSON envelope.
		return nil, errors.New("the gateway needs a token: an unauthenticated agent is a remote shell")
	}

	addr := strings.TrimSpace(opts.Listen)
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("the listen address %q is not host:port: %w", addr, err)
	}
	// Any address may be bound, including a non-loopback one, and there is no longer a second act
	// required for it. The socket is not what decides who may connect: gateway.allow is, and it is
	// applied to every request below. Refusing a wildcard bind here would re-introduce exactly the
	// confusion this design removes - an operator who wrote the address they wanted being told to
	// turn on a setting that no longer exists.

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Reported, never retried on another port: a client that was told an address must not
		// find a server somewhere else.
		return nil, fmt.Errorf("could not listen on %s: %w", addr, err)
	}

	if opts.MaxBodyKB <= 0 {
		opts.MaxBodyKB = defaultMaxBodyKB
	}
	first := newConversation(DefaultSession, opts.Service)
	baseCtx, baseCancel := context.WithCancel(context.Background())
	// Resolved here so the stream loop never has to reason about zero and negative: a zero means
	// the default and a negative means off, and both are decided once, at construction.
	heartbeat := opts.Heartbeat
	if heartbeat == 0 {
		heartbeat = defaultHeartbeat
	}
	if heartbeat < 0 {
		heartbeat = 0
	}

	s := &Server{
		opts: opts, listener: ln, sessions: map[string]*conversation{DefaultSession: first},
		baseCtx: baseCtx, baseCancel: baseCancel,
		heartbeat: heartbeat,
	}
	// The origin policy wraps the ENTIRE routing table, including the page and /v1/health.
	//
	// Everything rather than only the API, and that is a decision: a rule set that still served the
	// page to a refused origin would be a rule set that only half applied, and the page is the one
	// thing a browser asks for BEFORE it holds any credential. Refusing /v1/health too means a
	// client that is not allowed in finds out immediately, with a 403 that names the reason,
	// instead of having its health probe succeed and its every real call fail.
	//
	// The policy is enforced BEFORE the token check, so a refused origin does not even learn
	// whether its credential was good.
	s.mux = s.originPolicy(s.routes())
	s.server = &http.Server{
		Handler: s.mux,
		// No WriteTimeout. It is a deadline on the WHOLE response, and half of these responses
		// are a run that streams until the agent is done: a deadline there would cut a slow
		// answer off mid-sentence, which reads to the user as "the agent stopped". A stalled
		// client is bounded by ReadHeaderTimeout instead, which is the risk worth bounding.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	return s, nil
}

// Addr is the address a CLIENT should call, with the real port when 0 was asked for.
//
// It is not simply what the listener reports, because for a wildcard bind the listener reports
// "[::]" — the dual-stack wildcard Go picks for "0.0.0.0". That string is an address to LISTEN on
// and not one to dial: put in a URL it names no host a client can reach. Measured with a real
// browser: a page served on a wildcard bind loads over 127.0.0.1, over localhost and over the
// machine's LAN address, and comes back as an empty document over [::].
//
// So the wildcard is translated into the only address that means "this machine" to every client
// and works everywhere: loopback, keeping the real port. A remote client does NOT use this address
// — it uses the host it reached us on — which is exactly why exposure is answered by
// ReachableFromNetwork rather than read out of this string.
//
// Reporting the listener's address here would have put an uncallable URL into the startup
// announcement, into the service file that `status`, `stop` and the client all discover the
// gateway from, and therefore into every client that ever resolved it.
func (s *Server) Addr() string {
	addr := s.listener.Addr().String()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if isLoopback(host) {
		return addr
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// ReachableFromNetwork reports whether this gateway is bound beyond loopback.
//
// It exists because Addr() can no longer answer it: normalising the wildcard into loopback would
// make a network-bound gateway LOOK like a local one. The two are different questions — which
// address to call, and how far this is exposed — and collapsing them is how a service file claims a
// gateway is private while it answers the whole network.
func (s *Server) ReachableFromNetwork() bool {
	host, _, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return false
	}
	return !isLoopback(host)
}

// AllowDescription is the origin rule set this gateway enforces, as the SERVER understands it.
//
// It is read from the server rather than from the configuration it was built from so that the
// description and the enforcement cannot disagree: what is written to the service file is the
// policy this process is actually applying, and a caller that re-parsed the configuration would be
// describing its own reading of the file rather than the gateway's behaviour.
func (s *Server) AllowDescription() string {
	if s.opts.Allow == nil {
		// No policy at all is the same outcome as an empty one: no rule restricts anything. It is
		// reported through the policy's own words rather than a second phrase invented here, so the
		// two spellings cannot drift.
		return (&netrules.Policy{}).Describe()
	}
	return s.opts.Allow.Describe()
}

// BaseURL is the origin a local client should speak to.
func (s *Server) BaseURL() string { return "http://" + s.Addr() }

// Token is the bearer token this server requires.
func (s *Server) Token() string { return s.opts.Token }

// Handler is the router. It is exposed so a test can drive the endpoints without a socket when
// a socket is not the thing being tested.
func (s *Server) Handler() http.Handler { return s.mux }

// Serve blocks until Close. A closed listener is reported as nil: that is how this server
// stops, not a failure.
func (s *Server) Serve() error {
	if s.opts.Log != nil {
		s.opts.Log.Info("the gateway is listening", "address", s.Addr())
	}
	if err := s.server.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Close stops the listener and waits for the connections in flight, bounded by ctx.
//
// The runs in flight are cancelled FIRST. A run is not bounded by its connection any more, so
// without this a gateway that shut down would leave a turn running with nowhere to report - and it
// is cancelled before the listener closes, so the client that was watching it gets the cancellation
// on its stream rather than a connection that just ends.
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.baseCancel()
		s.closeErr = s.server.Shutdown(ctx)
	})
	return s.closeErr
}

// routes builds the endpoint table.
//
// Every conversation endpoint is addressed by the session it is about, and only /v1/health is
// process-wide. One rule with no exceptions is worth more than a shorter table: a reader who
// knows it never has to check which endpoints are about a conversation.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)

	// The interface, when it is on, BEFORE the API: it is the one part of this server that is
	// deliberately reachable without a token, because it is the only way a browser can get one.
	//
	// The root is registered as "{$}" so that it matches ONLY "/". A bare "GET /" in Go's
	// ServeMux matches everything not otherwise registered, which would answer a typo with a
	// page that renders - a bug that looks like it worked.
	if s.opts.WebUI {
		s.page(mux, "GET /{$}", "/")
		s.page(mux, "GET /app.css", "/app.css")
		s.page(mux, "GET /app.js", "/app.js")
	}

	// auth wraps anything, not just a HandlerFunc: withConversation hands back a Handler, and
	// forcing it through a HandlerFunc would be a cast that says nothing.
	auth := func(h http.Handler) http.Handler { return requireToken(s.opts.Token, h) }
	// plain is the same check for a bare handler function, so the session-list endpoints do not
	// have to be cast to satisfy a signature.
	plain := func(h http.HandlerFunc) http.Handler { return requireToken(s.opts.Token, h) }
	// scoped is every handler that speaks ABOUT a conversation: the token is checked, then the
	// session is resolved, then the handler runs with it in the request context.
	scoped := func(h http.HandlerFunc) http.Handler { return auth(s.withConversation(h)) }

	mux.Handle("GET /v1/sessions", plain(s.handleListSessions))
	mux.Handle("POST /v1/sessions", plain(s.handleCreateSession))
	mux.Handle("DELETE /v1/sessions/{id}", scoped(s.handleDeleteSession))

	mux.Handle("GET /v1/sessions/{id}", scoped(s.handleSession))
	mux.Handle("GET /v1/sessions/{id}/report", scoped(s.handleSessionReport))
	mux.Handle("GET /v1/sessions/{id}/messages", scoped(s.handleMessages))
	mux.Handle("POST /v1/sessions/{id}/reset", scoped(s.handleReset))
	mux.Handle("GET /v1/sessions/{id}/config", scoped(s.handleConfig))
	mux.Handle("GET /v1/sessions/{id}/models", scoped(s.handleModels))
	mux.Handle("POST /v1/sessions/{id}/reasoning", scoped(s.handleReasoning))
	mux.Handle("POST /v1/sessions/{id}/verdict", scoped(s.handleVerdict))
	mux.Handle("GET /v1/sessions/{id}/reward", scoped(s.handleReward))
	mux.Handle("GET /v1/sessions/{id}/questions", scoped(s.handleQuestions))
	mux.Handle("POST /v1/sessions/{id}/task", scoped(s.handleTask))
	mux.Handle("POST /v1/sessions/{id}/plan", scoped(s.handlePlan))
	mux.Handle("GET /v1/sessions/{id}/run", scoped(s.handleRunStatus))
	mux.Handle("GET /v1/sessions/{id}/events", scoped(s.handleAttach))
	mux.Handle("POST /v1/sessions/{id}/cancel", scoped(s.handleCancelRun))
	mux.Handle("POST /v1/sessions/{id}/runs/approval", scoped(s.handleApproval))
	if s.opts.WebUI {
		// Authorised by the BEARER token specifically, not by the cookie: this is where the
		// token taken from the URL fragment is exchanged for the browser's cookie, and it must
		// stay single-entry. A browser that already holds a cookie has no business minting
		// itself another one, so requireBearer is used rather than the general check.
		mux.Handle("POST /v1/webui/session", requireBearer(s.opts.Token, http.HandlerFunc(s.handleWebUISession)))
	}
	return mux
}

// page registers one page file at one exact pattern.
func (s *Server) page(mux *http.ServeMux, pattern, name string) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body, ctype, err := webui.Content(name)
		if err != nil {
			// The name comes from this file, never from the request, so an error here means the
			// binary and the router disagree. A 404 is the honest answer to that.
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		// No caching. The page is part of the binary, so a client holding yesterday's copy
		// after an upgrade is running code that no longer exists - and a stale page calling an
		// API that moved is a failure nobody can explain.
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
}

// handleWebUISession exchanges the token for the browser's cookie.
//
// The token arrives as a bearer header, which the page's script took from the URL FRAGMENT: a
// fragment is never sent to the server and never appears in a log or a Referer, which is the
// only way to put a secret in a URL without it travelling. From here on the browser holds a
// DERIVED value, not the token (see cookieValue), so what a browser stores is not a credential
// that could be replayed against the API.
func (s *Server) handleWebUISession(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:  webuiCookie,
		Value: cookieValue(s.opts.Token),
		Path:  "/",
		// A script cannot read it, so an injected script cannot exfiltrate it.
		HttpOnly: true,
		// Another origin never sends it. Together with this server sending no CORS header at
		// all, a hostile page can neither send this credential nor read a response.
		SameSite: http.SameSiteStrictMode,
		// Long-lived because it is derived, not stored: it stays valid until the token rotates,
		// and rotating the token invalidates it with nothing to clean up.
		MaxAge: 30 * 24 * 3600,
		// NOT Secure, deliberately: this gateway speaks plain http (there is no TLS, and the
		// supported remote path is an SSH tunnel). A Secure cookie is one a browser refuses to
		// send over http, so setting it would look more careful and silently break the
		// interface - the browser would never stay connected, with nothing in any log to say why.
	})
	w.WriteHeader(http.StatusNoContent)
}

// originPolicy refuses requests from an origin the configured rules do not allow.
//
// It reads the client's address from the CONNECTION, never from a header. That is the whole
// security property of this middleware: X-Forwarded-For, X-Real-IP and friends are attacker-supplied
// strings, so honouring one would let anybody reach a gateway that had been restricted to one office
// address by simply claiming to be it. A header-based check is how an allow list turns into a
// decoration. (A deployment behind a real reverse proxy would need a setting naming the proxies it
// trusts; that setting does not exist, and this comment is why it must be a deliberate addition
// rather than an accident.)
//
// A NIL policy is the open one: it is what a caller that never configured a rule set passes, and it
// is the documented default rather than an error.
func (s *Server) originPolicy(next http.Handler) http.Handler {
	if s.opts.Allow == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RemoteAddr is "ip:port" as reported by the socket. An address that cannot be parsed is
		// NOT treated as allowed: a request whose origin cannot be established is exactly the one
		// to refuse, and net/rpc-style "the transport guarantees it" reasoning does not hold across
		// every listener Go supports.
		addrPort, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			writeError(w, http.StatusForbidden, "this gateway could not determine the origin of the request, so it will not serve it")
			return
		}
		if !s.opts.Allow.Allows(addrPort.Addr()) {
			// The message names the RULE SET, not the client's address alone: an operator reading
			// this from the other machine has to be able to tell "your rules do not cover me" from
			// "something is broken", and the rules are what they will go and edit.
			writeError(w, http.StatusForbidden, fmt.Sprintf(
				"this gateway does not serve requests from %s: gateway.allow is %s",
				addrPort.Addr(), s.opts.Allow.Describe()))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopback reports whether host names this machine only.
func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.opts.Version})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	// viewOf, never the configuration itself: see view.go.
	writeJSON(w, http.StatusOK, viewOf(convOf(r).svc.Config()))
}

// writeJSON is the one place a response body is produced, so the Content-Type is set in the one
// place it could be forgotten.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError is the refusal shape, so a client parses one thing and not five.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody reads a bounded JSON body and reports a failure the client can act on. It returns
// false when it has already written the refusal, and the caller then returns.
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.opts.MaxBodyKB)<<10)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		// MaxBytesReader makes the read fail, so an oversized body lands here too and is
		// reported as the malformed request it is. One shape for both: the client's fix is to
		// send less, and the message says which.
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the request body is not the JSON this endpoint expects: %v", err))
		return false
	}
	return true
}
