package gitforge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/oauth"
)

func TestSummarize(t *testing.T) {
	cases := []struct {
		in   []Check
		want string
	}{
		{nil, StateNone},
		{[]Check{{State: StateSuccess}}, StateSuccess},
		{[]Check{{State: StateSuccess}, {State: StatePending}}, StatePending},
		{[]Check{{State: StatePending}, {State: StateFailure}}, StateFailure},
	}
	for _, c := range cases {
		if got := summarize(c.in).State; got != c.want {
			t.Errorf("%+v: %s, want %s", c.in, got, c.want)
		}
	}
}

func TestMapState(t *testing.T) {
	for in, want := range map[string]string{
		"SUCCESSFUL": StateSuccess, "success": StateSuccess, "skipped": StateSuccess, "manual": StateSuccess,
		"FAILED": StateFailure, "error": StateFailure, "canceled": StateFailure, "STOPPED": StateFailure,
		"INPROGRESS": StatePending, "running": StatePending, "": StatePending,
	} {
		if got := mapState(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

func TestCIGitHub(t *testing.T) {
	a, _ := newAPI(t, KindGitHub, map[string]string{
		"GET /repos/o/r/pulls/5":                `{"head":{"sha":"abc"}}`,
		"GET /repos/o/r/commits/abc/check-runs": `{"check_runs":[{"id":11,"name":"build","status":"completed","conclusion":"success","html_url":"u1"},{"id":12,"name":"test","status":"completed","conclusion":"failure","html_url":"u2","output":{"title":"2 tests failed\nmore","summary":"s"}},{"id":13,"name":"lint","status":"in_progress"},{"id":14,"name":"docs","status":"completed","conclusion":"cancelled","output":{"summary":"only a summary"}}]}`,
		"GET /repos/o/r/commits/abc/status":     `{"statuses":[{"context":"legacy","state":"success","target_url":"u3","description":"ok"}]}`,
	})
	st, err := a.CI(context.Background(), Remote{Path: "o/r"}, 5)
	if err != nil || st.State != StateFailure || len(st.Checks) != 5 {
		t.Fatalf("%+v %v", st, err)
	}
	if st.Checks[1].Detail != "2 tests failed" || st.Checks[1].jobID != 12 || st.Checks[2].State != StatePending || st.Checks[3].Detail != "only a summary" {
		t.Errorf("checks = %+v", st.Checks)
	}
}

func TestCIGitea(t *testing.T) {
	a, _ := newAPI(t, KindGitea, map[string]string{
		"GET /repos/o/r/pulls/5":            `{"head":{"sha":"abc"}}`,
		"GET /repos/o/r/commits/abc/status": `{"statuses":[{"context":"ci","status":"success"},{"context":"x","state":"pending"}]}`,
	})
	st, err := a.CI(context.Background(), Remote{Path: "o/r"}, 5)
	if err != nil || st.State != StatePending || len(st.Checks) != 2 || st.Checks[0].State != StateSuccess {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestCIGitHubFailures(t *testing.T) {
	r := Remote{Path: "o/r"}
	a, _ := newAPI(t, KindGitHub, nil)
	if _, err := a.CI(context.Background(), r, 5); err == nil {
		t.Error("a missing pull request must surface")
	}
	a, _ = newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/pulls/5": `{}`})
	if _, err := a.CI(context.Background(), r, 5); err == nil {
		t.Error("a pull request with no commit must be an error")
	}
	a, _ = newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/pulls/5": `{"head":{"sha":"abc"}}`})
	if _, err := a.CI(context.Background(), r, 5); err == nil {
		t.Error("check runs failing must surface")
	}
	a, _ = newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/pulls/5": `{"head":{"sha":"abc"}}`, "GET /repos/o/r/commits/abc/check-runs": `{}`})
	if _, err := a.CI(context.Background(), r, 5); err == nil {
		t.Error("statuses failing must surface")
	}
	a, _ = newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/pulls/5": `{"head":{"sha":"abc"}}`, "GET /repos/o/r/commits/abc/check-runs": `{}`, "GET /repos/o/r/commits/abc/status": `{}`})
	if st, err := a.CI(context.Background(), r, 5); err != nil || st.State != StateNone {
		t.Errorf("no checks: %+v %v", st, err)
	}
}

func TestCIGitLab(t *testing.T) {
	r := Remote{Path: "g/r"}
	a, _ := newAPI(t, KindGitLab, map[string]string{
		"GET /projects/g%2Fr/merge_requests/3/pipelines": `[{"id":99},{"id":98}]`,
		"GET /projects/g%2Fr/pipelines/99/jobs":          `[{"name":"build","status":"success","web_url":"u1"},{"name":"flaky","status":"failed","allow_failure":true},{"name":"test","status":"failed","web_url":"u2","failure_reason":"script_failure"},{"name":"deploy","status":"running"}]`,
	})
	st, err := a.CI(context.Background(), r, 3)
	if err != nil || st.State != StateFailure || len(st.Checks) != 4 || st.Checks[1].State != StateSuccess || st.Checks[2].Detail != "script_failure" {
		t.Fatalf("%+v %v", st, err)
	}
	a, _ = newAPI(t, KindGitLab, map[string]string{"GET /projects/g%2Fr/merge_requests/3/pipelines": `[]`})
	if st, err = a.CI(context.Background(), r, 3); err != nil || st.State != StateNone {
		t.Errorf("%+v %v", st, err)
	}
	a, _ = newAPI(t, KindGitLab, nil)
	if _, err = a.CI(context.Background(), r, 3); err == nil {
		t.Error("pipelines failing must surface")
	}
	a, _ = newAPI(t, KindGitLab, map[string]string{"GET /projects/g%2Fr/merge_requests/3/pipelines": `[{"id":1}]`})
	if _, err = a.CI(context.Background(), r, 3); err == nil {
		t.Error("jobs failing must surface")
	}
}

func TestCIBitbucket(t *testing.T) {
	r := Remote{Path: "ws/r"}
	a, _ := newAPI(t, KindBitbucket, map[string]string{"GET /repositories/ws/r/pullrequests/2/statuses": `{"values":[{"name":"pipe","state":"SUCCESSFUL","url":"u","description":"d"},{"name":"b","state":"INPROGRESS"}]}`})
	st, err := a.CI(context.Background(), r, 2)
	if err != nil || st.State != StatePending || len(st.Checks) != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	a, _ = newAPI(t, KindBitbucket, nil)
	if _, err = a.CI(context.Background(), r, 2); err == nil {
		t.Error("a host error must surface")
	}
}

func TestFailureLog(t *testing.T) {
	r := Remote{Path: "o/r"}
	big := strings.Repeat("x", logTail) + "THE END"
	a, h := newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/actions/jobs/12/logs": big})
	log, ok, err := a.FailureLog(context.Background(), r, Check{jobID: 12})
	if err != nil || !ok || len(log) != logTail || !strings.HasSuffix(log, "THE END") || h.auth != "Bearer tok" {
		t.Fatalf("len=%d ok=%v err=%v", len(log), ok, err)
	}
	log, ok, _ = a.FailureLog(context.Background(), r, Check{jobID: 12})
	_ = log
	// A short log is returned whole.
	a, _ = newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/actions/jobs/12/logs": "short"})
	if log, ok, _ = a.FailureLog(context.Background(), r, Check{jobID: 12}); log != "short" || !ok {
		t.Errorf("log = %q", log)
	}
	// No job id, or a host that cannot serve one: not an error, just not available.
	if _, ok, err = a.FailureLog(context.Background(), r, Check{}); ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
	gl, _ := newAPI(t, KindGitLab, nil)
	if _, ok, err = gl.FailureLog(context.Background(), r, Check{jobID: 1}); ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
	a, _ = newAPI(t, KindGitHub, nil)
	if _, _, err = a.FailureLog(context.Background(), r, Check{jobID: 12}); err == nil {
		t.Error("a refused log must be an error")
	}
	dead := API{Service: Service{Kind: KindGitHub, APIBase: "http://127.0.0.1:1"}, Cred: oauth.Credential{AccessToken: "t"}}
	if _, _, err = dead.FailureLog(context.Background(), r, Check{jobID: 1}); err == nil {
		t.Error("an unreachable host must be an error")
	}
	bad := API{Service: Service{Kind: KindGitHub, APIBase: "http://bad host/%"}}
	if _, _, err = bad.FailureLog(context.Background(), r, Check{jobID: 1}); err == nil {
		t.Error("a malformed URL must be an error")
	}
	// The default client is used when none is given.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }))
	defer srv.Close()
	plain := API{Service: Service{Kind: KindGitHub, APIBase: srv.URL}, Cred: oauth.Credential{AccessToken: "t"}}
	if log, ok, _ = plain.FailureLog(context.Background(), r, Check{jobID: 1}); log != "hi" || !ok {
		t.Errorf("log = %q ok=%v", log, ok)
	}
}

func TestFirstLineOf(t *testing.T) {
	if firstLineOf(" a \nb") != "a" || firstLineOf("  ") != "" {
		t.Error("firstLineOf")
	}
}
