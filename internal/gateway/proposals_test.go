package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/skills"
)

// fakeProposals is a library that keeps proposals.
type fakeProposals struct {
	fakeSkills
	proposed []string
	listErr  error
	err      error
	accepted []string
	rejected []string
}

func (f *fakeProposals) ProposedSkills() ([]string, error) { return f.proposed, f.listErr }

func (f *fakeProposals) ProposedSkill(n string) (skills.Skill, error) {
	return skills.Skill{Name: n, Title: "T", Body: "B"}, f.err
}

func (f *fakeProposals) AcceptProposedSkill(n string) error {
	f.accepted = append(f.accepted, n)
	return f.err
}

func (f *fakeProposals) RejectProposedSkill(n string) error {
	f.rejected = append(f.rejected, n)
	return f.err
}

func TestProposedSkillsAreReviewedOverHTTP(t *testing.T) {
	lib := &fakeProposals{}
	srv := withSkills(t, lib)

	if w := get(t, srv, "/v1/skills/proposed", testToken); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"skills":[]}` {
		t.Errorf("none: %d %s", w.Code, w.Body.String())
	}
	lib.proposed = []string{"deploy"}
	if w := get(t, srv, "/v1/skills/proposed", testToken); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deploy"`) {
		t.Errorf("one: %d %s", w.Code, w.Body.String())
	}
	if w := get(t, srv, "/v1/skills/proposed/deploy", testToken); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"body":"B"`) {
		t.Errorf("read: %d %s", w.Code, w.Body.String())
	}
	if w := send(t, srv, http.MethodPost, "/v1/skills/proposed/deploy/accept", testToken, ""); w.Code != http.StatusNoContent || fmt.Sprint(lib.accepted) != "[deploy]" {
		t.Errorf("accept: %d %v", w.Code, lib.accepted)
	}
	if w := del(t, srv, "/v1/skills/proposed/junk", testToken); w.Code != http.StatusNoContent || fmt.Sprint(lib.rejected) != "[junk]" {
		t.Errorf("reject: %d %v", w.Code, lib.rejected)
	}

	// No such proposal: 404. A disk that refused: 500.
	for err, want := range map[error]int{
		fmt.Errorf("%w: nope", skills.ErrNotFound): http.StatusNotFound,
		errors.New("disk"):                         http.StatusInternalServerError,
	} {
		lib.err = err
		for _, w := range []int{
			get(t, srv, "/v1/skills/proposed/nope", testToken).Code,
			send(t, srv, http.MethodPost, "/v1/skills/proposed/nope/accept", testToken, "").Code,
			del(t, srv, "/v1/skills/proposed/nope", testToken).Code,
		} {
			if w != want {
				t.Errorf("%v: %d, want %d", err, w, want)
			}
		}
	}
	lib.listErr = errors.New("unreadable")
	if w := get(t, srv, "/v1/skills/proposed", testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable list: %d", w.Code)
	}
}

// A library without proposals, and no library at all, are each said with a 501.
func TestProposedSkillsWithoutALibraryThatKeepsThem(t *testing.T) {
	for _, srv := range []*Server{withSkills(t, &fakeSkills{}), newTestServer(t, &fakeService{})} {
		for _, w := range []int{
			get(t, srv, "/v1/skills/proposed", testToken).Code,
			get(t, srv, "/v1/skills/proposed/x", testToken).Code,
			send(t, srv, http.MethodPost, "/v1/skills/proposed/x/accept", testToken, "").Code,
			del(t, srv, "/v1/skills/proposed/x", testToken).Code,
		} {
			if w != http.StatusNotImplemented {
				t.Errorf("%d, want 501", w)
			}
		}
	}
}
