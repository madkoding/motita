package gitforge

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// What people said on a pull request is the other half of following it: a CI can be green while a
// reviewer asks for a change. Each host keeps it in its own shape, and Activity folds them all into
// one list of comments with an identity that does not repeat, so a watcher can tell the new ones
// from the ones it has seen.

// Kinds of comment.
const (
	CommentGeneral = "comment" // a comment on the pull request as a whole
	CommentReview  = "review"  // a review with a verdict or a summary
	CommentLine    = "line"    // a comment on a line of the diff
)

// commentBodyMax is how much of a comment is kept: enough to act on, not a novel in a tool's output.
const commentBodyMax = 2000

// Comment is one thing a person said on a pull request.
type Comment struct {
	// ID is unique within the pull request across kinds: a review and a comment may share a number
	// on the host, so the kind is part of it.
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Author string `json:"author,omitempty"`
	Body   string `json:"body"`
	URL    string `json:"url,omitempty"`
	// State is a review's verdict ("changes_requested", "commented"); empty for the other kinds.
	State string `json:"state,omitempty"`
	// Path is the file a line comment is on.
	Path string `json:"path,omitempty"`
}

// Activity is what was said on a pull request, as far as the host shows it in one page per kind.
type Activity struct {
	Comments []Comment `json:"comments"`
}

// Activity reads the comments and reviews of pull request n. Approvals and the host's own notes
// (a push, a label) are left out: they ask nothing of anyone.
func (a API) Activity(ctx context.Context, r Remote, n int) (Activity, error) {
	switch a.Service.Kind {
	case KindGitLab:
		return a.gitLabActivity(ctx, r, n)
	case KindBitbucket:
		return a.bitbucketActivity(ctx, r, n)
	default:
		return a.pullsActivity(ctx, r, n)
	}
}

func newComment(kind string, id int64, author, body, url string) Comment {
	body = strings.TrimSpace(body)
	if len(body) > commentBodyMax {
		body = body[:commentBodyMax] + "…"
	}
	return Comment{ID: kind + ":" + strconv.FormatInt(id, 10), Kind: kind, Author: author, Body: body, URL: url}
}

// ghUser is the author of a comment on GitHub and Gitea.
type ghUser struct {
	Login string `json:"login"`
}

// pullsActivity reads the comments and reviews GitHub and Gitea keep under the same paths. Gitea's
// line comments hang from each review and are not read: the review that carries them is.
func (a API) pullsActivity(ctx context.Context, r Remote, n int) (Activity, error) {
	base := fmt.Sprintf("%s/repos/%s", a.Service.APIBase, r.Path)
	var general []struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		User    ghUser `json:"user"`
	}
	if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues/%d/comments?per_page=100", base, n), nil, &general); err != nil {
		return Activity{}, err
	}
	var out Activity
	for _, c := range general {
		out.Comments = append(out.Comments, newComment(CommentGeneral, c.ID, c.User.Login, c.Body, c.HTMLURL))
	}
	var reviews []struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		User    ghUser `json:"user"`
	}
	if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", base, n), nil, &reviews); err != nil {
		return Activity{}, err
	}
	for _, rv := range reviews {
		state := reviewState(rv.State)
		// An approval asks for nothing, and an empty "commented" review is only the envelope of
		// its line comments, which are read on their own.
		if state == "" || (state == "commented" && strings.TrimSpace(rv.Body) == "") {
			continue
		}
		c := newComment(CommentReview, rv.ID, rv.User.Login, rv.Body, rv.HTMLURL)
		c.State = state
		out.Comments = append(out.Comments, c)
	}
	if a.Service.Kind != KindGitHub {
		return out, nil
	}
	var lines []struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		Path    string `json:"path"`
		HTMLURL string `json:"html_url"`
		User    ghUser `json:"user"`
	}
	endpoint := fmt.Sprintf("%s/pulls/%d/comments?per_page=100&sort=created&direction=desc", base, n)
	if err := a.do(ctx, http.MethodGet, endpoint, nil, &lines); err != nil {
		return Activity{}, err
	}
	for _, l := range lines {
		c := newComment(CommentLine, l.ID, l.User.Login, l.Body, l.HTMLURL)
		c.Path = l.Path
		out.Comments = append(out.Comments, c)
	}
	return out, nil
}

// reviewState folds GitHub's and Gitea's review verdicts into the two that ask for something, and
// "" for the rest (an approval, a dismissed or pending review).
func reviewState(s string) string {
	switch strings.ToUpper(s) {
	case "CHANGES_REQUESTED", "REQUEST_CHANGES":
		return "changes_requested"
	case "COMMENTED", "COMMENT":
		return "commented"
	default:
		return ""
	}
}

// gitLabActivity reads a merge request's notes, newest first, without the ones GitLab writes itself.
func (a API) gitLabActivity(ctx context.Context, r Remote, n int) (Activity, error) {
	var notes []struct {
		ID     int64  `json:"id"`
		Body   string `json:"body"`
		System bool   `json:"system"`
		Type   string `json:"type"`
		Author struct {
			Username string `json:"username"`
		} `json:"author"`
		Position *struct {
			NewPath string `json:"new_path"`
		} `json:"position"`
	}
	endpoint := fmt.Sprintf("%s/merge_requests/%d/notes?sort=desc&order_by=created_at&per_page=100",
		a.gitLabProject(r), n)
	if err := a.do(ctx, http.MethodGet, endpoint, nil, &notes); err != nil {
		return Activity{}, err
	}
	var out Activity
	for _, note := range notes {
		if note.System {
			continue
		}
		kind := CommentGeneral
		if note.Type == "DiffNote" {
			kind = CommentLine
		}
		c := newComment(kind, note.ID, note.Author.Username, note.Body, "")
		if note.Position != nil {
			c.Path = note.Position.NewPath
		}
		out.Comments = append(out.Comments, c)
	}
	return out, nil
}

// bitbucketActivity reads a pull request's comments, newest first, without the deleted ones.
func (a API) bitbucketActivity(ctx context.Context, r Remote, n int) (Activity, error) {
	var page struct {
		Values []struct {
			ID      int64 `json:"id"`
			Deleted bool  `json:"deleted"`
			Content struct {
				Raw string `json:"raw"`
			} `json:"content"`
			User struct {
				Nickname string `json:"nickname"`
			} `json:"user"`
			Inline *struct {
				Path string `json:"path"`
			} `json:"inline"`
			Links struct {
				HTML struct {
					Href string `json:"href"`
				} `json:"html"`
			} `json:"links"`
		} `json:"values"`
	}
	if err := a.do(ctx, http.MethodGet, a.bitbucketPRPath(r, n)+"/comments?pagelen=100&sort=-created_on", nil, &page); err != nil {
		return Activity{}, err
	}
	var out Activity
	for _, v := range page.Values {
		if v.Deleted {
			continue
		}
		kind := CommentGeneral
		if v.Inline != nil {
			kind = CommentLine
		}
		c := newComment(kind, v.ID, v.User.Nickname, v.Content.Raw, v.Links.HTML.Href)
		if v.Inline != nil {
			c.Path = v.Inline.Path
		}
		out.Comments = append(out.Comments, c)
	}
	return out, nil
}
