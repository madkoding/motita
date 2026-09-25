package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A gateway started without a schedule directory answers an EMPTY LIST, not an error:
// scheduling is off in that deployment, and a front end that draws an empty list is
// telling the truth. This mirrors /v1/projects, which answers an empty list too.
func TestAScheduleListWithoutAStoreIsEmpty(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, "/v1/schedules", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Schedules []map[string]any `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if body.Schedules == nil || len(body.Schedules) != 0 {
		t.Fatalf("schedules = %v, want an empty list and never null", body.Schedules)
	}
}
