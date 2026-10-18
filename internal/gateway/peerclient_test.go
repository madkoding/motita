package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPeerRequestsFromTheTerminal(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+r.Header.Get("Content-Type"))
		if r.URL.Path == "/v1/peer-requests/gone" {
			writeError(w, http.StatusNotFound, "no such request")
			return
		}
		_, _ = io.WriteString(w, `{"self":{"name":"server","reachable":true},
			"requests":[{"id":"r 1","code":"123 456"}],"grants":[{"id":"g"}]}`)
	}))
	defer srv.Close()
	ctx := context.Background()
	view, err := ReadPeerRequests(ctx, srv.URL+"/", "tok")
	if err != nil || view.Name != "server" || !view.Reachable {
		t.Fatalf("%+v %v", view, err)
	}
	if len(view.Requests) != 1 || len(view.Grants) != 1 {
		t.Fatalf("%+v %v", view, err)
	}
	if err := DecidePeerRequest(ctx, srv.URL, "tok", "r 1", true); err != nil {
		t.Fatal(err)
	}
	if err := DecidePeerRequest(ctx, srv.URL, "tok", "gone", false); err == nil || err.Error() != "no such request" {
		t.Fatalf("refusal: %v", err)
	}
	want := []string{"GET /v1/peers Bearer tok ", "POST /v1/peer-requests/r 1 Bearer tok application/json"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("%q", got)
	}
	srv.Close()
	if _, err := ReadPeerRequests(ctx, srv.URL, "tok"); err == nil {
		t.Fatal("a gateway that is gone answered")
	}
	if _, err := ReadPeerRequests(ctx, "http://bad\x7f", "tok"); err == nil {
		t.Fatal("a bad address was asked")
	}
}
