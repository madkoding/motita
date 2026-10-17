package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// nextSuggest posts one body and returns the status and the decoded suggestion.
func nextSuggest(t *testing.T, srv *Server, body string) (int, string) {
	t.Helper()
	w := post(t, srv, sessionPath(srv, DefaultSession, "/next-suggestion"), body, testToken)
	var n nextSuggestion
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &n); err != nil {
			t.Fatalf("the answer is not JSON: %v: %s", err, w.Body.String())
		}
	}
	return w.Code, n.Text
}

func TestNextSuggestionAsksTheModelInTheInterfaceLanguage(t *testing.T) {
	svc := &narrateService{fakeService: &fakeService{}}
	srv := newTestServer(t, svc)
	svc.out = "\"**Agrega** tests para el parser.\"\n"
	code, text := nextSuggest(t, srv, `{"lang":"es","request":"arregla el parser",`+
		`"answer":"Listo, corregido.","files":["parser.go"]}`)
	if code != http.StatusOK || text != "Agrega tests para el parser" {
		t.Fatalf("got %d %q", code, text)
	}
	if !strings.Contains(svc.system, "Spanish") || !strings.Contains(svc.user, "arregla el parser") ||
		!strings.Contains(svc.user, "Listo, corregido.") || !strings.Contains(svc.user, "- parser.go") {
		t.Errorf("prompt:\n%s\n%s", svc.system, svc.user)
	}
}

func TestNextSuggestionIsBoundedToOneLine(t *testing.T) {
	svc := &narrateService{fakeService: &fakeService{}}
	srv := newTestServer(t, svc)
	svc.out = strings.Repeat("palabra ", 50) + "\n\n```x```"
	_, text := nextSuggest(t, srv, `{"lang":"en","request":"r","answer":"a"}`)
	if len(text) > nextMaxLine+len("…") || strings.Contains(text, "\n") {
		t.Errorf("not a bounded line: %q", text)
	}
}

func TestNextSuggestionRefusals(t *testing.T) {
	svc := &narrateService{fakeService: &fakeService{}}
	srv := newTestServer(t, svc)
	if code, _ := nextSuggest(t, srv, `{"lang":"en","request":"r","answer":" "}`); code != http.StatusBadRequest {
		t.Errorf("an empty answer: %d", code)
	}
	if code, _ := nextSuggest(t, srv, `nope`); code != http.StatusBadRequest {
		t.Errorf("a broken body: %d", code)
	}
	svc.err = errors.New("down")
	if code, _ := nextSuggest(t, srv, `{"lang":"en","request":"r","answer":"a"}`); code != http.StatusBadGateway {
		t.Errorf("a failing model: %d", code)
	}
	plain := newTestServer(t, &fakeService{})
	if code, _ := nextSuggest(t, plain, `{"lang":"en","request":"r","answer":"a"}`); code != http.StatusNotImplemented {
		t.Errorf("a service without a model: %d", code)
	}
}
