package gitforge

import (
	"context"
	"strings"
	"testing"
)

func TestActivityGitHub(t *testing.T) {
	a, h := newAPI(t, KindGitHub, map[string]string{
		"GET /repos/o/r/issues/5/comments": `[{"id":1,"body":" please rename ","html_url":"u1","user":{"login":"ana"}}]`,
		"GET /repos/o/r/pulls/5/reviews": `[{"id":1,"body":"","state":"APPROVED","user":{"login":"bo"}},` +
			`{"id":2,"body":"","state":"COMMENTED"},{"id":3,"body":"fix the test","state":"CHANGES_REQUESTED",` +
			`"html_url":"u3","user":{"login":"bo"}},{"id":4,"body":"nit","state":"COMMENTED"},{"id":5,"state":"DISMISSED"}]`,
		"GET /repos/o/r/pulls/5/comments": `[{"id":9,"body":"typo","path":"a.go","html_url":"u9","user":{"login":"cy"}}]`,
	})
	act, err := a.Activity(context.Background(), Remote{Path: "o/r"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []Comment{
		{ID: "comment:1", Kind: CommentGeneral, Author: "ana", Body: "please rename", URL: "u1"},
		{ID: "review:3", Kind: CommentReview, Author: "bo", Body: "fix the test", URL: "u3", State: "changes_requested"},
		{ID: "review:4", Kind: CommentReview, Body: "nit", State: "commented"},
		{ID: "line:9", Kind: CommentLine, Author: "cy", Body: "typo", URL: "u9", Path: "a.go"},
	}
	if len(act.Comments) != len(want) {
		t.Fatalf("comments = %+v", act.Comments)
	}
	for i := range want {
		if act.Comments[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, act.Comments[i], want[i])
		}
	}
	if !strings.Contains(strings.Join(h.seen, " "), "direction=desc") {
		t.Errorf("line comments are read newest first: %v", h.seen)
	}
}

func TestActivityGiteaReadsNoLineComments(t *testing.T) {
	a, h := newAPI(t, KindGitea, map[string]string{
		"GET /repos/o/r/issues/5/comments": `[]`,
		"GET /repos/o/r/pulls/5/reviews":   `[{"id":7,"body":"no","state":"REQUEST_CHANGES","user":{"login":"d"}}]`,
	})
	act, err := a.Activity(context.Background(), Remote{Path: "o/r"}, 5)
	if err != nil || len(act.Comments) != 1 || act.Comments[0].State != "changes_requested" {
		t.Fatalf("%+v %v", act, err)
	}
	if len(h.seen) != 2 {
		t.Errorf("gitea asked %v", h.seen)
	}
}

func TestActivityGitLab(t *testing.T) {
	a, _ := newAPI(t, KindGitLab, map[string]string{
		"GET /projects/o%2Fr/merge_requests/5/notes": `[{"id":1,"body":"added 1 commit","system":true},` +
			`{"id":2,"body":"why?","type":"DiffNote","author":{"username":"e"},"position":{"new_path":"b.go"}},` +
			`{"id":3,"body":"ok","author":{"username":"f"}}]`,
	})
	act, err := a.Activity(context.Background(), Remote{Path: "o/r"}, 5)
	if err != nil || len(act.Comments) != 2 {
		t.Fatalf("%+v %v", act, err)
	}
	if c := act.Comments[0]; c.Kind != CommentLine || c.Path != "b.go" || c.Author != "e" || c.ID != "line:2" {
		t.Errorf("diff note = %+v", c)
	}
	if c := act.Comments[1]; c.Kind != CommentGeneral || c.Path != "" {
		t.Errorf("note = %+v", c)
	}
}

func TestActivityBitbucket(t *testing.T) {
	long := strings.Repeat("x", commentBodyMax+10)
	a, _ := newAPI(t, KindBitbucket, map[string]string{
		"GET /repositories/o/r/pullrequests/5/comments": `{"values":[{"id":1,"deleted":true},` +
			`{"id":2,"content":{"raw":"here"},"user":{"nickname":"g"},"inline":{"path":"c.go"},` +
			`"links":{"html":{"href":"u2"}}},{"id":3,"content":{"raw":"` + long + `"}}]}`,
	})
	act, err := a.Activity(context.Background(), Remote{Path: "o/r"}, 5)
	if err != nil || len(act.Comments) != 2 {
		t.Fatalf("%+v %v", act, err)
	}
	if c := act.Comments[0]; c.Kind != CommentLine || c.Path != "c.go" || c.URL != "u2" || c.Author != "g" {
		t.Errorf("inline = %+v", c)
	}
	if c := act.Comments[1]; c.Kind != CommentGeneral || len(c.Body) != commentBodyMax+len("…") {
		t.Errorf("a long comment is cut: %d", len(c.Body))
	}
}

func TestActivityFailures(t *testing.T) {
	ctx := context.Background()
	r := Remote{Path: "o/r"}
	for _, kind := range []Kind{KindGitHub, KindGitLab, KindBitbucket} {
		a, _ := newAPI(t, kind, nil)
		if _, err := a.Activity(ctx, r, 5); err == nil {
			t.Errorf("%s: a refusal must surface", kind)
		}
	}
	a, _ := newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/issues/5/comments": `[]`})
	if _, err := a.Activity(ctx, r, 5); err == nil {
		t.Error("unreadable reviews must surface")
	}
	a, _ = newAPI(t, KindGitHub, map[string]string{
		"GET /repos/o/r/issues/5/comments": `[]`, "GET /repos/o/r/pulls/5/reviews": `[]`,
	})
	if _, err := a.Activity(ctx, r, 5); err == nil {
		t.Error("unreadable line comments must surface")
	}
}
