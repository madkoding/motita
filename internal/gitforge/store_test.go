package gitforge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/oauth"
)

func ghService() Service { return Defaults(noEnv)[0] }

func TestStoreSaveConnectRemove(t *testing.T) {
	s := Store{Dir: t.TempDir(), Env: noEnv}
	gh := ghService()
	if s.Connected(gh) {
		t.Fatal("nothing is stored yet")
	}
	if err := s.Save(gh, oauth.Token{AccessToken: "tok"}, "octo", false); err != nil {
		t.Fatal(err)
	}
	if !s.Connected(gh) {
		t.Fatal("the login must be stored")
	}
	cred, err := s.Credential(context.Background(), gh)
	if err != nil || cred.AccessToken != "tok" || cred.Username != "octo" || cred.BaseURL != "github.com" {
		t.Fatalf("cred = %+v %v", cred, err)
	}
	// A 0600 file in a directory the user only can enter: it is a secret.
	info, _ := os.Stat(oauth.CredentialPath(s.Dir, "git-github"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	if err := s.Remove(gh); err != nil || s.Connected(gh) {
		t.Fatalf("remove: %v", err)
	}
	if err := s.Remove(gh); err != nil {
		t.Errorf("removing twice must not fail: %v", err)
	}
	if _, err := s.Credential(context.Background(), gh); !IsNotConnected(err) {
		t.Errorf("err = %v", err)
	}
}

func TestStoreCredentialRefreshes(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "old-r" || r.Form.Get("client_id") != "cid" {
			t.Errorf("form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","refresh_token":"new-r","expires_in":7200}`))
	}))
	defer srv.Close()
	gl := Defaults(noEnv)[1]
	gl.Endpoints.TokenURL = srv.URL
	gl.Client = oauth.Client{ID: "cid"}
	now := time.Now()
	s := Store{Dir: t.TempDir(), Env: noEnv, Now: func() time.Time { return now }}
	exp := now.Add(-time.Minute)
	if err := s.Save(gl, oauth.Token{AccessToken: "stale", RefreshToken: "old-r", ExpiresAt: exp}, "me", false); err != nil {
		t.Fatal(err)
	}
	cred, err := s.Credential(context.Background(), gl)
	if err != nil || cred.AccessToken != "fresh" || cred.RefreshToken != "new-r" || calls != 1 {
		t.Fatalf("cred=%+v err=%v calls=%d", cred, err, calls)
	}
	// The renewed token was persisted: the second read does not renew again.
	if cred, _ = s.Credential(context.Background(), gl); cred.AccessToken != "fresh" || calls != 1 {
		t.Errorf("second read: %+v calls=%d", cred, calls)
	}
}

func TestStoreCredentialKeepsAnUnrenewableLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
	}))
	defer srv.Close()
	gl := Defaults(noEnv)[1]
	gl.Endpoints.TokenURL = srv.URL
	s := Store{Dir: t.TempDir(), Env: noEnv}
	_ = s.Save(gl, oauth.Token{AccessToken: "stale", RefreshToken: "r", ExpiresAt: time.Now().Add(-time.Hour)}, "me", false)
	cred, err := s.Credential(context.Background(), gl)
	if err != nil || cred.AccessToken != "stale" {
		t.Fatalf("cred=%+v err=%v", cred, err)
	}
	// A token that expires but cannot be renewed (a pasted one) is left alone.
	_ = s.Save(gl, oauth.Token{AccessToken: "pasted", ExpiresAt: time.Now().Add(-time.Hour)}, "me", false)
	if cred, _ = s.Credential(context.Background(), gl); cred.AccessToken != "pasted" {
		t.Errorf("cred = %+v", cred)
	}
	// A token that is fine is returned without a request.
	_ = s.Save(gl, oauth.Token{AccessToken: "fine", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}, "me", false)
	if cred, _ = s.Credential(context.Background(), gl); cred.AccessToken != "fine" {
		t.Errorf("cred = %+v", cred)
	}
}

func TestStoreNowDefaults(t *testing.T) {
	if d := time.Since(Store{}.now()); d < 0 || d > time.Minute {
		t.Errorf("default clock off by %v", d)
	}
}

func TestStoreCredentialSurvivesAFailedWriteBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"fresh","expires_in":60}`))
	}))
	defer srv.Close()
	gl := Defaults(noEnv)[1]
	gl.Endpoints.TokenURL = srv.URL
	dir := t.TempDir()
	s := Store{Dir: dir, Env: noEnv}
	_ = s.Save(gl, oauth.Token{AccessToken: "stale", RefreshToken: "r", ExpiresAt: time.Now().Add(-time.Hour)}, "me", false)
	// Make the directory unwritable: the renewed token cannot be saved, and the
	// caller must still get it.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot restrict the directory")
	}
	defer os.Chmod(dir, 0o700)
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	cred, err := s.Credential(context.Background(), gl)
	if err != nil || cred.AccessToken != "fresh" {
		t.Fatalf("cred=%+v err=%v", cred, err)
	}
}

func TestStoreServicesAndAccounts(t *testing.T) {
	s := Store{Dir: t.TempDir(), Env: noEnv}
	corp, _ := Custom(KindGitLab, "git.corp.io", noEnv)
	_ = s.Save(corp, oauth.Token{AccessToken: "t"}, "corp-user", false)
	_ = s.Save(ghService(), oauth.Token{AccessToken: "g"}, "octo", false)
	// Files that are not a git login, or name nothing that exists, are ignored.
	for _, name := range []string{"git-bogus@x.io.json", "git-gitlab@bad host.json", "copilot.json"} {
		_ = os.WriteFile(filepath.Join(s.Dir, name), []byte("{}"), 0o600)
	}
	// A custom file for a default host must not duplicate it.
	_ = os.WriteFile(filepath.Join(s.Dir, "git-gitlab@gitlab.com.json"), []byte("{}"), 0o600)
	// And a custom service twice is listed once.
	all := s.All()
	ids := map[string]int{}
	for _, svc := range all {
		ids[svc.ID]++
	}
	if len(all) != 4 || ids["gitlab@git.corp.io"] != 1 || ids["gitlab"] != 1 {
		t.Fatalf("all = %v", ids)
	}
	if svc, ok := s.ByID("gitlab@git.corp.io"); !ok || svc.Host != "git.corp.io" {
		t.Errorf("ByID = %+v %v", svc, ok)
	}
	if _, ok := s.ByID("nope"); ok {
		t.Error("an unknown id must not be found")
	}
	if svc, ok := s.ForHost("GITHUB.com"); !ok || svc.ID != "github" {
		t.Errorf("ForHost = %+v %v", svc, ok)
	}
	if _, ok := s.ForHost("example.org"); ok {
		t.Error("an unknown host must not be found")
	}

	accounts := map[string]Account{}
	for _, a := range s.Accounts() {
		accounts[a.Service.ID] = a
	}
	if a := accounts["github"]; !a.Connected || a.Username != "octo" {
		t.Errorf("github = %+v", a)
	}
	if a := accounts["gitlab@git.corp.io"]; !a.Connected || a.Username != "corp-user" {
		t.Errorf("corp = %+v", a)
	}
	if accounts["bitbucket"].Connected {
		t.Error("bitbucket was never connected")
	}
}

func TestStoreForRemote(t *testing.T) {
	s := Store{Dir: t.TempDir(), Env: noEnv}
	svc, r, err := s.ForRemote("git@github.com:madkoding/motita.git")
	if err != nil || svc.ID != "github" || r.Path != "madkoding/motita" {
		t.Fatalf("%+v %+v %v", svc, r, err)
	}
	if _, _, err := s.ForRemote("https://example.org/a/b"); err != ErrNoService {
		t.Errorf("err = %v", err)
	}
	if _, _, err := s.ForRemote("nonsense"); err == nil || err == ErrNoService {
		t.Errorf("err = %v", err)
	}
}
