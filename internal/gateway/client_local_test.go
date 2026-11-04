package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madkoding/motita/internal/localmodel"
)

func TestClientLocalModelUsesEachRoute(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/v1/local-model/start" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"busy"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(localmodel.Status{ModelID: localmodel.ModelID})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "t")
	for _, action := range []string{"", "install", "delete"} {
		if st, err := c.LocalModel(context.Background(), action); err != nil || st.ModelID != localmodel.ModelID {
			t.Errorf("%q: %+v %v", action, st, err)
		}
	}
	if _, err := c.LocalModel(context.Background(), "start"); err == nil || err.Error() != "busy" {
		t.Errorf("start: %v", err)
	}
	want := []string{"GET /v1/local-model", "POST /v1/local-model/install", "DELETE /v1/local-model",
		"POST /v1/local-model/start"}
	for i, w := range want {
		if i >= len(seen) || seen[i] != w {
			t.Fatalf("requests = %v", seen)
		}
	}
}
