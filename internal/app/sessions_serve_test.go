package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAServedGatewayCanOpenAnotherSession is the wiring test for the factory.
//
// Without it the endpoints exist and nothing calls them: startGateway has to hand the gateway a
// way to BUILD a conversation, or a remote front end gets a 501 on a gateway that can plainly
// serve work. The two halves live in different packages, so the only place that can prove they fit
// together is here.
//
// It goes over HTTP against a real -serve process, with the token read from disk, because that is
// the path a remote client takes.
func TestAServedGatewayCanOpenAnotherSession(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)
	go func() { _ = Run(opts) }()
	addr := waitForAddress(t, logPath)

	// The token is read back from the file the gateway writes, which is what a remote client has
	// to do - it keeps the value out of this test and out of any failure message.
	token := readToken(t, filepath.Join(filepath.Dir(cfgPath), "gateway.token"))

	client := &http.Client{Timeout: 5 * time.Second}

	// Listing works and reports the default conversation: this is the endpoint a remote front end
	// uses to find where it can talk.
	var listed struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := getJSONInto(t, client, "http://"+addr+"/v1/sessions", token, &listed); err != nil {
		t.Fatalf("listing sessions: %v", err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != "default" {
		t.Fatalf("a fresh gateway reports %+v, it must report only the default session", listed.Sessions)
	}

	// And it can open one, which is the factory being wired.
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/sessions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("opening a session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("opening a session answered %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("the answer is not a session: %v", err)
	}
	if created.ID == "" || created.ID == "default" {
		t.Fatalf("the opened session has id %q, it must have one of its own", created.ID)
	}

	// The new conversation is usable: a report comes back over ITS path.
	var report struct {
		Text string `json:"text"`
	}
	if err := getJSONInto(t, client, "http://"+addr+"/v1/sessions/"+created.ID+"/report", token, &report); err != nil {
		t.Fatalf("the opened session is not usable: %v", err)
	}

	cancel()
}

// getJSONInto performs an authenticated GET and decodes the answer.
func getJSONInto(t *testing.T, c *http.Client, url, token string, dst any) error {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s answered %d: %s", url, resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// readToken reads the bearer token the gateway created, trimmed of the newline it is written with.
func readToken(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the gateway never wrote its token to %s", path)
	return ""
}
