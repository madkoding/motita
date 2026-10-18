package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/gateway"
)

// fakePeers is a gateway answering the computer endpoints: view is what GET /v1/peers answers,
// and decided records what POST /v1/peer-requests/{id} was told.
type fakePeers struct {
	view     string
	status   int
	decided  []string
	decision int
}

func (f *fakePeers) serve(t *testing.T) Options {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/peers":
			if f.status != 0 {
				w.WriteHeader(f.status)
				_, _ = io.WriteString(w, `{"error":"broken"}`)
				return
			}
			_, _ = io.WriteString(w, f.view)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/peer-requests/"):
			var body struct{ Allow bool }
			_ = json.NewDecoder(r.Body).Decode(&body)
			mark := map[bool]string{true: "+", false: "-"}[body.Allow]
			f.decided = append(f.decided, strings.TrimPrefix(r.URL.Path, "/v1/peer-requests/")+mark)
			if f.decision != 0 {
				w.WriteHeader(f.decision)
				_, _ = io.WriteString(w, `{"error":"gone"}`)
				return
			}
			_, _ = io.WriteString(w, f.view)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	op, _, _ := netOptions("")
	op.DiscoverGateway = func(context.Context, string) (gateway.Found, bool, error) {
		return gateway.Found{BaseURL: srv.URL + "/", Token: "tok"}, true, nil
	}
	return op
}

const asking = `{"self":{"name":"server","reachable":true},
	"requests":[{"id":"r1","name":"laptop","address":"fd7a::1","code":"123 456"}],
	"grants":[{"id":"g1","name":"desk"}]}`

func outputs(op Options) (string, string) {
	return op.Out.(interface{ String() string }).String(), op.Err.(interface{ String() string }).String()
}

func TestComputersListsTheOnesAsking(t *testing.T) {
	old := computerAddrs
	t.Cleanup(func() { computerAddrs = old })
	computerAddrs = func() []string { return []string{"fd7a::2", "100.64.0.2"} }
	op := (&fakePeers{view: asking}).serve(t)
	if code := op.runGatewayCommand(context.Background(), "computers", flags{}); code != Success {
		t.Fatalf("exit %d", code)
	}
	out, _ := outputs(op)
	for _, want := range []string{"this computer: server", "fd7a::2, 100.64.0.2 (port", "let in: desk",
		"123 456  laptop, from fd7a::1", "motita gateway allow <code>"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestComputersWithNoneAskingOrUnreachable(t *testing.T) {
	old := computerAddrs
	t.Cleanup(func() { computerAddrs = old })
	computerAddrs = func() []string { return nil }
	op := (&fakePeers{view: `{"self":{"name":"server","reachable":true}}`}).serve(t)
	op.runGatewayCommand(context.Background(), "computers", flags{})
	if out, _ := outputs(op); !strings.Contains(out, "no computer is asking") || strings.Contains(out, "its addresses") {
		t.Errorf("none asking:\n%s", out)
	}
	op = (&fakePeers{view: `{"self":{"name":"server","reachable":false}}`}).serve(t)
	op.runGatewayCommand(context.Background(), "computers", flags{})
	if out, _ := outputs(op); !strings.Contains(out, "motita gateway setup") {
		t.Errorf("unreachable:\n%s", out)
	}
}

func TestAllowAndDenyAnswerByCode(t *testing.T) {
	f := &fakePeers{view: asking}
	op := f.serve(t)
	if code := op.runGatewayCommand(context.Background(), "allow", flags{gatewayCode: "123456"}); code != Success {
		t.Fatalf("allow exit %d", code)
	}
	if code := op.runGatewayCommand(context.Background(), "DENY", flags{gatewayCode: "123 456"}); code != Success {
		t.Fatalf("deny exit %d", code)
	}
	out, _ := outputs(op)
	if strings.Join(f.decided, ",") != "r1+,r1-" || !strings.Contains(out, "laptop is let in") ||
		!strings.Contains(out, "laptop was turned away") {
		t.Fatalf("%v\n%s", f.decided, out)
	}
}

func TestComputerCommandFailures(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		f      *fakePeers
		action string
		code   string
		want   string
	}{
		"unknown code": {&fakePeers{view: asking}, "allow", "999999", "no computer asks"},
		"empty code":   {&fakePeers{view: asking}, "allow", "", "no computer asks"},
		"list fails":   {&fakePeers{status: http.StatusInternalServerError}, "computers", "", "broken"},
		"read fails":   {&fakePeers{status: http.StatusInternalServerError}, "deny", "123456", "broken"},
		"answer fails": {&fakePeers{view: asking, decision: http.StatusNotFound}, "allow", "123456", "gone"},
		"bad answer":   {&fakePeers{view: "{"}, "computers", "", "could not be read"},
	}
	for name, c := range cases {
		op := c.f.serve(t)
		if code := op.runGatewayCommand(ctx, c.action, flags{gatewayCode: c.code}); code != ConfigError {
			t.Errorf("%s: exit %d", name, code)
		}
		if _, errOut := outputs(op); !strings.Contains(errOut, c.want) {
			t.Errorf("%s: %s", name, errOut)
		}
	}
}

func TestComputerCommandsNeedARunningGateway(t *testing.T) {
	for _, found := range []func(context.Context, string) (gateway.Found, bool, error){
		func(context.Context, string) (gateway.Found, bool, error) { return gateway.Found{}, false, nil },
		func(context.Context, string) (gateway.Found, bool, error) {
			return gateway.Found{}, false, errors.New("corrupt service file")
		},
	} {
		for _, action := range []string{"computers", "allow"} {
			op, _, errOut := netOptions("")
			op.DiscoverGateway = found
			if code := op.runGatewayCommand(context.Background(), action, flags{gatewayCode: "1"}); code != ConfigError ||
				errOut.Len() == 0 {
				t.Errorf("%s: exit %d %s", action, code, errOut)
			}
		}
	}
}

func TestTheComputerCommandsAreParsed(t *testing.T) {
	cases := map[string]struct {
		args         []string
		action, code string
		rest         string
	}{
		"computers": {[]string{"gateway", "computers"}, "computers", "", ""},
		"split":     {[]string{"gateway", "allow", "123", "456"}, "allow", "123456", ""},
		"joined":    {[]string{"gateway", "deny", "123456", "-config", "c.yaml"}, "deny", "123456", "c.yaml"},
		"half":      {[]string{"gateway", "allow", "123", "-config", "c.yaml"}, "allow", "123", "c.yaml"},
		"not half":  {[]string{"gateway", "allow", "12", "456"}, "", "", ""},
	}
	for name, c := range cases {
		fl, err := parse(c.args)
		if c.action == "" {
			if err == nil {
				t.Errorf("%s: a stray argument was accepted", name)
			}
			continue
		}
		if err != nil || fl.gatewayAction != c.action || fl.gatewayCode != c.code || fl.configPath != c.rest {
			t.Errorf("%s: %+v %v", name, fl, err)
		}
	}
	if _, err := parse([]string{"gateway", "allow"}); err == nil || !strings.Contains(err.Error(), "gateway computers") {
		t.Errorf("allow without a code: %v", err)
	}
}
