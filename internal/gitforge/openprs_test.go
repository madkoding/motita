package gitforge

import (
	"context"
	"testing"
)

func TestOpenPRsFromKeepsTheBranchesWithThePrefix(t *testing.T) {
	r := Remote{Path: "o/r"}
	cases := map[string]struct {
		kind   Kind
		routes map[string]string
	}{
		"github": {KindGitHub, map[string]string{"GET /repos/o/r/pulls?state=open": `[
			{"number":1,"html_url":"u1","title":"a","head":{"ref":"motita/auto-1"},"base":{"ref":"main"}},
			{"number":2,"html_url":"u2","title":"b","head":{"ref":"feature"},"base":{"ref":"main"}}]`}},
		"gitea": {KindGitea, map[string]string{"GET /repos/o/r/pulls?state=open": `[
			{"number":1,"html_url":"u1","title":"a","head":{"ref":"motita/auto-1"},"base":{"ref":"main"}},
			{"number":2,"html_url":"u2","title":"b","head":{"ref":"feature"},"base":{"ref":"main"}}]`}},
		"gitlab": {KindGitLab, map[string]string{"GET /projects/o%2Fr/merge_requests": `[
			{"iid":1,"web_url":"u1","title":"a","source_branch":"motita/auto-1","target_branch":"main"},
			{"iid":2,"web_url":"u2","title":"b","source_branch":"feature","target_branch":"main"}]`}},
		"bitbucket": {KindBitbucket, map[string]string{"GET /repositories/o/r/pullrequests": `{"values":[
			{"id":1,"title":"a","source":{"branch":{"name":"motita/auto-1"}},"links":{"html":{"href":"u1"}}},
			{"id":2,"title":"b","source":{"branch":{"name":"feature"}},"links":{"html":{"href":"u2"}}}]}`}},
	}
	for name, c := range cases {
		a, _ := newAPI(t, c.kind, c.routes)
		got, err := a.OpenPRsFrom(context.Background(), r, `motita/auto-`)
		if err != nil || len(got) != 1 || got[0].Number != 1 || got[0].URL != "u1" || got[0].Head != "motita/auto-1" {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
	// A quote in the prefix never reaches Bitbucket's query language.
	a, h := newAPI(t, KindBitbucket, map[string]string{"GET /repositories/o/r/pullrequests": `{"values":[]}`})
	if _, err := a.OpenPRsFrom(context.Background(), r, `x"y`); err != nil || len(h.seen) != 1 {
		t.Fatalf("%v %v", err, h.seen)
	}
}

func TestOpenPRsFromSurfacesHostErrors(t *testing.T) {
	for _, kind := range []Kind{KindGitHub, KindGitLab, KindBitbucket} {
		a, _ := newAPI(t, kind, nil)
		if _, err := a.OpenPRsFrom(context.Background(), Remote{Path: "o/r"}, "motita/auto-"); err == nil {
			t.Errorf("%s: a host error must surface", kind)
		}
	}
}
