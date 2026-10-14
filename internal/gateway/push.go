package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/i18n"
	"github.com/madkoding/motita/internal/webpush"
)

// PHONE NOTIFICATIONS, through the browsers' own Web Push.
//
// The desktop notification is shown by the page, so it needs the page open. A phone closes the
// page as soon as it is out of sight, so here it is the GATEWAY that says a CI ended: the browser
// subscribes once (from the settings), the gateway keeps the subscription, and every step of a
// pull request's watch that the toast tells is also pushed to every subscribed browser. The
// service worker shows it, and a tap opens the session.
//
// It needs a page on a secure address (https or localhost), like every notification; on an
// iPhone, the page installed on the home screen as well.

// pushTimeout bounds one request to a push service, so a slow one does not hold the next.
const pushTimeout = 15 * time.Second

// openPush loads the gateway's VAPID key and the subscriptions when a directory is configured. A
// failure is NOT fatal: the gateway serves, and the settings say the phone notifications are off.
func (s *Server) openPush() {
	dir := strings.TrimSpace(s.opts.PushDir)
	if dir == "" {
		return
	}
	keys, err := webpush.LoadKeys(filepath.Join(dir, "vapid.key"))
	var st *webpush.Store
	if err == nil {
		st, err = webpush.OpenStore(filepath.Join(dir, "subscriptions.json"))
	}
	if err != nil {
		s.warn("could not open the push store; phone notifications are off", "error", err.Error())
		return
	}
	s.push = &webpush.Sender{Keys: keys, Store: st, Client: &http.Client{Timeout: pushTimeout}, Now: time.Now}
}

// pushView is what GET /v1/push answers.
type pushView struct {
	// Available is false on a gateway with no place to keep the subscriptions.
	Available bool `json:"available"`
	// PublicKey is the VAPID key the browser subscribes with.
	PublicKey string `json:"public_key,omitempty"`
	// Subscribed says whether the endpoint in the query is one the gateway sends to: a browser
	// can hold a subscription the gateway has forgotten, and then it subscribes again.
	Subscribed bool `json:"subscribed"`
}

func (s *Server) handleGetPush(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeJSON(w, http.StatusOK, pushView{})
		return
	}
	v := pushView{Available: true, PublicKey: s.push.Keys.Public()}
	_, v.Subscribed = s.pushSubscription(r.URL.Query().Get("endpoint"))
	writeJSON(w, http.StatusOK, v)
}

// pushSubscription finds the saved subscription with this endpoint.
func (s *Server) pushSubscription(endpoint string) (webpush.Subscription, bool) {
	for _, sub := range s.push.Store.List() {
		if endpoint != "" && sub.Endpoint == endpoint {
			return sub, true
		}
	}
	return webpush.Subscription{}, false
}

// pushReady writes the answer for a gateway without push, and reports whether it has it.
func (s *Server) pushReady(w http.ResponseWriter) bool {
	if s.push == nil {
		writeError(w, http.StatusServiceUnavailable, "this gateway has no place to keep phone notifications")
		return false
	}
	return true
}

func (s *Server) handleAddPush(w http.ResponseWriter, r *http.Request) {
	if !s.pushReady(w) {
		return
	}
	var sub webpush.Subscription
	if !s.decodeBody(w, r, &sub) {
		return
	}
	if err := s.push.Store.Add(sub); errors.Is(err, webpush.ErrInvalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pushEndpoint is the body of the endpoints that name one subscription.
type pushEndpoint struct {
	Endpoint string `json:"endpoint"`
}

func (s *Server) handleRemovePush(w http.ResponseWriter, r *http.Request) {
	if !s.pushReady(w) {
		return
	}
	var body pushEndpoint
	if !s.decodeBody(w, r, &body) {
		return
	}
	if _, err := s.push.Store.Remove(body.Endpoint); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestPush sends one notification to the browser that asks, so the person sees the whole
// path work: gateway, push service, phone.
func (s *Server) handleTestPush(w http.ResponseWriter, r *http.Request) {
	if !s.pushReady(w) {
		return
	}
	var body pushEndpoint
	if !s.decodeBody(w, r, &body) {
		return
	}
	sub, ok := s.pushSubscription(body.Endpoint)
	if !ok {
		writeError(w, http.StatusNotFound, "this browser is not subscribed to phone notifications")
		return
	}
	lang := i18n.Lang(sub.Lang)
	err := s.push.SendTo(r.Context(), sub, webpush.Message{
		Title: i18n.T(lang, "Motita notifications work"),
		Body:  i18n.T(lang, "This is how a CI that ends will look."),
		Tag:   "motita-test",
		URL:   "/",
	})
	if errors.Is(err, webpush.ErrGone) {
		_, _ = s.push.Store.Remove(sub.Endpoint)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// prPushNotice is the step of a watch worth a notification, or "" for none: the same steps the
// page's toast tells (prNotice in web/src/prApi.ts). Sending the agent again is news at every new
// attempt; anything else only when the status changes.
func prPushNotice(prev, cur *PRWatchView) string {
	if cur == nil || cur.Status == prFollowing {
		return ""
	}
	var was PRWatchView
	if prev != nil {
		was = *prev
	}
	switch cur.Status {
	case prFixing, prReviewing, prResolving:
		if was.Status != cur.Status || cur.Attempts > was.Attempts {
			return cur.Status
		}
		return ""
	default:
		if was.Status == cur.Status {
			return ""
		}
		return cur.Status
	}
}

// pushPR tells every subscribed browser that the watch of c moved from prev to cur, when that is
// news. It does not wait for the push services, and the sends end when the gateway closes.
func (s *Server) pushPR(c *conversation, prev, cur *PRWatchView) {
	notice := prPushNotice(prev, cur)
	if s.push == nil || notice == "" {
		return
	}
	c.stateMu.Lock()
	title := c.title
	c.stateMu.Unlock()
	if isPlaceholderTitle(title) {
		title = ""
	}
	v := *cur
	s.pushWG.Go(func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, pushTimeout)
		defer cancel()
		if _, err := s.push.Send(ctx, func(lang string) webpush.Message {
			return prMessage(i18n.Lang(lang), notice, &v, title, c.id)
		}); err != nil {
			s.warn("could not send a phone notification", "session", c.id, "error", err.Error())
		}
	})
}

// prMessage is the notification for a step of the watch, in lang. Its title and text are the
// toast's, and a tap opens the session.
func prMessage(lang i18n.Lang, notice string, v *PRWatchView, title, session string) webpush.Message {
	pr := ""
	if v.Number > 0 {
		pr = fmt.Sprintf("#%d", v.Number)
	}
	var head, body string
	switch notice {
	case prPassed:
		head, body = "CI passed", i18n.Tf(lang, "Pull request %s is green: you can go and merge it.", pr)
	case prFixing:
		head, body = "CI failed", i18n.Tf(lang, "Motita is fixing it — attempt %d of %d.", v.Attempts, v.Max)
	case prReviewing:
		head = "New review comments"
		body = i18n.Tf(lang, "Motita is answering the comments on pull request %s — attempt %d of %d.",
			pr, v.Attempts, v.Max)
	case prResolving:
		head = "Merge conflict"
		body = i18n.Tf(lang, "Pull request %s conflicts with its base branch: Motita is resolving it — attempt %d of %d.",
			pr, v.Attempts, v.Max)
	case prGaveUp:
		head = "Motita could not fix the CI"
		body = i18n.Tf(lang, "It tried %d times. Look at pull request %s and try again when you are ready.", v.Attempts, pr)
	case prNoCI:
		head, body = "This repository has no CI", i18n.Tf(lang, "Pull request %s has no checks to wait for.", pr)
	case prBaseRed:
		head = "The CI is red on the base branch too"
		body = i18n.Tf(lang,
			"Pull request %s fails the same checks as the branch it merges into: this change did not cause it.", pr)
	case prClosed:
		head, body = "Pull request closed", i18n.Tf(lang, "Pull request %s was closed without being merged.", pr)
	default: // prMerged
		head, body = "Pull request merged", i18n.Tf(lang, "Pull request %s was merged.", pr)
	}
	head = i18n.T(lang, head)
	if title != "" {
		head += " — " + title
	}
	return webpush.Message{
		Title: head,
		Body:  body,
		// One notification per session: the next step replaces the last instead of stacking.
		Tag: "motita-pr-" + session,
		URL: "/?session=" + url.QueryEscape(session),
	}
}
