package gitforge

import (
	"context"
	"errors"
	"testing"
)

func TestUpdateBranchMergesTheBaseInWhereTheHostCan(t *testing.T) {
	ctx := context.Background()
	r := Remote{Path: "o/r"}
	route := map[string]string{"PUT /repos/o/r/pulls/7/update-branch": `{"message":"Updating"}`}
	gh, ghHost := newAPI(t, KindGitHub, route)
	if err := gh.UpdateBranch(ctx, r, 7); err != nil {
		t.Fatalf("github: %v", err)
	}
	if len(ghHost.seen) != 1 {
		t.Errorf("github asked %v", ghHost.seen)
	}
	gt, _ := newAPI(t, KindGitea, map[string]string{"POST /repos/o/r/pulls/7/update": ``})
	if err := gt.UpdateBranch(ctx, r, 7); err != nil {
		t.Fatalf("gitea: %v", err)
	}
	for _, kind := range []Kind{KindGitLab, KindBitbucket} {
		a, _ := newAPI(t, kind, nil)
		if err := a.UpdateBranch(ctx, r, 7); !errors.Is(err, ErrNoBranchUpdate) {
			t.Errorf("%s only rebases, which force-pushes: %v", kind, err)
		}
	}
	refused, _ := newAPI(t, KindGitHub, map[string]string{})
	if err := refused.UpdateBranch(ctx, r, 7); err == nil {
		t.Error("a refusal of the host is an error")
	}
}
