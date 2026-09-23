package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/starlight/internal/logx"
)

// defaultMaxBodyKB caps a request body when nothing else is configured. A task or a prompt is a
// sentence; 256 KB is generous for one and small enough that a client cannot make the agent
// chew on a novel.
const defaultMaxBodyKB = 256

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
	// AllowLAN must be true for a listen address that is not loopback. Two deliberate acts are
	// required to put an agent that runs commands on this machine on a network, and this is
	// the second one - the first is writing a non-loopback address at all.
	AllowLAN bool
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
}

// Server is the HTTP face of the conversations this process holds.
type Server struct {
	opts     Options
	listener net.Listener
	server   *http.Server
	mux      *http.ServeMux

	// closeOnce makes Close idempotent. Close is called from a defer in the app AND from the
	// shutdown path, and a second Shutdown on an already-closed listener returns an error the
	// caller would have to know to ignore.
	closeOnce sync.Once
	closeErr  error

	// sessionsMu guards the registry. The conversations themselves are safe to use without it:
	// each is guarded by its own locks, and this is only read to find one.
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
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("the listen address %q is not host:port: %w", addr, err)
	}
	if !opts.AllowLAN && !isLoopback(host) {
		return nil, fmt.Errorf("the listen address %q is not loopback; set gateway.allow_lan to accept that anything on the network can drive this agent", addr)
	}

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
	s := &Server{opts: opts, listener: ln, sessions: map[string]*conversation{DefaultSession: first}}
	s.mux = s.routes()
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

// Addr is the address that was actually bound, with the real port when 0 was asked for.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// BaseURL is the origin a client should speak to.
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
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
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
	mux.Handle("POST /v1/sessions/{id}/reset", scoped(s.handleReset))
	mux.Handle("GET /v1/sessions/{id}/config", scoped(s.handleConfig))
	mux.Handle("GET /v1/sessions/{id}/models", scoped(s.handleModels))
	mux.Handle("POST /v1/sessions/{id}/reasoning", scoped(s.handleReasoning))
	mux.Handle("POST /v1/sessions/{id}/verdict", scoped(s.handleVerdict))
	mux.Handle("GET /v1/sessions/{id}/reward", scoped(s.handleReward))
	mux.Handle("GET /v1/sessions/{id}/questions", scoped(s.handleQuestions))
	mux.Handle("POST /v1/sessions/{id}/task", scoped(s.handleTask))
	mux.Handle("POST /v1/sessions/{id}/plan", scoped(s.handlePlan))
	mux.Handle("POST /v1/sessions/{id}/runs/approval", scoped(s.handleApproval))
	return mux
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
