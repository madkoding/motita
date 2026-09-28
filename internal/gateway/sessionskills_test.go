package gateway

// The per-session library route.
//
// A library is scoped to a project, so "what does this session see" is a question only the
// session can answer. The process-wide /v1/skills stays exactly what it was — the shared shelf,
// which is a real thing to look at — and this route answers for a conversation, which is what a
// library window opened INSIDE a project must draw.
//
// Without it a scoped library is invisible over HTTP: the model sees the project's procedures
// and the user has no way to, which is the state these tests were written in.

import (
	"net/http"
	"testing"

	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// TestTheSessionSkillRouteAnswersForThatSession: the route exists, and it answers from the
// SESSION's service rather than the process-wide one. The two are given different libraries here
// so a route that reached for the wrong one is caught rather than passing on a coincidence.
func TestTheSessionSkillRouteAnswersForThatSession(t *testing.T) {
	processWide := &fakeSkills{
		index: []skills.Skill{{Name: "process-wide", Title: "P", Summary: "s", Path: "/p.md"}},
	}
	fromSession := &fakeSkills{
		index: []skills.Skill{{Name: "from-the-session", Title: "S", Summary: "s", Path: "/s.md"}},
	}

	// A service that carries a scope, which is what makes the session's own library the one
	// the route should use.
	svc := &scopedFakeService{fakeService: &fakeService{}, skills: fromSession}

	srv := newTestServer(t, svc, func(o *Options) {
		o.Skills = processWide
		o.NewService = func() (Service, error) { return svc, nil }
	})
	created := createSessionIn(t, srv, "")
	if created.ID == "" {
		t.Fatal("could not create a session to ask about")
	}

	rec := get(t, srv, "/v1/sessions/"+created.ID+"/skills", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("the per-session skill route answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	decodeJSON(t, rec, &out)
	if len(out.Skills) != 1 || out.Skills[0].Name != "from-the-session" {
		t.Errorf("the route must answer from the SESSION's library, got %+v", out.Skills)
	}
}

// TestTheProcessWideRouteStillAnswersFromTheProcess: the new route must not have taken the old
// one with it. /v1/skills is the shared shelf and a caller with no conversation in mind still
// gets exactly the list it always got.
func TestTheProcessWideRouteStillAnswersFromTheProcess(t *testing.T) {
	processWide := &fakeSkills{
		index: []skills.Skill{{Name: "process-wide", Title: "P", Summary: "s", Path: "/p.md"}},
	}
	svc := &scopedFakeService{
		fakeService: &fakeService{},
		skills: &fakeSkills{
			index: []skills.Skill{{Name: "from-the-session", Title: "S", Summary: "s", Path: "/s.md"}},
		},
	}
	srv := newTestServer(t, svc, func(o *Options) {
		o.Skills = processWide
		o.NewService = func() (Service, error) { return svc, nil }
	})

	rec := get(t, srv, "/v1/skills", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/skills = %d, want 200", rec.Code)
	}
	var out struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	decodeJSON(t, rec, &out)
	if len(out.Skills) != 1 || out.Skills[0].Name != "process-wide" {
		t.Errorf("the process-wide route must still answer from the process, got %+v", out.Skills)
	}
}

// TestTheSessionSkillRouteRefusesAnUnknownSession: the route is scoped, so a conversation that
// does not exist is refused. Answering it from the process-wide library would report a shelf
// that has nothing to do with the question that was asked.
func TestTheSessionSkillRouteRefusesAnUnknownSession(t *testing.T) {
	srv := withSkills(t, &fakeSkills{})

	rec := get(t, srv, "/v1/sessions/no-such-session/skills", testToken)
	if rec.Code == http.StatusOK {
		t.Error("an unknown session must not be answered about the process-wide library")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown session is a 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAServiceThatCannotBeScopedFallsBackToTheProcessWideLibrary: the optional interface means
// an embedder with no library of its own keeps working. The route must answer from the shared
// shelf rather than failing, because a window that refuses to open is worse than one showing
// the shelf.
func TestAServiceThatCannotBeScopedFallsBackToTheProcessWideLibrary(t *testing.T) {
	processWide := &fakeSkills{
		index: []skills.Skill{{Name: "shelf", Title: "S", Summary: "s", Path: "/s.md"}},
	}
	// fakeService implements SkillService but NOT SetProjectScope.
	svc := &unscopedSkillService{fakeService: &fakeService{}, skills: processWide}
	srv := newTestServer(t, svc, func(o *Options) {
		o.Skills = processWide
		o.NewService = func() (Service, error) { return svc, nil }
	})

	created := createSessionIn(t, srv, "")
	rec := get(t, srv, "/v1/sessions/"+created.ID+"/skills", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("a service that cannot be scoped must fall back, got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	decodeJSON(t, rec, &out)
	if len(out.Skills) != 1 || out.Skills[0].Name != "shelf" {
		t.Errorf("the fallback is the process-wide shelf, got %+v", out.Skills)
	}
}

// scopedFakeService is a Service that CAN carry a project scope, which is what the real
// AppRunner does. Its skill methods delegate to a library of its own.
type scopedFakeService struct {
	*fakeService
	skills SkillService
}

func (s *scopedFakeService) SetProjectScope(string)               {}
func (s *scopedFakeService) Skills() ([]skills.Skill, error)      { return s.skills.Skills() }
func (s *scopedFakeService) Skill(n string) (skills.Skill, error) { return s.skills.Skill(n) }
func (s *scopedFakeService) SaveSkill(n, b string) (skills.Skill, error) {
	return s.skills.SaveSkill(n, b)
}
func (s *scopedFakeService) SkillTelemetry() map[string]usage.Entry { return s.skills.SkillTelemetry() }
func (s *scopedFakeService) SetSkillPinned(n string, p bool) error {
	return s.skills.SetSkillPinned(n, p)
}
func (s *scopedFakeService) SetSkillDisabled(n string, d bool) error {
	return s.skills.SetSkillDisabled(n, d)
}
func (s *scopedFakeService) DeleteSkill(n string) error        { return s.skills.DeleteSkill(n) }
func (s *scopedFakeService) ArchiveSkill(n string) error       { return s.skills.ArchiveSkill(n) }
func (s *scopedFakeService) RestoreSkill(n string) error       { return s.skills.RestoreSkill(n) }
func (s *scopedFakeService) ArchivedSkills() ([]string, error) { return s.skills.ArchivedSkills() }

// unscopedSkillService serves skills but has NO SetProjectScope, so the route must fall back.
type unscopedSkillService struct {
	*fakeService
	skills SkillService
}

func (s *unscopedSkillService) Skills() ([]skills.Skill, error)      { return s.skills.Skills() }
func (s *unscopedSkillService) Skill(n string) (skills.Skill, error) { return s.skills.Skill(n) }
func (s *unscopedSkillService) SaveSkill(n, b string) (skills.Skill, error) {
	return s.skills.SaveSkill(n, b)
}
func (s *unscopedSkillService) SkillTelemetry() map[string]usage.Entry {
	return s.skills.SkillTelemetry()
}
func (s *unscopedSkillService) SetSkillPinned(n string, p bool) error {
	return s.skills.SetSkillPinned(n, p)
}
func (s *unscopedSkillService) SetSkillDisabled(n string, d bool) error {
	return s.skills.SetSkillDisabled(n, d)
}
func (s *unscopedSkillService) DeleteSkill(n string) error        { return s.skills.DeleteSkill(n) }
func (s *unscopedSkillService) ArchiveSkill(n string) error       { return s.skills.ArchiveSkill(n) }
func (s *unscopedSkillService) RestoreSkill(n string) error       { return s.skills.RestoreSkill(n) }
func (s *unscopedSkillService) ArchivedSkills() ([]string, error) { return s.skills.ArchivedSkills() }
