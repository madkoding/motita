package gateway

import (
	"context"
	"net/http"
	"strings"
)

// handleShutdown stops this gateway the way SIGTERM does on unix.
//
// It exists for the platform without SIGTERM. On Windows the only thing `gateway stop` could send
// a process was os.Kill, which ends it on the spot: no session saved, no run told it was
// cancelled, no listener closed. Asking over the local API instead reaches the same graceful path
// a signal reaches elsewhere - Options.Shutdown cancels the context the service runs under and
// Close does the rest - and the CLI only falls back to the kill when this request fails.
//
// The answer is written BEFORE the shutdown is requested, so the caller learns it was accepted;
// the shutdown itself is only a cancellation and does not block, and the graceful close lets this
// response finish.
func (s *Server) handleShutdown(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Shutdown == nil {
		writeError(w, http.StatusConflict, "this gateway lives inside a terminal's interface and is stopped by closing it")
		return
	}
	s.info("the gateway was asked to shut down over its API")
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	s.opts.Shutdown()
}

// RequestShutdown asks the gateway at baseURL to stop, with its bearer token.
//
// A nil error means the gateway ACCEPTED the request, not that it has stopped: the caller still
// waits for it to stop answering, exactly as it waits after a signal.
func RequestShutdown(ctx context.Context, baseURL, token string) error {
	url := strings.TrimSuffix(baseURL, "/") + "/v1/shutdown"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", bearerPrefix+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return refusalError(resp)
	}
	return nil
}
