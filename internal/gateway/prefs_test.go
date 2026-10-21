package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putPrefs(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, srv.BaseURL()+"/v1/ui/prefs", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func prefsOf(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var v prefsView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v.Prefs
}

// TestPreferencesAreKeptByTheGatewayAndSurviveARestart: a value PUT is merged over the ones kept,
// answered to every browser, and read back by the next gateway that opens the same file.
func TestPreferencesAreKeptByTheGatewayAndSurviveARestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "ui-prefs.json")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.PrefsPath = file })

	if got := prefsOf(t, get(t, srv, "/v1/ui/prefs", testToken)); len(got) != 0 {
		t.Fatalf("fresh gateway kept %v", got)
	}
	if w := get(t, srv, "/v1/ui/prefs", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d", w.Code)
	}
	if w := putPrefs(t, srv, `{"prefs":{"motita.ui.theme":"{\"mode\":\"dark\"}"}}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	w := putPrefs(t, srv, `{"prefs":{"motita.ui.nextHint":"off"}}`)
	got := prefsOf(t, w)
	if got["motita.ui.theme"] != `{"mode":"dark"}` || got["motita.ui.nextHint"] != "off" {
		t.Fatalf("merge lost a value: %v", got)
	}
	if again := prefsOf(t, get(t, srv, "/v1/ui/prefs", testToken)); again["motita.ui.nextHint"] != "off" {
		t.Fatalf("GET = %v", again)
	}

	reopened := openPrefs(file).all()
	if reopened["motita.ui.theme"] != `{"mode":"dark"}` {
		t.Fatalf("not saved on disk: %v", reopened)
	}
}

// TestPreferencesRefuseWhatTheGatewayDoesNotKeep: an unknown key, an oversized value or a body
// that is not a preferences document changes nothing.
func TestPreferencesRefuseWhatTheGatewayDoesNotKeep(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.PrefsPath = filepath.Join(t.TempDir(), "p.json") })
	for name, body := range map[string]string{
		"unknown key": `{"prefs":{"motita.ui.other":"x"}}`,
		"too large":   `{"prefs":{"motita.ui.theme":"` + strings.Repeat("a", maxPrefBytes+1) + `"}}`,
		"not json":    `nope`,
	} {
		if w := putPrefs(t, srv, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, w.Code, w.Body.String())
		}
	}
	if got := prefsOf(t, get(t, srv, "/v1/ui/prefs", testToken)); len(got) != 0 {
		t.Fatalf("a refused PUT kept %v", got)
	}
}

// TestPreferencesWithoutAFileLastAsLongAsTheProcess, and a file that cannot be written is an error,
// not a silent loss.
func TestPreferencesWithoutAFileLastAsLongAsTheProcess(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if w := putPrefs(t, srv, `{"prefs":{"motita.ui.usageOpen":"open"}}`); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d", w.Code)
	}
	if got := prefsOf(t, get(t, srv, "/v1/ui/prefs", testToken)); got["motita.ui.usageOpen"] != "open" {
		t.Fatalf("GET = %v", got)
	}

	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	broken := newTestServer(t, &fakeService{}, func(o *Options) { o.PrefsPath = filepath.Join(blocker, "p.json") })
	if w := putPrefs(t, broken, `{"prefs":{"motita.ui.usageOpen":"open"}}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("unwritable = %d", w.Code)
	}
	if got := prefsOf(t, get(t, broken, "/v1/ui/prefs", testToken)); len(got) != 0 {
		t.Fatalf("a failed write was kept: %v", got)
	}

	// A file whose scratch name is taken by a folder cannot be written either.
	file := filepath.Join(t.TempDir(), "p.json")
	if err := os.Mkdir(file+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	stuck := newTestServer(t, &fakeService{}, func(o *Options) { o.PrefsPath = file })
	if w := putPrefs(t, stuck, `{"prefs":{"motita.ui.usageOpen":"open"}}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("unwritable scratch = %d", w.Code)
	}
}

// TestOpenPrefsIgnoresAForeignOrBrokenFile keeps only the keys the gateway knows.
func TestOpenPrefsIgnoresAForeignOrBrokenFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	_ = os.WriteFile(good, []byte(`{"motita.ui.theme":"x","other":"y"}`), 0o600)
	if got := openPrefs(good).all(); len(got) != 1 || got["motita.ui.theme"] != "x" {
		t.Fatalf("got %v", got)
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{`), 0o600)
	if got := openPrefs(bad).all(); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
