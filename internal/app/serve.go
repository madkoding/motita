package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/gateway"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/procedures"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/tui"
)

// runServe runs the gateway and no interface.
//
// The runner is the same tui.AppRunner the text interface uses, which is the whole point of the
// design: there is ONE agent, and a client on a phone and a terminal in front of the machine talk
// to the same conversation, the same procedure library and the same reward ledger.
func (op Options) runServe(ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger) int {
	// owned=false: this IS the service the user asked for. It stops when it is told to, and nothing
	// about the interface's lifetime has any say over it.
	srv, err := op.startGateway(fl, cfg, engine, box, log, false)
	if err != nil {
		fmt.Fprintf(op.Err, "the gateway could not start: %v\n", err)
		return ConfigError
	}
	defer op.runGatewayLoop(ctx, srv, log)()

	// Serve until the process is asked to stop. The signal handling already in run() cancels
	// this context; nothing else ends a server.
	<-ctx.Done()
	log.Info("the gateway is shutting down")

	// The description of a gateway that is no longer listening is worse than none: discovery trusts
	// it, and the next start would probe an address nothing answers on before deciding to bring its
	// own up. Removed here rather than by the client, because this process is the one that knows it
	// has stopped.
	_ = gateway.RemoveServiceFile(op.serviceFilePath())
	return Success
}

// runGatewayLoop starts the server in the background and returns the function that shuts it
// down.
//
// It is shared by both modes on purpose: -serve and the interface differ in what they DRAW, not
// in how the gateway is run, and two copies of this would be two places for the shutdown to
// drift. The interface in particular would silently stop closing its gateway if only one copy
// were fixed.
//
// The returned function is what the caller defers, and it does not block the shutdown on a
// client that never goes away: Server.Close is bounded by the same context the process was
// cancelled with.
func (op Options) runGatewayLoop(ctx context.Context, srv *gateway.Server, log *logx.Logger) func() {
	go func() {
		serve := func(*gateway.Server) error { return srv.Serve() }
		if op.ServeGateway != nil {
			serve = op.ServeGateway
		}
		if err := serve(srv); err != nil {
			log.Error("the gateway stopped with an error", "error", err)
		}
	}()

	closeGateway := func(*gateway.Server, context.Context) error { return srv.Close(ctx) }
	if op.CloseGateway != nil {
		closeGateway = op.CloseGateway
	}
	return func() {
		if err := closeGateway(srv, ctx); err != nil {
			log.Warn("the gateway did not shut down cleanly", "error", err)
		}
	}
}

// startGateway binds the listener and returns the running server.
//
// The token is read or created here rather than in the TUI, because BOTH modes need it and a
// server that generated one in two places would eventually generate two.
//
// owned says whether the process that asked for this gateway is responsible for shutting it down.
// It is written into the service file and NOT inferred from who called what, because the inference
// is wrong in the case that matters: a user starts a service and then opens a terminal, and "the
// interface started it" would be false - the interface found it. Getting it wrong either kills a
// service the user asked to keep, or leaks one nobody asked for.
//
// The file is written with the EFFECTIVE address (srv.Addr()), not the one that was asked for: with
// a port of 0 the real port is chosen at bind time, and writing the zero down would leave a file
// nobody can use.
func (op Options) startGateway(fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger, owned bool) (*gateway.Server, error) {
	switch {
	case strings.EqualFold(strings.TrimSpace(fl.gateway), "off"):
		return nil, errors.New("the gateway was turned off with -gateway off")
	}
	// The token is generated BEFORE the listener is bound: a gateway that could not keep its
	// token must not be listening at all, and a listener that opened first would have to be
	// closed again on a path nothing exercises.
	token, err := gateway.LoadOrCreateToken(cfg.Gateway.TokenFile)
	if err != nil {
		return nil, err
	}
	listen := cfg.Gateway.Listen
	if v := strings.TrimSpace(fl.gateway); v != "" {
		listen = v
	}
	runner := tui.NewAppRunner(op.Out, op.Err, cfg, engine, box, log)

	// ONE procedure store for every conversation this process will ever hold.
	//
	// Shared deliberately: the store is the library AND its reward ledger, and the ledger is a
	// file read once into memory. Two conversations with two stores over one file would each save
	// their own view over the other's, and a verdict would be lost with nothing to show for it.
	// One store means one in-memory truth, and the ledger's lock makes it safe from two runs.
	store := procedures.Open(cfg, log)
	runner.UseStore(store)

	// NewService is what lets a client open a conversation of its own. It is wired in BOTH modes,
	// because the interface's gateway IS the gateway -serve starts: the difference between them is
	// what they DRAW, not who may talk to them.
	newService := func() (gateway.Service, error) {
		r := tui.NewAppRunner(op.Out, op.Err, cfg, engine, box, log)
		r.UseStore(store)
		return r, nil
	}

	srv, err := gateway.Start(gateway.Options{
		Service:     runner,
		NewService:  newService,
		MaxSessions: cfg.Gateway.MaxSessions,
		Listen:      listen,
		Token:       token,
		AllowLAN:    cfg.Gateway.AllowLAN,
		MaxBodyKB:   cfg.Gateway.MaxBodyKB,
		Version:     op.Version,
		Log:         log,
		// The interface is served from THIS mux, so the page and the API share an origin
		// and no proxy or CORS is involved anywhere.
		WebUI: cfg.Gateway.WebUI,
	})
	if err != nil {
		return nil, err
	}

	// Published AFTER the bind, because the address is only known then, and BEFORE anything is
	// served: a client that looked in that window would find nothing and start a second gateway.
	//
	// A failure to write it is REPORTED and not fatal. The file is a convenience - it carries the
	// token and the pid for the commands that act on a service - while the gateway itself is
	// reachable at its address either way, and the port is fixed by default so it is findable
	// without help. Refusing to serve because a home directory is not writable would turn a
	// read-only `$HOME` (a container running as an immutable user is the ordinary case) into "the
	// gateway does not work at all", which is a far worse failure than the one it would prevent.
	// The end-to-end run is what found this: it serves with `HOME=/`.
	if err := op.WriteServiceFile(op.serviceFilePath(), gateway.ServiceFile{
		Address: srv.Addr(),
		Token:   token,
		PID:     os.Getpid(),
		Owned:   owned,
	}); err != nil {
		if log != nil {
			log.Warn("the gateway is serving but could not record where it is, so "+
				"`gateway status` and `gateway stop` will not find it; it is at "+srv.Addr(),
				"error", err.Error(), "path", op.serviceFilePath())
		}
	}
	return srv, nil
}
