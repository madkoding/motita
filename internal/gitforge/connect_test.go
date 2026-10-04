package gitforge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/oauth"
)

func instant(time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	c <- time.Now()
	return c
}

// oauthHost is one fake server that is the git host and its OAuth server.
func oauthHost(t *testing.T, kind Kind, polls ...string) (Service, Store, *httptest.Server) {
	t.Helper()
	n := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"AB-12","verification_uri":"https://h.test/verify","expires_in":600,"interval":1}`))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(b))
		if form.Get("grant_type") == "authorization_code" {
			_, _ = w.Write([]byte(`{"access_token":"from-code"}`))
			return
		}
		body := polls[len(polls)-1]
		if n < len(polls) {
			body = polls[n]
		}
		n++
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer good", "Bearer from-code", "token good", "token from-code", "Basic dXNlcjpwdw==":
			_, _ = w.Write([]byte(`{"login":"octo","username":"octo"}`))
		case "Bearer broken":
			http.Error(w, "boom", 500)
		default:
			http.Error(w, `{"message":"Bad credentials"}`, 401)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	svc := Service{ID: string(kind), Name: "Host", Kind: kind, Host: "h.test", APIBase: srv.URL, EnvPrefix: "MOTITA_X",
		Client:    oauth.Client{ID: "cid", Secret: "sec"},
		Endpoints: oauth.Endpoints{DeviceURL: srv.URL + "/device", TokenURL: srv.URL + "/token", AuthorizeURL: srv.URL + "/authorize"}}
	return svc, Store{Dir: t.TempDir(), Env: noEnv}, srv
}

func TestDeviceFlowEndToEnd(t *testing.T) {
	sleepFor = instant
	defer func() { sleepFor = time.After }()
	svc, s, _ := oauthHost(t, KindGitHub, `{"error":"authorization_pending"}`, `{"access_token":"good","refresh_token":"r"}`)
	f, err := s.StartDevice(context.Background(), svc)
	if err != nil || f.Code != "AB-12" || f.URL != "https://h.test/verify" || f.ExpiresIn != 600 {
		t.Fatalf("%+v %v", f, err)
	}
	who, err := f.Wait(context.Background())
	if err != nil || who != "octo" {
		t.Fatalf("%q %v", who, err)
	}
	cred, _ := s.Credential(context.Background(), svc)
	if cred.AccessToken != "good" || cred.RefreshToken != "r" || cred.Username != "octo" || cred.ClientID != "cid" {
		t.Errorf("cred = %+v", cred)
	}
}

func TestDeviceFlowFailures(t *testing.T) {
	sleepFor = instant
	defer func() { sleepFor = time.After }()
	// No device login configured.
	plain := Defaults(noEnv)[1]
	s := Store{Dir: t.TempDir(), Env: noEnv}
	if _, err := s.StartDevice(context.Background(), plain); err == nil || !strings.Contains(err.Error(), "MOTITA_GITLAB_CLIENT_ID") {
		t.Errorf("err = %v", err)
	}
	// The host refuses to start one.
	svc, s, srv := oauthHost(t, KindGitHub, `{"error":"access_denied"}`)
	dead := svc
	dead.Endpoints.DeviceURL = srv.URL + "/missing"
	if _, err := s.StartDevice(context.Background(), dead); err == nil {
		t.Error("a refused start must be an error")
	}
	// The user says no.
	f, _ := s.StartDevice(context.Background(), svc)
	if _, err := f.Wait(context.Background()); err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Errorf("err = %v", err)
	}
	// A token the host then rejects is not stored.
	svc, s, _ = oauthHost(t, KindGitHub, `{"access_token":"bad"}`)
	f, _ = s.StartDevice(context.Background(), svc)
	if _, err := f.Wait(context.Background()); err == nil || !strings.Contains(err.Error(), "did not accept") {
		t.Errorf("err = %v", err)
	}
	if s.Connected(svc) {
		t.Error("a rejected token must not be stored")
	}
}

func TestConnectToken(t *testing.T) {
	svc, s, _ := oauthHost(t, KindGitHub, "")
	who, err := s.ConnectToken(context.Background(), svc, "  good ", "")
	if err != nil || who != "octo" || !s.Connected(svc) {
		t.Fatalf("%q %v", who, err)
	}
	if _, err := s.ConnectToken(context.Background(), svc, "", ""); err == nil {
		t.Error("an empty token must be refused")
	}
	if _, err := s.ConnectToken(context.Background(), svc, "nope", ""); err == nil || !strings.Contains(err.Error(), "did not accept") {
		t.Errorf("err = %v", err)
	}
	if _, err := s.ConnectToken(context.Background(), svc, "broken", ""); err == nil || !strings.Contains(err.Error(), "could not confirm") {
		t.Errorf("err = %v", err)
	}
	// Failing to store is reported.
	bad := Store{Dir: "", Env: noEnv}
	if _, err := bad.ConnectToken(context.Background(), svc, "good", ""); err == nil {
		t.Error("a store with no directory must fail")
	}
	// Bitbucket's app password needs the account, and is sent with Basic.
	bb, s, _ := oauthHost(t, KindBitbucket, "")
	if _, err := s.ConnectToken(context.Background(), bb, "pw", ""); err == nil || !strings.Contains(err.Error(), "account name") {
		t.Errorf("err = %v", err)
	}
	if who, err = s.ConnectToken(context.Background(), bb, "pw", "user"); err != nil || who != "user" {
		t.Fatalf("%q %v", who, err)
	}
	cred, _ := s.Credential(context.Background(), bb)
	if !cred.Basic || cred.Username != "user" {
		t.Errorf("cred = %+v", cred)
	}
}

func TestCodeFlowByPaste(t *testing.T) {
	svc, s, _ := oauthHost(t, KindGitea, "")
	svc.Endpoints.DeviceURL = ""
	f, err := s.StartCode(svc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.URL, "code_challenge=") || !strings.HasPrefix(f.RedirectURI(), "http://localhost:") {
		t.Errorf("url=%s redirect=%s", f.URL, f.RedirectURI())
	}
	paste := make(chan string, 1)
	paste <- "just-a-code"
	who, err := f.Wait(context.Background(), paste)
	if err != nil || who != "octo" {
		t.Fatalf("%q %v", who, err)
	}
	if cred, _ := s.Credential(context.Background(), svc); cred.AccessToken != "from-code" {
		t.Errorf("cred = %+v", cred)
	}
}

func TestCodeFlowByBrowserAndBusyPort(t *testing.T) {
	svc, s, _ := oauthHost(t, KindGitea, "")
	svc.Endpoints.DeviceURL = ""
	// Two flows at once: the second finds the fixed port taken and takes another.
	first, err := s.StartCode(svc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.StartCode(svc)
	if err != nil {
		t.Fatal(err)
	}
	first.Cancel()
	second.Cancel()
	third, _ := s.StartCode(svc)
	u, _ := url.Parse(third.URL)
	state := u.Query().Get("state")
	done := make(chan string, 1)
	go func() {
		who, _ := third.Wait(context.Background(), nil)
		done <- who
	}()
	// The browser comes back to the loopback with the state this login issued.
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get(third.RedirectURI() + "?code=abc&state=" + state)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case who := <-done:
		if who != "octo" {
			t.Errorf("who = %q", who)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the flow did not finish")
	}
}

func TestCodeFlowFailures(t *testing.T) {
	plain := Defaults(noEnv)[3]
	s := Store{Dir: t.TempDir(), Env: noEnv}
	if _, err := s.StartCode(plain); err == nil || !strings.Contains(err.Error(), "MOTITA_CODEBERG_CLIENT_SECRET") {
		t.Errorf("err = %v", err)
	}
	svc, s, srv := oauthHost(t, KindGitea, "")
	svc.Endpoints.DeviceURL = ""
	// A paste with the wrong state, and a cancelled wait.
	f, _ := s.StartCode(svc)
	paste := make(chan string, 1)
	paste <- "code=x&state=other"
	if _, err := f.Wait(context.Background(), paste); err == nil {
		t.Error("a code for another login must be refused")
	}
	f, _ = s.StartCode(svc)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Wait(ctx, nil); err == nil {
		t.Error("a cancelled wait must fail")
	}
	// The host refuses the exchange.
	bad := svc
	bad.Endpoints.TokenURL = srv.URL + "/missing"
	f, _ = s.StartCode(bad)
	paste = make(chan string, 1)
	paste <- "a-code"
	if _, err := f.Wait(context.Background(), paste); err == nil || !strings.Contains(err.Error(), "could not complete") {
		t.Errorf("err = %v", err)
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestStartCodeSetupFailures(t *testing.T) {
	svc, s, _ := oauthHost(t, KindGitea, "")
	svc.Endpoints.DeviceURL = ""
	old := oauth.RandReader
	oauth.RandReader = failReader{}
	_, err := s.StartCode(svc)
	oauth.RandReader = old
	if err == nil {
		t.Error("a failed random source must be an error")
	}
	oldLB := startLoopback
	startLoopback = func(string, string, string) (*oauth.Loopback, error) { return nil, errors.New("no ports") }
	defer func() { startLoopback = oldLB }()
	if _, err := s.StartCode(svc); err == nil || !strings.Contains(err.Error(), "no ports") {
		t.Errorf("err = %v", err)
	}
}
