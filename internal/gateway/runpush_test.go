package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/i18n"
)

func TestRunMessageSaysWhatTheRunNeeds(t *testing.T) {
	for _, kind := range []string{"approval", "done", "error"} {
		for _, lang := range []i18n.Lang{i18n.EN, i18n.ES} {
			m := runMessage(lang, kind, "make test", "Login page", "s 1")
			if !strings.HasSuffix(m.Title, " — Login page") || m.Body == "" || m.Tag != "motita-run-s 1" ||
				m.URL != "/?session=s+1" || strings.Contains(m.Body, "%") {
				t.Errorf("%s/%s = %+v", kind, lang, m)
			}
		}
	}
	if m := runMessage(i18n.ES, "approval", "make test", "", "s"); m.Title != "Motita necesita tu aprobación" {
		t.Errorf("es = %+v", m)
	}
}

// TestAnUnwatchedRunReachesThePhoneAndAWatchedOneDoesNot: a run with no page attached is the one
// nobody will see ask or end; one with a reader attached shows it on screen already.
func TestAnUnwatchedRunReachesThePhoneAndAWatchedOneDoesNot(t *testing.T) {
	srv, tr, _ := newPushServer(t)
	_ = pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "es"))
	c := newConversation("s1", &fakeService{})
	rn := newRun("r1", srv.baseCtx, func() {}, "")

	srv.pushUnwatched(c, rn, "done", "")
	srv.pushWG.Wait()
	if tr.count() != 1 {
		t.Fatalf("unwatched sent %d, want 1", tr.count())
	}

	sub := rn.subscribe()
	srv.pushUnwatched(c, rn, "approval", "make test")
	srv.pushWG.Wait()
	rn.unsubscribe(sub)
	if tr.count() != 1 {
		t.Fatalf("watched sent %d, want none more", tr.count())
	}

	srv.unattendedApprover("job")(rn)
	srv.pushUnwatched(c, rn, "done", "")
	srv.pushWG.Wait()
	if tr.count() != 1 || !rn.quiet {
		t.Fatalf("a scheduled run sent %d", tr.count())
	}
}
