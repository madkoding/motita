package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Found is a gateway that answered.
type Found struct {
	BaseURL string
	Token   string
	PID     int
	// Version is what the gateway reported about ITSELF, and it is deliberately not this process's
	// version: when a client attaches to a gateway somebody else is running, showing the client's
	// own build would be a confident answer to a question nobody asked. The health endpoint already
	// sends it, and dropping it here is what left the version unreachable from every front end.
	Version string
	// Owned is carried through from the service file: whoever wrote it decided whether the process
	// that finds it is responsible for shutting it down.
	Owned bool
	// Reachable is carried through from the service file and says whether the gateway that wrote it
	// was bound beyond loopback. It travels with the file rather than being inferred from BaseURL,
	// because BaseURL is deliberately the CALLABLE address (loopback when the socket is a wildcard)
	// and so says nothing about exposure. A reader who derives exposure from it gets the answer
	// backwards for exactly the gateways it matters for.
	Reachable bool
	// Allow is the origin rule set the RUNNING gateway enforces, as it describes itself.
	//
	// It travels in the service file for the same reason Reachable does: this process's own
	// configuration is not evidence about a gateway somebody else is running, and a service left
	// running across an upgrade or a configuration edit enforces the rules it was started with.
	Allow string
}

// Health is what /v1/health reports, and the shape discovery checks for.
//
// The field names are the ones the handler actually writes - `ok` and `version`, from
// server.go:233 - and NOT a `status` string. Reading the wire format from the handler rather than
// from memory is the whole point: a guessed field name leaves OK false on a healthy gateway, and
// discovery then reports "no gateway" on every machine, forever.
type Health struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

// Discover reports the gateway running at the address the service file names, if one is.
//
// The probe is /v1/health, which is the one endpoint that does not require the token. That is not a
// detail: a discovery that needed the token would fail in exactly the case where the token file was
// lost while the gateway kept running, and it would report "no gateway" for a gateway that is
// plainly there.
//
// The body is parsed rather than the status alone trusted. Another program listening on the same
// port would answer 200 to anything, and the client would then send it the bearer token and fail on
// every call with an error that says nothing about the real problem. A shape check is what turns
// that into "this is not a motita gateway".
//
// A missing service file returns found=false with no error: nothing running is the normal state,
// not a failure. A corrupt one returns an error, because the alternative is starting a second
// gateway beside a live one.
func Discover(ctx context.Context, path string, probe func(context.Context, string) (Health, error)) (Found, bool, error) {
	svc, err := ReadServiceFile(path)
	if err != nil {
		return Found{}, false, err
	}
	if svc.IsZero() {
		return Found{}, false, nil
	}

	if probe == nil {
		probe = ProbeHealth
	}
	health, err := probe(ctx, svc.Address)
	if err != nil {
		// The address is remembered but nothing is there. Not an error: this is the state after a
		// crash or a shutdown that did not get to clean up, and the caller starts a fresh one.
		return Found{}, false, nil
	}
	if !health.OK {
		// Something answered that is not a motita gateway. The check is on `ok` and NOT on the
		// version string, which is the trap: the build injects the version with -ldflags, and a
		// developer build that never received the injection reports "dev" or an empty string. A
		// version check would then refuse to recognise a perfectly healthy gateway built from source -
		// the exact build its author runs.
		return Found{}, false, nil
	}
	return Found{
		BaseURL:   "http://" + svc.Address,
		Token:     svc.Token,
		PID:       svc.PID,
		Version:   health.Version,
		Owned:     svc.Owned,
		Reachable: svc.Reachable,
		Allow:     svc.Allow,
	}, true, nil
}

// ProbeHealth asks a gateway whether it is there, over a bounded request.
//
// The timeout is what keeps a machine with a firewall that DROPs packets from hanging the interface
// at startup: a connect that is neither accepted nor refused would otherwise wait out the operating
// system's own timeout, which is measured in minutes.
func ProbeHealth(ctx context.Context, address string) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/v1/health", nil)
	if err != nil {
		return Health{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Health{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("/v1/health answered %d", resp.StatusCode)
	}
	var h Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return Health{}, err
	}
	return h, nil
}
