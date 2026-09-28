package gateway

// scopeProceduresTo, and the two cases it exists to tell apart.
//
// It is an OPTIONAL interface: the many small services the tests build (and any embedder that
// does not care about procedures) have no library to scope, and the type assertion is what lets
// them stay that way instead of growing a no-op method. Both outcomes are asserted here, because
// a helper that only ever runs its happy path is a helper nobody knows the failure behaviour of.

import (
	"testing"
)

// scopingService is a Service that can carry a project scope, which is what the real AppRunner
// does. It records what it was told, so the test asserts on the call rather than on a guess.
type scopingService struct {
	*fakeService
	scoped []string
}

func (s *scopingService) SetProjectScope(dir string) { s.scoped = append(s.scoped, dir) }

// TestAServiceThatCanBeScopedIsScoped: a session inside a project must reach the procedures of
// that project, and the project's own checkout is what it is pointed at — never a session
// worktree, which is removed when the session ends.
func TestAServiceThatCanBeScopedIsScoped(t *testing.T) {
	svc := &scopingService{fakeService: &fakeService{}}

	scopeProceduresTo(svc, "/projects/alpha")

	if len(svc.scoped) != 1 || svc.scoped[0] != "/projects/alpha" {
		t.Errorf("the project's checkout must be passed through once, got %v", svc.scoped)
	}
}

// TestAServiceThatCannotBeScopedIsLeftAlone: the type assertion fails, nothing panics, and the
// service keeps the shared shelf — the behaviour that existed before scoping and is never
// wrong, only less specific.
func TestAServiceThatCannotBeScopedIsLeftAlone(t *testing.T) {
	inner := &fakeService{}

	// fakeService satisfies Service and deliberately does NOT implement SetProjectScope.
	scopeProceduresTo(inner, "/projects/alpha")

	// Reaching here without a panic IS the assertion; the explicit check below pins that the
	// helper did not somehow obtain a scoper through an embedded field.
	if _, ok := interface{}(inner).(interface{ SetProjectScope(string) }); ok {
		t.Fatal("fakeService must not implement SetProjectScope, or this test asserts nothing")
	}
}
