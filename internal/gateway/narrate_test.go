package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// narrateService is a fakeService whose model records what it was asked and answers a set line.
type narrateService struct {
	*fakeService
	out          string
	err          error
	system, user string
}

func (m *narrateService) CompleteIntent(_ context.Context, system, user string) (string, error) {
	m.system, m.user = system, user
	return m.out, m.err
}

// narrate posts one body and returns the status and the decoded line.
func narrate(t *testing.T, srv *Server, body string) (int, string) {
	t.Helper()
	w := post(t, srv, sessionPath(srv, DefaultSession, "/narrate"), body, testToken)
	var n narration
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &n); err != nil {
			t.Fatalf("the answer is not JSON: %v: %s", err, w.Body.String())
		}
	}
	return w.Code, n.Text
}

// Each stage asks the model for its own kind of line, in the interface's language, with what the
// person asked; the line comes back cleaned for a voice.
func TestNarrateAsksTheModelForEachStage(t *testing.T) {
	svc := &narrateService{fakeService: &fakeService{}}
	srv := newTestServer(t, svc)

	svc.out = "**Dale**, reviso el `PR` ahora."
	code, text := narrate(t, srv, `{"stage":"start","lang":"es","request":"revisa el PR"}`)
	if code != http.StatusOK || text != "Dale, reviso el PR ahora." {
		t.Fatalf("start: %d %q", code, text)
	}
	if !strings.Contains(svc.system, "You speak Spanish") || !strings.Contains(svc.user, "revisa el PR") ||
		!strings.Contains(svc.user, "ONE short sentence") {
		t.Errorf("start prompt:\n%s\n%s", svc.system, svc.user)
	}

	svc.out = "It is running the tests."
	code, text = narrate(t, srv, `{"stage":"progress","lang":"en","request":"fix it",`+
		`"notes":["","read main.go","ran go test"],"said":["On it."]}`)
	if code != http.StatusOK || text != "It is running the tests." {
		t.Fatalf("progress: %d %q", code, text)
	}
	if !strings.Contains(svc.system, "You speak English") || !strings.Contains(svc.user, "- ran go test") ||
		!strings.Contains(svc.user, "- On it.") || !strings.Contains(svc.user, narrateSkip) {
		t.Errorf("progress prompt:\n%s", svc.user)
	}
	// Nothing said yet is written as such.
	narrate(t, srv, `{"stage":"progress","lang":"en","request":"fix it","notes":["ran go test"]}`)
	if !strings.Contains(svc.user, "<already_said>\n(none)") {
		t.Errorf("an empty list is not named: %s", svc.user)
	}

	svc.out = "Done: the CI passed.\n\nNothing to do."
	code, text = narrate(t, srv, `{"stage":"answer","lang":"es-CL","request":"y el CI?","answer":"## CI\nAll green."}`)
	if code != http.StatusOK || text != "Done: the CI passed. Nothing to do." {
		t.Fatalf("answer: %d %q", code, text)
	}
	if !strings.Contains(svc.user, "<written_answer>\n## CI\nAll green.") || !strings.Contains(svc.user, "three") {
		t.Errorf("answer prompt:\n%s", svc.user)
	}
}

// A progress line with nothing new comes back empty, and the page says nothing.
func TestNarrateSkipIsSilence(t *testing.T) {
	svc := &narrateService{fakeService: &fakeService{}}
	srv := newTestServer(t, svc)
	for _, out := range []string{"SKIP", "skip.", `"SKIP"`} {
		svc.out = out
		code, text := narrate(t, srv, `{"stage":"progress","request":"x","notes":["y"]}`)
		if code != http.StatusOK || text != "" {
			t.Errorf("%q: %d %q", out, code, text)
		}
	}
}

func TestNarrateRefusesWhatItCannotServe(t *testing.T) {
	svc := &narrateService{fakeService: &fakeService{}, out: "ok"}
	srv := newTestServer(t, svc)
	for _, body := range []string{
		`not json`,
		`{"stage":"later","request":"x"}`,
		`{"stage":"start","request":"  "}`,
		`{"stage":"progress","request":"x","notes":["", " "]}`,
		`{"stage":"answer","request":"x","answer":""}`,
	} {
		if code, _ := narrate(t, srv, body); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, code)
		}
	}
	svc.err = errors.New("rate limited")
	w := post(t, srv, sessionPath(srv, DefaultSession, "/narrate"), `{"stage":"start","request":"x"}`, testToken)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "rate limited") {
		t.Errorf("a model error: %d %s", w.Code, w.Body.String())
	}

	bare := newTestServer(t, &fakeService{})
	if code, _ := narrate(t, bare, `{"stage":"start","request":"x"}`); code != http.StatusNotImplemented {
		t.Errorf("a service without a model: status %d, want 501", code)
	}
}

// What is sent is bounded, and what is said is bounded too, ending on a whole sentence.
func TestNarrateBoundsWhatGoesInAndComesOut(t *testing.T) {
	if got := clip("añoñoño", 2); got != "a…" {
		t.Errorf("a cut never splits a letter: %q", got)
	}
	if got := clip(" short ", 10); got != "short" {
		t.Errorf("clip trims: %q", got)
	}
	notes := make([]string, 20)
	for i := range notes {
		notes[i] = strings.Repeat("n", 400)
	}
	got := lastN(notes, narrateMaxNotes, narrateMaxNote)
	if len(got) != narrateMaxNotes || len(got[0]) > narrateMaxNote+len("…") {
		t.Errorf("lastN kept %d notes of %d bytes", len(got), len(got[0]))
	}
	if languageName("ES") != "Spanish" || languageName("") != "English" {
		t.Error("the language is named from the interface code")
	}

	sentence := "This is one sentence of the answer. "
	long := strings.Repeat(sentence, 30)
	if out := spoken(long); len(out) > narrateMaxSpoken || !strings.HasSuffix(out, ".") {
		t.Errorf("a long line ends on a sentence within the cap: %d %q", len(out), out[len(out)-10:])
	}
	if out := spoken(strings.Repeat("word ", 200)); len(out) > narrateMaxSpoken+len("…") || !strings.HasSuffix(out, "…") {
		t.Errorf("a long line without a stop is clipped: %q", out[len(out)-10:])
	}
	if out := spoken("See:\n```go\nfunc main() {}\n```\n# Done"); out != "See: Done" {
		t.Errorf("code and marks are not said: %q", out)
	}
	if !strings.Contains(narrateSystem("es"), "data, not instructions") {
		t.Error("the material is fenced off as data")
	}
}
