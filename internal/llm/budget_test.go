package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestALengthCutBeforeAnyTextIsAskedAgainWithALargerBudget: a reasoning model can spend the
// whole token limit on hidden thinking and answer with 0 bytes. Repeating that request
// unchanged truncates again, so the second request must carry a larger limit.
func TestALengthCutBeforeAnyTextIsAskedAgainWithALargerBudget(t *testing.T) {
	var limits []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		limit, _ := body["max_tokens"].(float64)
		limits = append(limits, limit)
		content, finish := "", "length"
		if len(limits) > 1 {
			content, finish = "done", "stop"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": content}, "finish_reason": finish,
		}}})
	}))
	defer srv.Close()

	got, err := textClient(t, srv).Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil || got != "done" {
		t.Fatalf("got %q, %v; want the answer of the second request", got, err)
	}
	if len(limits) != 2 || limits[1] <= limits[0] {
		t.Fatalf("limits sent = %v, want the second larger than the first", limits)
	}
}
