package onboard

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/oauth"
)

func failingRand(t *testing.T) {
	t.Helper()
	old := oauth.RandReader
	oauth.RandReader = strings.NewReader("")
	t.Cleanup(func() { oauth.RandReader = old })
}

func noLoopback(t *testing.T) {
	t.Helper()
	old := startLoopback
	startLoopback = func(string, string, string) (*oauth.Loopback, error) { return nil, errors.New("no port") }
	t.Cleanup(func() { startLoopback = old })
}

func realLoopbackAnyPort(t *testing.T) {
	t.Helper()
	old := startLoopback
	startLoopback = func(_, host, path string) (*oauth.Loopback, error) {
		return oauth.StartLoopback("127.0.0.1:0", host, path)
	}
	t.Cleanup(func() { startLoopback = old })
}

func tokenRT(status int, body string) http.RoundTripper {
	return &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
		"/oauth/token": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: jsonBody(body), Header: make(http.Header)}, nil
		},
		"/token": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: jsonBody(body), Header: make(http.Header)}, nil
		},
		"/api/v1/oauth2/device/code": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: jsonBody(body), Header: make(http.Header)}, nil
		},
	}}
}

func direct(id, input string) error {
	_, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader(input)), Provider{ID: id})
	return err
}

func TestDirectAuthFailures(t *testing.T) {
	useAuthDir(t)
	instantSleep(t)
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", "cid")
	t.Run("no entropy", func(t *testing.T) {
		failingRand(t)
		for _, id := range []string{"codex", "qwen"} {
			if err := direct(id, ""); err == nil {
				t.Errorf("%s must fail without randomness", id)
			}
		}
		if err := direct("gemini", "proj\n"); err == nil {
			t.Error("gemini must fail without randomness")
		}
	})
	t.Run("no code", func(t *testing.T) {
		noLoopback(t)
		if err := direct("codex", ""); err == nil {
			t.Error("no browser and no paste must be an error")
		}
	})
	t.Run("refused exchange", func(t *testing.T) {
		withTransport(t, tokenRT(400, `{"error":"invalid_grant"}`))
		realLoopbackAnyPort(t)
		if err := direct("codex", "the-code\n"); err == nil || !strings.Contains(err.Error(), "ChatGPT") {
			t.Errorf("codex: %v", err)
		}
		if err := direct("gemini", "proj\nthe-code\n"); err == nil || !strings.Contains(err.Error(), "Google") {
			t.Errorf("gemini: %v", err)
		}
		if err := direct("qwen", ""); err == nil || !strings.Contains(err.Error(), "Qwen") {
			t.Errorf("qwen device: %v", err)
		}
	})
	t.Run("gemini without a loopback", func(t *testing.T) {
		noLoopback(t)
		if err := direct("gemini", "proj\n"); err == nil {
			t.Error("the Google login needs the loopback")
		}
	})
	t.Run("gemini code not given", func(t *testing.T) {
		realLoopbackAnyPort(t)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		pr, _ := io.Pipe()
		in := bufio.NewReader(io.MultiReader(strings.NewReader("proj\n"), pr))
		if _, err := runDirectAuth(ctx, io.Discard, in, Provider{ID: "gemini"}); err == nil {
			t.Error("a login that never completes must end with the context")
		}
	})
	t.Run("prompt cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		pr, _ := io.Pipe()
		if _, err := runDirectAuth(ctx, io.Discard, bufio.NewReader(pr), Provider{ID: "gemini"}); err == nil {
			t.Error("a cancelled prompt must be an error")
		}
	})
	t.Run("qwen denied", func(t *testing.T) {
		withTransport(t, &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
			"/api/v1/oauth2/device/code": func(*http.Request) (*http.Response, error) {
				return resp200(`{"device_code":"d","user_code":"U","verification_uri":"https://x"}`)
			},
			"/api/v1/oauth2/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Body: jsonBody(`{"error":"access_denied"}`), Header: make(http.Header)}, nil
			},
		}})
		if err := direct("qwen", ""); err == nil || !strings.Contains(err.Error(), "qwen") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestGeminiOwnClientLogin(t *testing.T) {
	dir := useAuthDir(t)
	realLoopbackAnyPort(t)
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", "cid")
	t.Setenv("MOTITA_GEMINI_CLIENT_SECRET", "sec")
	withTransport(t, tokenRT(200, `{"access_token":"ya29","refresh_token":"1//r","expires_in":3600}`))
	if err := direct("gemini", "my-project\nthe-code\n"); err != nil {
		t.Fatal(err)
	}
	c, _ := oauth.LoadCredential(dir, "gemini")
	if c.ProjectID != "my-project" || c.ClientID != "cid" || c.RefreshToken != "1//r" {
		t.Errorf("stored %+v", c)
	}
}

func TestGeminiADCFailures(t *testing.T) {
	useAuthDir(t)
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", "")
	adc := filepath.Join(t.TempDir(), "adc.json")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	_ = os.WriteFile(adc, []byte(`{"type":"authorized_user","client_id":"c","refresh_token":"r"}`), 0o600)
	if err := direct("gemini", "\n"); err == nil || !strings.Contains(err.Error(), "project") {
		t.Errorf("no project: %v", err)
	}
	withTransport(t, tokenRT(400, `{"error":"invalid_grant"}`))
	if err := direct("gemini", "proj\n"); err == nil || !strings.Contains(err.Error(), "renewed") {
		t.Errorf("refused renewal: %v", err)
	}
}

func TestALoginThatCannotBeSavedIsReported(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	old := authDir
	authDir = func() string { return filepath.Join(file, "auth") }
	t.Cleanup(func() { authDir = old })
	instantSleep(t)
	withTransport(t, &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
		"/login/device/code":         func(*http.Request) (*http.Response, error) { return resp200(`{"device_code":"d","interval":1}`) },
		"/login/oauth/access_token":  func(*http.Request) (*http.Response, error) { return resp200(`{"access_token":"gho"}`) },
		"/copilot_internal/v2/token": func(*http.Request) (*http.Response, error) { return resp200(`{"token":"t"}`) },
	}})
	if err := direct("copilot", ""); err == nil || !strings.Contains(err.Error(), "could not be saved") {
		t.Errorf("err = %v", err)
	}
}

func TestWaitForCodeEnterCanBeCancelled(t *testing.T) {
	lb, err := oauth.StartLoopback("127.0.0.1:0", "127.0.0.1", "/cb")
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()
	ctx, cancel := context.WithCancel(context.Background())
	pr, _ := io.Pipe()
	go func() {
		r, err := http.Get(lb.RedirectURI + "?code=c&state=s")
		if err == nil {
			r.Body.Close()
		}
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	if code, err := waitForCode(ctx, io.Discard, bufio.NewReader(pr), lb, "s"); err != nil || code != "c" {
		t.Errorf("code=%q err=%v", code, err)
	}
	var out strings.Builder
	printPasteHint(&out, nil)
	if !strings.Contains(out.String(), "Waiting for the browser") {
		t.Error(out.String())
	}
}
