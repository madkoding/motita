package gateway

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/madkoding/motita/internal/i18n"
)

// pushTransport answers the push service's side with a fixed status and counts the requests.
type pushTransport struct {
	mu     sync.Mutex
	status int
	err    error
	hits   []string
}

func (p *pushTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hits = append(p.hits, r.URL.String())
	if p.err != nil {
		return nil, p.err
	}
	return &http.Response{StatusCode: p.status, Status: http.StatusText(p.status), Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (p *pushTransport) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.hits)
}

func newPushServer(t *testing.T) (*Server, *pushTransport, string) {
	t.Helper()
	dir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.PushDir = dir })
	tr := &pushTransport{status: http.StatusCreated}
	srv.push.Client = &http.Client{Transport: tr}
	return srv, tr, dir
}

// subscriptionJSON is a browser's PushSubscription.toJSON(), with a key pair that is valid.
func subscriptionJSON(t *testing.T, endpoint, lang string) string {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	b, _ := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(auth),
		},
		"lang": lang,
	})
	return string(b)
}

func pushCall(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return sendNetwork(t, srv, method, path, body)
}

func pushState(t *testing.T, srv *Server, endpoint string) pushView {
	t.Helper()
	w := get(t, srv, "/v1/push?endpoint="+url.QueryEscape(endpoint), testToken)
	var v pushView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

const testEndpoint = "https://fcm.googleapis.com/fcm/send/phone"

func TestAGatewayWithoutAPushDirSaysSo(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if v := pushState(t, srv, testEndpoint); v.Available || v.PublicKey != "" {
		t.Fatalf("GET = %+v", v)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/push/subscriptions"},
		{http.MethodDelete, "/v1/push/subscriptions"},
		{http.MethodPost, "/v1/push/test"},
	} {
		if w := pushCall(t, srv, c.method, c.path, `{}`); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", c.method, c.path, w.Code)
		}
	}
	// Nothing to send to: a step of a watch is not an error.
	c := newConversation("s1", &fakeService{})
	srv.pushPR(c, nil, &PRWatchView{Status: prPassed})
}

func TestAPushDirThatCannotBeOpenedLeavesPushOff(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.PushDir = file })
	if pushState(t, srv, "").Available {
		t.Fatal("push is on with a key that could not be saved")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "subscriptions.json"), []byte("{"), 0o600)
	srv = newTestServer(t, &fakeService{}, func(o *Options) { o.PushDir = dir })
	if pushState(t, srv, "").Available {
		t.Fatal("push is on with subscriptions that could not be read")
	}
}

// TestABrowserSubscribesTestsAndUnsubscribes is the settings' whole path: the key, the
// subscription, the test notification and the way out.
func TestABrowserSubscribesTestsAndUnsubscribes(t *testing.T) {
	srv, tr, _ := newPushServer(t)
	v := pushState(t, srv, testEndpoint)
	if !v.Available || v.PublicKey != srv.push.Keys.Public() || v.Subscribed {
		t.Fatalf("GET = %+v", v)
	}
	for _, body := range []string{`nope`, subscriptionJSON(t, "https://10.0.0.1/x", "es")} {
		if w := pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, w.Code)
		}
	}
	if w := pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "es")); w.Code != http.StatusNoContent {
		t.Fatalf("subscribe = %d %s", w.Code, w.Body.String())
	}
	if !pushState(t, srv, testEndpoint).Subscribed {
		t.Fatal("the gateway does not know the browser it just subscribed")
	}

	body := `{"endpoint":"` + testEndpoint + `"}`
	if w := pushCall(t, srv, http.MethodPost, "/v1/push/test", body); w.Code != http.StatusNoContent || tr.count() != 1 {
		t.Fatalf("test = %d %s, %d requests", w.Code, w.Body.String(), tr.count())
	}
	for _, b := range []string{`nope`, `{"endpoint":"https://fcm.googleapis.com/other"}`} {
		if w := pushCall(t, srv, http.MethodPost, "/v1/push/test", b); w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Errorf("test %s = %d", b, w.Code)
		}
	}
	tr.status = http.StatusInternalServerError
	if w := pushCall(t, srv, http.MethodPost, "/v1/push/test", body); w.Code != http.StatusBadGateway {
		t.Errorf("refused test = %d, want 502", w.Code)
	}
	if !pushState(t, srv, testEndpoint).Subscribed {
		t.Fatal("a refused message dropped the subscription")
	}

	if w := pushCall(t, srv, http.MethodDelete, "/v1/push/subscriptions", `nope`); w.Code != http.StatusBadRequest {
		t.Errorf("bad unsubscribe = %d", w.Code)
	}
	if w := pushCall(t, srv, http.MethodDelete, "/v1/push/subscriptions", body); w.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe = %d", w.Code)
	}
	if pushState(t, srv, testEndpoint).Subscribed {
		t.Fatal("still subscribed after unsubscribing")
	}
}

func TestATestToAGoneBrowserForgetsIt(t *testing.T) {
	srv, tr, _ := newPushServer(t)
	_ = pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "en"))
	tr.status = http.StatusGone
	if w := pushCall(t, srv, http.MethodPost, "/v1/push/test", `{"endpoint":"`+testEndpoint+`"}`); w.Code != http.StatusBadGateway {
		t.Fatalf("gone test = %d", w.Code)
	}
	if pushState(t, srv, testEndpoint).Subscribed {
		t.Fatal("a browser the push service dropped is still subscribed")
	}
}

func TestPushSubscriptionsThatCannotBeSavedAreReported(t *testing.T) {
	srv, _, dir := newPushServer(t)
	_ = pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "en"))
	// The folder becomes a file: the next save has nowhere to go.
	_ = os.RemoveAll(dir)
	_ = os.WriteFile(dir, nil, 0o600)
	t.Cleanup(func() { _ = os.Remove(dir) })
	if w := pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions",
		subscriptionJSON(t, testEndpoint+"2", "en")); w.Code != http.StatusInternalServerError {
		t.Errorf("subscribe = %d, want 500", w.Code)
	}
	if w := pushCall(t, srv, http.MethodDelete, "/v1/push/subscriptions",
		`{"endpoint":"`+testEndpoint+`"}`); w.Code != http.StatusInternalServerError {
		t.Errorf("unsubscribe = %d, want 500", w.Code)
	}
}

func TestPrPushNoticeMatchesTheToast(t *testing.T) {
	w := func(status string, attempts int) *PRWatchView {
		return &PRWatchView{Status: status, Attempts: attempts}
	}
	cases := []struct {
		prev, cur *PRWatchView
		want      string
	}{
		{nil, nil, ""},
		{nil, w(prFollowing, 0), ""},
		{nil, w(prPassed, 0), prPassed},
		{w(prFollowing, 0), w(prPassed, 0), prPassed},
		{w(prPassed, 0), w(prPassed, 0), ""},
		{w(prFollowing, 0), w(prFixing, 1), prFixing},
		{w(prFixing, 1), w(prFixing, 1), ""},
		{w(prFixing, 1), w(prFixing, 2), prFixing},
		{w(prFixing, 1), w(prReviewing, 1), prReviewing},
		{w(prFixing, 2), w(prGaveUp, 2), prGaveUp},
	}
	for _, c := range cases {
		if got := prPushNotice(c.prev, c.cur); got != c.want {
			t.Errorf("%+v -> %+v = %q, want %q", c.prev, c.cur, got, c.want)
		}
	}
}

func TestPrMessageSaysWhatTheToastSays(t *testing.T) {
	v := &PRWatchView{Number: 7, Attempts: 2, Max: 5}
	for _, n := range []string{prPassed, prFixing, prReviewing, prResolving, prGaveUp, prNoCI, prBaseRed, prClosed, prMerged} {
		for _, lang := range []i18n.Lang{i18n.EN, i18n.ES} {
			m := prMessage(lang, n, v, "Login page", "s 1")
			if !strings.HasSuffix(m.Title, " — Login page") || m.Body == "" || m.Tag != "motita-pr-s 1" ||
				m.URL != "/?session=s+1" {
				t.Errorf("%s/%s = %+v", n, lang, m)
			}
			if strings.Contains(m.Body, "%") {
				t.Errorf("%s/%s left a verb: %q", n, lang, m.Body)
			}
		}
	}
	if m := prMessage(i18n.ES, prPassed, v, "", "s"); m.Title != "CI aprobado" ||
		m.Body != "El pull request #7 está en verde: ya puedes ir a mergearlo." {
		t.Errorf("es = %+v", m)
	}
	if m := prMessage(i18n.EN, prFixing, &PRWatchView{Attempts: 1, Max: 3}, "", "s"); m.Body != "Motita is fixing it — attempt 1 of 3." {
		t.Errorf("en = %+v", m)
	}
}

// TestAStepOfTheWatchReachesEverySubscribedBrowser: the watch moving is what sends, once per step,
// and a push service that refuses is logged, not fatal.
func TestAStepOfTheWatchReachesEverySubscribedBrowser(t *testing.T) {
	srv, tr, _ := newPushServer(t)
	_ = pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "es"))
	_ = pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint+"2", "en"))
	c := newConversation("s1", &fakeService{})
	c.setTitle(placeholderTitle)
	w := &prWatchState{status: prFollowing}
	srv.publishPR(c, w, 3)
	srv.pushWG.Wait()
	if tr.count() != 0 {
		t.Fatalf("following sent %d", tr.count())
	}
	w.status = prPassed
	srv.publishPR(c, w, 3)
	srv.publishPR(c, w, 3) // the same step again is not news
	srv.pushWG.Wait()
	if tr.count() != 2 {
		t.Fatalf("passed sent %d, want one per browser", tr.count())
	}
	tr.err = errors.New("offline")
	c.setTitle("Login page")
	w.status = prClosed
	srv.publishPR(c, w, 3)
	srv.pushWG.Wait()
	if tr.count() != 4 {
		t.Fatalf("closed sent %d", tr.count())
	}
}
