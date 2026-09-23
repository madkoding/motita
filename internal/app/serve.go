package app

import (
	"context"
	"errors"
	"fmt"
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
	srv, err := op.startGateway(fl, cfg, engine, box, log)
	if err != nil {
		fmt.Fprintf(op.Err, "the gateway could not start: %v\n", err)
		return ConfigError
	}
	defer op.runGatewayLoop(ctx, srv, log)()

	// Serve until the process is asked to stop. The signal handling already in run() cancels
	// this context; nothing else ends a server.
	<-ctx.Done()
	log.Info("the gateway is shutting down")
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
func (op Options) startGateway(fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger) (*gateway.Server, error) {
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

	return gateway.Start(gateway.Options{
		Service:     runner,
		NewService:  newService,
		MaxSessions: cfg.Gateway.MaxSessions,
		Listen:      listen,
		Token:       token,
		AllowLAN:    cfg.Gateway.AllowLAN,
		MaxBodyKB:   cfg.Gateway.MaxBodyKB,
		Version:     op.Version,
		Log:         log,
	})
}
