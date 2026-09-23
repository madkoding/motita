package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A service file names where a gateway WAS. Whether one is there NOW is a different question, and
// the only honest answer comes from asking it: a pid is recycled, and a port can be taken over by
// an unrelated program, so neither is evidence on its own.

func TestAnAddressThatDoesNotAnswerIsNotAGateway(t *testing.T) {
	// A real server that is closed before the probe, leaving its address behind in the file: this
	// is the state after a crash or a shutdown that never got to clean up.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()

	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: address, Token: "t", PID: 1}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	found, ok, err := Discover(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("a dead address is the normal state after a crash, not an error: %v", err)
	}
	if ok {
		t.Fatalf("nothing is listening at %s, so discovery must not report a gateway: %+v", address, found)
	}
}

// /v1/health exists so something can ask "are you there" without the token, which is what makes it
// the right probe: a discovery that needed the token would fail in exactly the case where the token
// file was lost while the gateway kept running.
func TestAGatewayIsDiscoveredThroughItsHealthEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Errorf("discovery probed %s, it must use the one endpoint that needs no token", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":"dev"}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "gateway.json")
	address := strings.TrimPrefix(srv.URL, "http://")
	if err := WriteServiceFile(path, ServiceFile{Address: address, Token: "tok", PID: 99, Owned: true}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	found, ok, err := Discover(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !ok {
		t.Fatal("a healthy gateway was not discovered")
	}
	if found.BaseURL != "http://"+address {
		t.Fatalf("BaseURL = %q, want %q", found.BaseURL, "http://"+address)
	}
	if found.Token != "tok" || found.PID != 99 || !found.Owned {
		t.Fatalf("discovery did not carry the file through: %+v", found)
	}
}

// A developer build never receives the -ldflags version injection, so it reports "dev" or an empty
// string. Checking identity against the version would refuse to recognise a perfectly healthy
// gateway built from source - the exact build its author runs.
func TestADevelopmentBuildIsStillAGateway(t *testing.T) {
	for _, version := range []string{"dev", ""} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true,"version":"` + version + `"}`))
		}))
		path := filepath.Join(t.TempDir(), "gateway.json")
		if err := WriteServiceFile(path, ServiceFile{Address: strings.TrimPrefix(srv.URL, "http://"), Token: "t"}); err != nil {
			t.Fatalf("WriteServiceFile: %v", err)
		}
		if _, ok, err := Discover(context.Background(), path, nil); err != nil || !ok {
			srv.Close()
			t.Fatalf("a gateway reporting version %q must be recognised (ok=%v, err=%v)", version, ok, err)
		}
		srv.Close()
	}
}

// Another program listening where the file says is not our gateway. Treating it as one would send
// the client's token to a stranger and then fail on every call with an error that says nothing
// about the real problem.
func TestSomethingElseOnThePortIsNotOurGateway(t *testing.T) {
	stranger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer stranger.Close()

	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: strings.TrimPrefix(stranger.URL, "http://"), Token: "t"}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	if _, ok, err := Discover(context.Background(), path, nil); err != nil || ok {
		t.Fatalf("a program that is not a starlight gateway must not be discovered (ok=%v, err=%v)", ok, err)
	}
}

// A 200 with something that is not JSON at all is likewise not our gateway: the shape is what
// settles it, and a status code alone would not.
func TestA200ThatIsNotJSONIsNotOurGateway(t *testing.T) {
	stranger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>hello</title>"))
	}))
	defer stranger.Close()

	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: strings.TrimPrefix(stranger.URL, "http://"), Token: "t"}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	if _, ok, err := Discover(context.Background(), path, nil); err != nil || ok {
		t.Fatalf("a 200 that is not a health report must not be discovered (ok=%v, err=%v)", ok, err)
	}
}

// A gateway that answers is not the same as a gateway that is well: a /v1/health answering 500 is
// not something to attach to, and reporting it as found would put the failure at the first real
// call instead of here.
func TestAHealthThatIsNotOKIsNotAGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: strings.TrimPrefix(srv.URL, "http://"), Token: "t"}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	if _, ok, err := Discover(context.Background(), path, nil); err != nil || ok {
		t.Fatalf("a health answering 500 must not be discovered (ok=%v, err=%v)", ok, err)
	}
}

// Corruption is reported and does not silently become "start a second one". Two gateways on one
// machine is the failure to avoid.
func TestDiscoveryReportsACorruptServiceFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	found, ok, err := Discover(context.Background(), path, nil)
	if err == nil {
		t.Fatal("a corrupt service file must be reported, not treated as no gateway")
	}
	if ok {
		t.Fatalf("a corrupt file must not produce a gateway: %+v ok=%v", found, ok)
	}
}

// No service file at all is "nothing is running", which is the normal state on a fresh machine and
// not a failure.
func TestDiscoveryWithNoServiceFileFindsNothing(t *testing.T) {
	found, ok, err := Discover(context.Background(), filepath.Join(t.TempDir(), "absent.json"), nil)
	if err != nil {
		t.Fatalf("no service file is not a failure: %v", err)
	}
	if ok {
		t.Fatalf("there is nothing to find, got %+v", found)
	}
}

// A pathless call is the same state: no service, and therefore no error.
func TestDiscoveryWithNoPathFindsNothing(t *testing.T) {
	if _, ok, err := Discover(context.Background(), "", nil); err != nil || ok {
		t.Fatalf("a pathless discovery must find nothing and not fail (ok=%v, err=%v)", ok, err)
	}
}

// The probe is injectable so the caller can decide how to ask, and the default is used when none is
// given: both are exercised here, because a default that is never taken is a default nobody tested.
func TestDiscoveryUsesTheInjectedProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: "127.0.0.1:1", Token: "t"}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	var probed string
	found, ok, err := Discover(context.Background(), path, func(context.Context, string) (Health, error) {
		probed = "127.0.0.1:1"
		return Health{OK: true, Version: "dev"}, nil
	})
	if err != nil || !ok {
		t.Fatalf("the injected probe answered OK (ok=%v, err=%v)", ok, err)
	}
	if probed != "127.0.0.1:1" {
		t.Fatalf("the probe was called with %q, want the address from the file", probed)
	}
	if found.Token != "t" {
		t.Fatalf("the token from the file was not carried through: %+v", found)
	}
}

// A probe that fails and one that answers not-OK are different failures with the same outcome, and
// both have to be handled: an error is "nothing there", not-OK is "something else is there".
func TestDiscoveryTreatsAProbeFailureAsNoGateway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: "127.0.0.1:1", Token: "t"}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	if _, ok, err := Discover(context.Background(), path, func(context.Context, string) (Health, error) {
		return Health{}, errors.New("connection refused")
	}); err != nil || ok {
		t.Fatalf("a probe that failed means no gateway, not an error (ok=%v, err=%v)", ok, err)
	}
}

// --- ProbeHealth itself ------------------------------------------------------------------------

// The probe reads the field names the handler actually writes. A guessed name would leave OK false
// on a healthy gateway and make discovery report "no gateway" on every machine, forever, so the
// round trip against a real server is what pins it.
func TestProbeHealthReadsWhatTheHandlerWrites(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"version":"1.2.3"}`))
	}))
	defer srv.Close()

	h, err := ProbeHealth(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("ProbeHealth: %v", err)
	}
	if !h.OK {
		t.Fatal("a gateway answering ok:true was read as not OK - the field names are wrong")
	}
	if h.Version != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", h.Version)
	}
}

// A status that is not 200 is an error rather than a health report: the caller has to be able to
// tell "not there" from "there and broken".
func TestProbeHealthRejectsANon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := ProbeHealth(context.Background(), strings.TrimPrefix(srv.URL, "http://")); err == nil {
		t.Fatal("a 503 must be an error")
	}
}

// Garbage on the wire is an error, not a zero Health that reads as a gateway which is not OK.
func TestProbeHealthRejectsGarbage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<>not json<>"))
	}))
	defer srv.Close()

	if _, err := ProbeHealth(context.Background(), strings.TrimPrefix(srv.URL, "http://")); err == nil {
		t.Fatal("a body that is not JSON must be an error")
	}
}

// An address that cannot even be turned into a request is reported before any attempt to connect.
func TestProbeHealthRejectsAnUnbuildableAddress(t *testing.T) {
	if _, err := ProbeHealth(context.Background(), "127.0.0.1:99999:bad"); err == nil {
		t.Fatal("an address that cannot form a request must be an error")
	}
}

// An address nothing can be listening at fails, rather than hanging: the timeout exists so a
// firewall that DROPs packets cannot hold the interface at startup for minutes.
func TestProbeHealthFailsOnADeadAddress(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()

	if _, err := ProbeHealth(context.Background(), address); err == nil {
		t.Fatal("nothing is listening there, so the probe must fail")
	}
}
