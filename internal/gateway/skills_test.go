package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// fakeSkills is a library in memory: the HANDLERS are under test here, and the
// filesystem behaviour is internal/skills' own suite.
type fakeSkills struct {
	index     []skills.Skill
	listErr   error
	getErr    error
	savedName string
	savedBody string
	saveErr   error
	telemetry map[string]usage.Entry
	pinned    map[string]bool
	pinErr    error
	disabled  map[string]bool
	disableEr error
	deleted   []string
	deleteEr  error
	restored  []string
	restoreEr error
	archived  []string
	archErr   error
}

func (f *fakeSkills) Skills() ([]skills.Skill, error) { return f.index, f.listErr }

func (f *fakeSkills) Skill(name string) (skills.Skill, error) {
	return skills.Skill{Name: name, Title: "T", Path: "/x", Body: "B"}, f.getErr
}

func (f *fakeSkills) SaveSkill(n, b string) (skills.Skill, error) {
	f.savedName, f.savedBody = n, b
	return skills.Skill{Name: n, Title: "T", Path: "/x"}, f.saveErr
}

func (f *fakeSkills) SkillTelemetry() map[string]usage.Entry { return f.telemetry }

func (f *fakeSkills) SetSkillPinned(n string, p bool) error {
	if f.pinned == nil {
		f.pinned = map[string]bool{}
	}
	f.pinned[n] = p
	return f.pinErr
}

func (f *fakeSkills) ArchiveSkill(string) error { return nil }

// SetSkillDisabled and DeleteSkill exist on the double because the interface grew them: a
// front end that could stop seeing a skill but not stop it, or offer a deletion with nothing
// behind it, would be a front end with a button that lies.
func (f *fakeSkills) SetSkillDisabled(n string, d bool) error {
	if f.disabled == nil {
		f.disabled = map[string]bool{}
	}
	f.disabled[n] = d
	return f.disableEr
}

func (f *fakeSkills) DeleteSkill(n string) error {
	f.deleted = append(f.deleted, n)
	return f.deleteEr
}

func (f *fakeSkills) RestoreSkill(n string) error {
	f.restored = append(f.restored, n)
	return f.restoreEr
}

func (f *fakeSkills) ArchivedSkills() ([]string, error) { return f.archived, f.archErr }

// fakeCurator stands in for the maintenance pass.
type fakeCurator struct {
	status  string
	runText string
	runErr  error
	seen    [2]bool
}

func (f *fakeCurator) CuratorStatus() string { return f.status }

func (f *fakeCurator) CuratorRun(_ context.Context, c, d bool) (string, error) {
	f.seen = [2]bool{c, d}
	return f.runText, f.runErr
}

// withSkills builds a server with a library and nothing else.
func withSkills(t *testing.T, lib SkillService) *Server {
	t.Helper()
	return newTestServer(t, &fakeService{}, func(o *Options) { o.Skills = lib })
}

// TestTheSkillIndexIsServedWithoutBodies: the index is what a browser draws, and a body is
// what a user opens a document to read. Sending forty procedures to draw forty titles is
// how a browser feels slow for no reason.
func TestTheSkillIndexIsServedWithoutBodies(t *testing.T) {
	lib := &fakeSkills{
		index: []skills.Skill{{Name: "one", Title: "One", Summary: "S", Path: "/x/one.md"}},
		telemetry: map[string]usage.Entry{
			"one": {UseCount: 3, ViewCount: 2, PatchCount: 1, State: usage.StateActive, CreatedBy: usage.ByAgent},
		},
	}
	srv := withSkills(t, lib)

	rec := get(t, srv, "/v1/skills", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/skills = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Skills []struct {
			Name       string `json:"name"`
			Title      string `json:"title"`
			CreatedBy  string `json:"created_by"`
			State      string `json:"state"`
			UseCount   int    `json:"use_count"`
			ViewCount  int    `json:"view_count"`
			PatchCount int    `json:"patch_count"`
		} `json:"skills"`
	}
	decodeJSON(t, rec, &out)
	if len(out.Skills) != 1 || out.Skills[0].Name != "one" {
		t.Fatalf("skills = %+v, want one entry named one", out.Skills)
	}
	if out.Skills[0].UseCount != 3 || out.Skills[0].ViewCount != 2 || out.Skills[0].PatchCount != 1 {
		t.Errorf("telemetry did not ride along: %+v", out.Skills[0])
	}
	if out.Skills[0].CreatedBy != "agent" || out.Skills[0].State != "active" {
		t.Errorf("lifecycle did not ride along: %+v", out.Skills[0])
	}
	if strings.Contains(rec.Body.String(), `"body"`) {
		t.Errorf("the index carries a body, which is the one thing it must not:\n%s", rec.Body.String())
	}
}

// TestASkillIsServedWithItsBody: opening one document is the act the index exists to make
// possible, so the body is the payload.
func TestASkillIsServedWithItsBody(t *testing.T) {
	srv := withSkills(t, &fakeSkills{})

	rec := get(t, srv, "/v1/skills/one", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/skills/one = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	decodeJSON(t, rec, &out)
	if out.Body != "B" {
		t.Errorf("body = %q, want B", out.Body)
	}
}

// TestAnUnknownSkillIsReportedAsNotFound: 404 and not 500. A name that is not there is the
// client's to fix, and the message the library wrote is the one that says which rule it hit.
func TestAnUnknownSkillIsReportedAsNotFound(t *testing.T) {
	srv := withSkills(t, &fakeSkills{
		getErr: fmt.Errorf("%w: %q", skills.ErrNotFound, "nope"),
	})

	rec := get(t, srv, "/v1/skills/nope", testToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /v1/skills/nope = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no skill matches") {
		t.Errorf("the 404 does not carry the library's reason: %s", rec.Body.String())
	}
}

// TestASkillThatCannotBeReadIsNotANotFound: a library that FAILED is not a library with
// nothing in it. The distinction is the difference between "check your spelling" and
// "something is wrong on the server".
func TestASkillThatCannotBeReadIsNotANotFound(t *testing.T) {
	srv := withSkills(t, &fakeSkills{getErr: errors.New("the disk went away")})

	if rec := get(t, srv, "/v1/skills/one", testToken); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500: %s", rec.Code, rec.Body.String())
	}
}

// TestSavingASkillRequiresANameAndABody: both are what a document IS. A skill with no body
// is a title, and a skill with no name has no path.
func TestSavingASkillRequiresANameAndABody(t *testing.T) {
	srv := withSkills(t, &fakeSkills{})

	cases := []struct {
		body string
		want string
	}{
		{`{}`, "the name cannot be empty"},
		{`{"name":"a"}`, "the body cannot be empty"},
		{`{"name":"  ","body":"b"}`, "the name cannot be empty"},
		{`{"name":"a","body":"   "}`, "the body cannot be empty"},
	}
	for _, c := range cases {
		rec := post(t, srv, "/v1/skills", c.body, testToken)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", c.body, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("POST %s = %s, want %q", c.body, rec.Body.String(), c.want)
		}
	}
}

// TestSavingASkillPassesNameAndBodyOnUntouched: the handler is a transport, not a second
// implementation of the library's rules. What the client sent is what the library sees.
func TestSavingASkillPassesNameAndBodyOnUntouched(t *testing.T) {
	lib := &fakeSkills{}
	srv := withSkills(t, lib)

	rec := post(t, srv, "/v1/skills", `{"name":"One","body":"# One\n"}`, testToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("= %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if lib.savedName != "One" || lib.savedBody != "# One\n" {
		t.Errorf("the library received (%q, %q), want (\"One\", \"# One\\n\")", lib.savedName, lib.savedBody)
	}
}

// TestASaveTheLibraryRefusesIsTheClientsFault: the library refuses a name it cannot use and
// a body past the cap. Both are things the CLIENT sent, so both are 400 - and the message is
// the library's, because the library is the only thing that knows the cap.
func TestASaveTheLibraryRefusesIsTheClientsFault(t *testing.T) {
	srv := withSkills(t, &fakeSkills{saveErr: errors.New("past the 200000-byte limit")})

	rec := post(t, srv, "/v1/skills", `{"name":"a","body":"b"}`, testToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "past the") {
		t.Errorf("the 400 does not carry the library's reason: %s", rec.Body.String())
	}
}

// TestPinningASkillTakesABoolean: the pin is the user's veto over every automatic
// transition, and it has to be settable in BOTH directions - a pin that could only be set
// would be a pin with no way back.
func TestPinningASkillTakesABoolean(t *testing.T) {
	lib := &fakeSkills{}
	srv := withSkills(t, lib)

	if rec := post(t, srv, "/v1/skills/one/pin", `{"pinned":true}`, testToken); rec.Code != http.StatusNoContent {
		t.Fatalf("pin = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if !lib.pinned["one"] {
		t.Error("the pin was not recorded")
	}
	if rec := post(t, srv, "/v1/skills/one/pin", `{"pinned":false}`, testToken); rec.Code != http.StatusNoContent {
		t.Fatalf("unpin = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if lib.pinned["one"] {
		t.Error("the pin was not removed")
	}
}

// TestAPinTheLibraryRefusesIsReported: pinning without a ledger cannot be remembered, and a
// front end that was told "done" would be told a lie about the user's own veto.
func TestAPinTheLibraryRefusesIsReported(t *testing.T) {
	srv := withSkills(t, &fakeSkills{pinErr: errors.New("no usage ledger is configured")})

	rec := post(t, srv, "/v1/skills/one/pin", `{"pinned":true}`, testToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ledger") {
		t.Errorf("the reason is not carried: %s", rec.Body.String())
	}
}

// TestTheArchivedListIsAlwaysAList: never null. A client that has to tell "nothing was
// archived" from "the field is missing" is a client with a bug waiting to happen.
func TestTheArchivedListIsAlwaysAList(t *testing.T) {
	srv := withSkills(t, &fakeSkills{})

	rec := get(t, srv, "/v1/skills/archived", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"skills":[]`) {
		t.Errorf("an empty archive must be [], not null: %s", rec.Body.String())
	}
}

// TestAnUnreadableArchiveIsNotAnEmptyArchive: the same distinction the whole package makes
// everywhere else. "Nothing was archived" and "I could not read the archive" lead to
// different next steps.
func TestAnUnreadableArchiveIsNotAnEmptyArchive(t *testing.T) {
	srv := withSkills(t, &fakeSkills{archErr: errors.New("could not read the archive directory")})

	if rec := get(t, srv, "/v1/skills/archived", testToken); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500: %s", rec.Code, rec.Body.String())
	}
}

// TestRestoringAnArchivedSkillIsAddressedByName: the archive is addressed by name because
// that is all it holds - there is no body to disambiguate with.
func TestRestoringAnArchivedSkillIsAddressedByName(t *testing.T) {
	lib := &fakeSkills{}
	srv := withSkills(t, lib)

	if rec := post(t, srv, "/v1/skills/one/restore", `{}`, testToken); rec.Code != http.StatusNoContent {
		t.Fatalf("= %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if len(lib.restored) != 1 || lib.restored[0] != "one" {
		t.Errorf("restored = %v, want [one]", lib.restored)
	}
}

// TestRestoringWhatIsNotArchivedIsNotFound: 404 and not 500. The name is not in the archive,
// which is the client's to fix.
func TestRestoringWhatIsNotArchivedIsNotFound(t *testing.T) {
	srv := withSkills(t, &fakeSkills{restoreEr: errors.New("no archived skill named \"nope\"")})

	if rec := post(t, srv, "/v1/skills/nope/restore", `{}`, testToken); rec.Code != http.StatusNotFound {
		t.Fatalf("= %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestTheSkillsEndpointsSayWhenNoLibraryIsConfigured: 501 and not 404. "This gateway was
// started without a library" is a different fact from "there is no such skill", and a client
// acts differently on each.
func TestTheSkillsEndpointsSayWhenNoLibraryIsConfigured(t *testing.T) {
	srv := newTestServer(t, &fakeService{})

	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/v1/skills", ""},
		{http.MethodPost, "/v1/skills", `{"name":"a","body":"b"}`},
		{http.MethodGet, "/v1/skills/archived", ""},
		{http.MethodGet, "/v1/skills/one", ""},
		{http.MethodPost, "/v1/skills/one/pin", `{"pinned":true}`},
		{http.MethodPost, "/v1/skills/one/restore", `{}`},
		{http.MethodPost, "/v1/skills/one/disable", `{"disabled":true}`},
		{http.MethodDelete, "/v1/skills/one", ""},
	}
	for _, c := range cases {
		rec := call(t, srv, c.method, c.path, c.body, testToken)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501: %s", c.method, c.path, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), "without a procedure library") {
			t.Errorf("%s %s says the wrong thing: %s", c.method, c.path, rec.Body.String())
		}
	}
}

// TestTheSkillIndexReportsAFailureRatherThanAnEmptyLibrary: an unreadable library is not an
// empty one, and a browser that draws "no skills" for a broken directory sends the user
// looking for documents they still have.
func TestTheSkillIndexReportsAFailureRatherThanAnEmptyLibrary(t *testing.T) {
	srv := withSkills(t, &fakeSkills{listErr: errors.New("boom")})

	rec := get(t, srv, "/v1/skills", testToken)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("the reason is not carried: %s", rec.Body.String())
	}
}

// TestTheCuratorReports: the status is text, and it is served verbatim.
func TestTheCuratorReports(t *testing.T) {
	cur := &fakeCurator{status: "curator: ENABLED"}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Curator = cur })

	rec := get(t, srv, "/v1/curator", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	var out struct {
		Text string `json:"text"`
	}
	decodeJSON(t, rec, &out)
	if out.Text != "curator: ENABLED" {
		t.Errorf("text = %q", out.Text)
	}
}

// TestTheCuratorRunsWithTheFlagsItWasGiven: both flags reach the pass. A dry run that ran
// for real would be the worst possible outcome of this endpoint.
func TestTheCuratorRunsWithTheFlagsItWasGiven(t *testing.T) {
	cur := &fakeCurator{runText: "preview (nothing was changed): 1 stale, 0 archived"}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Curator = cur })

	rec := post(t, srv, "/v1/curator/run", `{"consolidate":true,"dry_run":true}`, testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if cur.seen != [2]bool{true, true} {
		t.Errorf("the pass received (consolidate=%v, dryRun=%v), want both true", cur.seen[0], cur.seen[1])
	}
	var out struct {
		Text string `json:"text"`
	}
	decodeJSON(t, rec, &out)
	if !strings.Contains(out.Text, "preview") {
		t.Errorf("text = %q, want the pass's own words", out.Text)
	}
}

// TestTheCuratorRunDefaultIsARealPass: the zero body is "run it for real", because that is
// what the button says. A default of dry-run would make the endpoint quietly do nothing.
func TestTheCuratorRunDefaultIsARealPass(t *testing.T) {
	cur := &fakeCurator{}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Curator = cur })

	if rec := post(t, srv, "/v1/curator/run", `{}`, testToken); rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	if cur.seen != [2]bool{false, false} {
		t.Errorf("empty body gave (%v, %v), want a real pass", cur.seen[0], cur.seen[1])
	}
}

// TestACuratorRunThatFailedIsABadGateway: the pass fails because the MODEL it consolidated
// with failed, and that is the same reading handleModels uses.
func TestACuratorRunThatFailedIsABadGateway(t *testing.T) {
	cur := &fakeCurator{runErr: errors.New("the model went away")}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Curator = cur })

	rec := post(t, srv, "/v1/curator/run", `{}`, testToken)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("= %d, want 502: %s", rec.Code, rec.Body.String())
	}
}

// TestTheCuratorEndpointsSayWhenThereIsNoCurator: the same 501 rule as the library.
func TestTheCuratorEndpointsSayWhenThereIsNoCurator(t *testing.T) {
	srv := newTestServer(t, &fakeService{})

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/curator"},
		{http.MethodPost, "/v1/curator/run"},
	} {
		rec := call(t, srv, c.method, c.path, `{}`, testToken)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501", c.method, c.path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "without a curator") {
			t.Errorf("%s %s says the wrong thing: %s", c.method, c.path, rec.Body.String())
		}
	}
}

// TestTheArchivedRouteWinsOverASkillNamedArchived: /v1/skills/archived is registered
// EXPLICITLY and is more specific than /v1/skills/{name}, so it wins. A skill that happens to
// be called "archived" is still reachable by opening it from the index.
//
// This is asserted rather than trusted to the mux: the precedence is a property of this
// program's routing, and a refactor that lost it would silently turn a list into a document.
func TestTheArchivedRouteWinsOverASkillNamedArchived(t *testing.T) {
	lib := &fakeSkills{archived: []string{"one"}}
	srv := withSkills(t, lib)

	rec := get(t, srv, "/v1/skills/archived", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	var list struct {
		Skills []string `json:"skills"`
	}
	decodeJSON(t, rec, &list)
	if len(list.Skills) != 1 || list.Skills[0] != "one" {
		t.Fatalf("GET /v1/skills/archived = %+v, want the LIST of archived names", list.Skills)
	}
	if strings.Contains(rec.Body.String(), `"body"`) {
		t.Error("the archived route answered with a document: the explicit route lost to the wildcard")
	}
}

// TestTheSkillRoutesNeedTheToken: they all go through the same authorising wrapper, so one
// route proves the group - but it must be proved.
func TestTheSkillRoutesNeedTheToken(t *testing.T) {
	srv := withSkills(t, &fakeSkills{})

	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/skills", ""},
		{http.MethodPost, "/v1/skills", `{"name":"a","body":"b"}`},
		{http.MethodGet, "/v1/skills/archived", ""},
		{http.MethodGet, "/v1/skills/one", ""},
		{http.MethodPost, "/v1/skills/one/pin", `{"pinned":true}`},
		{http.MethodPost, "/v1/skills/one/restore", `{}`},
		{http.MethodGet, "/v1/curator", ""},
	} {
		if rec := call(t, srv, c.method, c.path, c.body, "wrong-token"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a valid token = %d, want 401", c.method, c.path, rec.Code)
		}
	}
}

// call performs one authenticated request of the given method against the server.
//
// get() and post() are the package's own helpers and cover the two methods these routes use;
// this exists only for the tables, where the method is a column and the cases must not each
// grow their own branch.
func call(t *testing.T, srv *Server, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	switch method {
	case http.MethodGet:
		return get(t, srv, path, token)
	case http.MethodDelete:
		return del(t, srv, path, token)
	}
	return post(t, srv, path, body, token)
}

// del performs one authenticated DELETE. It exists because call() grew a third method: the
// deletion of a document is the one route here that carries its whole meaning in the verb,
// and a table whose DELETE case silently became a POST would test the wrong handler.
func del(t *testing.T, srv *Server, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.BaseURL()+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// decodeJSON unmarshals a recorder's body, naming the body when it is not JSON.
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("body = %q: %v", rec.Body.String(), err)
	}
}

// TestTheSkillWritesRejectAMalformedBody: decodeBody is the same wrapper every other write
// endpoint uses, and it answers 400 with its own message. A body that is not JSON is the
// client's to fix, and it must not reach the library at all.
func TestTheSkillWritesRejectAMalformedBody(t *testing.T) {
	lib := &fakeSkills{}
	srv := withSkills(t, lib)
	cur := &fakeCurator{}
	srv.opts.Curator = cur

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/skills"},
		{http.MethodPost, "/v1/skills/one/pin"},
		{http.MethodPost, "/v1/skills/one/disable"},
		{http.MethodPost, "/v1/curator/run"},
	} {
		if rec := call(t, srv, c.method, c.path, `{"name":`, testToken); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s with malformed JSON = %d, want 400: %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	// And nothing reached either backing service.
	if lib.savedName != "" || lib.pinned != nil || lib.disabled != nil {
		t.Errorf("a malformed body reached the library: saved=%q pinned=%v disabled=%v", lib.savedName, lib.pinned, lib.disabled)
	}
	if cur.seen != [2]bool{false, false} {
		t.Error("a malformed body ran the maintenance pass")
	}
}

// TestASkillWithNoTelemetryIsStillListed: a document the ledger has never heard of - written
// by an older build, or copied in by hand - is a skill, and a browser that dropped it from
// the list would be hiding a document the user has.
func TestASkillWithNoTelemetryIsStillListed(t *testing.T) {
	lib := &fakeSkills{
		index:     []skills.Skill{{Name: "orphan", Title: "Orphan", Path: "/x/orphan.md"}},
		telemetry: map[string]usage.Entry{},
	}
	srv := withSkills(t, lib)

	rec := get(t, srv, "/v1/skills", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	var out struct {
		Skills []struct {
			Name      string `json:"name"`
			CreatedBy string `json:"created_by"`
			State     string `json:"state"`
		} `json:"skills"`
	}
	decodeJSON(t, rec, &out)
	if len(out.Skills) != 1 || out.Skills[0].Name != "orphan" {
		t.Fatalf("skills = %+v, want the orphan listed", out.Skills)
	}
	if out.Skills[0].CreatedBy != "" || out.Skills[0].State != "" {
		t.Errorf("an untracked skill must not invent a lifecycle: %+v", out.Skills[0])
	}
}

// TestTheIndexCarriesTheLastUsedTime: the list sorts and labels by freshness, and a time
// that never reached the client would make every row look equally old.
func TestTheIndexCarriesTheLastUsedTime(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	lib := &fakeSkills{
		index: []skills.Skill{{Name: "one", Title: "One"}},
		telemetry: map[string]usage.Entry{
			"one": {UseCount: 1, LastUsedAt: when, State: usage.StateActive, CreatedBy: usage.ByAgent},
		},
	}
	srv := withSkills(t, lib)

	rec := get(t, srv, "/v1/skills", testToken)
	var out struct {
		Skills []struct {
			LastUsedAt *time.Time `json:"last_used_at"`
		} `json:"skills"`
	}
	decodeJSON(t, rec, &out)
	if len(out.Skills) != 1 || out.Skills[0].LastUsedAt == nil {
		t.Fatalf("last_used_at is missing: %s", rec.Body.String())
	}
	if !out.Skills[0].LastUsedAt.Equal(when) {
		t.Errorf("last_used_at = %v, want %v", out.Skills[0].LastUsedAt, when)
	}

	// And an entry that was never used omits the field rather than sending the zero time:
	// 0001-01-01 rendered in a browser is a bug report, not a date.
	lib2 := &fakeSkills{
		index: []skills.Skill{{Name: "fresh", Title: "F"}},
		telemetry: map[string]usage.Entry{
			"fresh": {State: usage.StateActive, CreatedBy: usage.ByAgent},
		},
	}
	rec2 := get(t, withSkills(t, lib2), "/v1/skills", testToken)
	if strings.Contains(rec2.Body.String(), "last_used_at") {
		t.Errorf("a never-used skill carries a zero timestamp: %s", rec2.Body.String())
	}
}

// TestTurningASkillOffAndOn: the body is {"disabled":true|false}, and 204 in both directions.
// It is not a state of the document but a decision of the user, so nothing comes back: there
// is nothing to say beyond "it was applied".
func TestTurningASkillOffAndOn(t *testing.T) {
	lib := &fakeSkills{}
	srv := withSkills(t, lib)

	if rec := post(t, srv, "/v1/skills/one/disable", `{"disabled":true}`, testToken); rec.Code != http.StatusNoContent {
		t.Fatalf("disabling = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if !lib.disabled["one"] {
		t.Error("disabling did not reach the library")
	}
	if rec := post(t, srv, "/v1/skills/one/disable", `{"disabled":false}`, testToken); rec.Code != http.StatusNoContent {
		t.Fatalf("enabling = %d, want 204", rec.Code)
	}
	if lib.disabled["one"] {
		t.Error("enabling did not reach the library")
	}
}

// TestTheDisableEndpointReportsWhyItFailed: 400 and not 500, because the documented failure is
// a gateway without a usage ledger — the environment the client is talking to, not a broken
// server — and the message travels whole, which is the whole difference for whoever reads it.
func TestTheDisableEndpointReportsWhyItFailed(t *testing.T) {
	srv := withSkills(t, &fakeSkills{disableEr: errors.New("no usage ledger is configured")})

	rec := post(t, srv, "/v1/skills/one/disable", `{"disabled":true}`, testToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ledger") {
		t.Errorf("the reason is not carried: %s", rec.Body.String())
	}
}

// TestDeletingASkill: DELETE and 204. It is the library's one irreversible operation, and the
// interface asks for a confirmation before reaching here; the endpoint asks nothing, because a
// confirmation has to be on the side of the human who answers it.
func TestDeletingASkill(t *testing.T) {
	lib := &fakeSkills{}
	srv := withSkills(t, lib)

	rec := call(t, srv, http.MethodDelete, "/v1/skills/one", "", testToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if len(lib.deleted) != 1 || lib.deleted[0] != "one" {
		t.Errorf("deleted = %v, want [one]", lib.deleted)
	}
}

// TestDeletingABuiltinIsRefused: 409 and not 400. The name is fine and what failed is the
// library's STATE — the document is the one that ships with the binary — which is the same
// distinction the archive makes with the default session.
func TestDeletingABuiltinIsRefused(t *testing.T) {
	srv := withSkills(t, &fakeSkills{deleteEr: errors.New(`the skill "x" is built in: it ships inside the binary and cannot be deleted`)})

	rec := call(t, srv, http.MethodDelete, "/v1/skills/x", "", testToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("= %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// TestTheIndexCarriesTheOffFlag: the list is where the user sees which skills are off, so the
// flag rides along with the rest of the telemetry — a badge the front end cannot draw without
// a round trip per skill is a badge that does not get drawn.
func TestTheIndexCarriesTheOffFlag(t *testing.T) {
	lib := &fakeSkills{
		index:     []skills.Skill{{Name: "one", Title: "One"}},
		telemetry: map[string]usage.Entry{"one": {Disabled: true, State: usage.StateActive}},
	}
	srv := withSkills(t, lib)

	rec := get(t, srv, "/v1/skills", testToken)
	var out struct {
		Skills []struct {
			Name     string `json:"name"`
			Disabled bool   `json:"disabled"`
		} `json:"skills"`
	}
	decodeJSON(t, rec, &out)
	if len(out.Skills) != 1 || !out.Skills[0].Disabled {
		t.Errorf("the off flag did not ride along: %s", rec.Body.String())
	}
}
