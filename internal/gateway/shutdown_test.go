package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The shutdown endpoint stops the gateway through Options.Shutdown, over the bearer token only.
func TestShutdownEndpointStopsTheGatewayWithTheBearerToken(t *testing.T) {
	var stopped atomic.Int32
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Shutdown = func() { stopped.Add(1) } })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	if err := RequestShutdown(context.Background(), ts.URL, "wrong"); err == nil {
		t.Fatal("a wrong token was accepted")
	}
	if stopped.Load() != 0 {
		t.Fatal("a refused request stopped the gateway")
	}
	if err := RequestShutdown(context.Background(), ts.URL+"/", testToken); err != nil {
		t.Fatalf("RequestShutdown = %v", err)
	}
	if stopped.Load() != 1 {
		t.Fatalf("Shutdown called %d times, want 1", stopped.Load())
	}
}

// A gateway that cannot stop itself (the one inside a terminal) refuses, and the client says why.
func TestShutdownEndpointRefusesWithoutAShutdown(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := sendNetwork(t, srv, http.MethodPost, "/v1/shutdown", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("POST /v1/shutdown = %d, want 409", w.Code)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	if err := RequestShutdown(context.Background(), ts.URL, testToken); err == nil ||
		!strings.Contains(err.Error(), "terminal") {
		t.Fatalf("RequestShutdown = %v, want the refusal", err)
	}
}

// The request's own failures are reported: an address that cannot be built and one nothing answers.
func TestRequestShutdownReportsTransportFailures(t *testing.T) {
	if err := RequestShutdown(context.Background(), "http://bad host", testToken); err == nil {
		t.Error("an invalid URL was accepted")
	}
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close()
	if err := RequestShutdown(context.Background(), url, testToken); err == nil {
		t.Error("a closed address reported success")
	}
}
