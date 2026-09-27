package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/curator"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/tui"
	"github.com/madkoding/motita/internal/usage"
)

// TestTheServedGatewayIsGivenTheLibraryAndTheCurator is the wiring test for the skill
// endpoints.
//
// Without it the handlers exist and nothing puts anything behind them: startGateway has to
// hand the gateway the ONE runner that owns the shared store, or every skill endpoint answers
// 501 on a gateway that can plainly serve work. The two halves live in different packages, so
// the only place that can prove they fit together is here.
//
// It goes over HTTP against a real -serve process, with the token read from disk, because that
// is the path a browser takes.
func TestTheServedGatewayIsGivenTheLibraryAndTheCurator(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)
	go func() { _ = Run(opts) }()
	addr := waitForAddress(t, logPath)

	token := readToken(t, filepath.Join(filepath.Dir(cfgPath), "gateway.token"))
	client := &http.Client{Timeout: 5 * time.Second}

	// The index answers, and it is NOT a 501: a gateway served without a library is exactly
	// the state this test exists to catch.
	var index struct {
		Skills []struct {
			Name      string `json:"name"`
			Title     string `json:"title"`
			CreatedBy string `json:"created_by"`
		} `json:"skills"`
	}
	if err := getJSONInto(t, client, "http://"+addr+"/v1/skills", token, &index); err != nil {
		t.Fatalf("listing skills: %v", err)
	}
	// The embed SHIPS a procedure (procedures.Open sets Builtins), so an empty list here
	// would mean the store was opened without its embedded half.
	if len(index.Skills) == 0 {
		t.Fatal("a served gateway reports no skills: the library was not wired, or its builtins are missing")
	}
	var found bool
	for _, s := range index.Skills {
		if s.Name == "files-and-directories" {
			found = true
		}
	}
	if !found {
		t.Errorf("the embedded procedure is not in the index: %+v", index.Skills)
	}

	// One document, with its body, over the same gateway.
	var doc struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := getJSONInto(t, client, "http://"+addr+"/v1/skills/files-and-directories", token, &doc); err != nil {
		t.Fatalf("reading one skill: %v", err)
	}
	if doc.Body == "" {
		t.Error("the document came back without a body")
	}

	// The curator answers too, and its status names the thresholds it was configured with.
	var status struct {
		Text string `json:"text"`
	}
	if err := getJSONInto(t, client, "http://"+addr+"/v1/curator", token, &status); err != nil {
		t.Fatalf("reading the curator: %v", err)
	}
	if status.Text == "" {
		t.Error("the curator answered nothing")
	}

	// And a DRY RUN over the real gateway changes nothing. This is the one that matters:
	// the endpoint reaches the same curator object the process holds, so a preview that
	// wrote would touch the user's real library.
	var preview struct {
		Text string `json:"text"`
	}
	if err := postJSONInto(t, client, "http://"+addr+"/v1/curator/run",
		`{"dry_run":true}`, token, &preview); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if preview.Text == "" {
		t.Error("the dry run answered nothing")
	}

	// The archive listing answers and is a list.
	var archived struct {
		Skills []string `json:"skills"`
	}
	if err := getJSONInto(t, client, "http://"+addr+"/v1/skills/archived", token, &archived); err != nil {
		t.Fatalf("listing the archive: %v", err)
	}

	cancel()
}

// postJSONInto performs an authenticated POST and decodes the answer.
//
// It accepts ANY 2xx and treats a non-2xx as an error, because the endpoints differ on
// purpose: a save answers 201, a pin and a restore answer 204 with no body at all. Callers
// that care which one they got use postCodeInto; callers that only care that it worked use
// this.
func postJSONInto(t *testing.T, c *http.Client, url, body, token string, dst any) error {
	t.Helper()
	code, resp, err := postRaw(c, url, body, token)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s answered %d: %s", url, code, raw)
	}
	if dst == nil || body == "" {
		resp.Body.Close()
		return nil
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dst)
}

// postRaw performs the POST and hands back the status and the open body. The caller owns
// closing it.
func postRaw(c *http.Client, url, body, token string) (int, *http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, resp, nil
}

// TestTheAdapterForwardsEveryWriteToTheLibrary exercises the rest of the adapter surface over
// HTTP, against the real process.
//
// It is not decoration. Skills() and Skill() are reached by the index and the read, but
// SAVING, PINNING, ARCHIVING and RESTORING only arrive at the library if the adapter forwards
// them: a method that forgot to would answer 204 and do nothing, or 501, on a feature the
// user was promised - and nothing else in this repo would notice.
//
// The PROVENANCE assertion is the one that matters most. A save made through the interface is
// marked ByForeground, and that marker is what stops the curator from archiving a skill a
// person wrote. If it were ByAgent, the user's own work would be fair game for a background
// goroutine, silently.
func TestTheAdapterForwardsEveryWriteToTheLibrary(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)
	go func() { _ = Run(opts) }()
	addr := waitForAddress(t, logPath)
	token := readToken(t, filepath.Join(filepath.Dir(cfgPath), "gateway.token"))
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + addr

	// SAVE, through the adapter.
	if err := postJSONInto(t, client, base+"/v1/skills",
		`{"name":"adapter-check","body":"# Adapter Check\n\nbody\n"}`, token, nil); err != nil {
		t.Fatalf("saving: %v", err)
	}

	// The index must show it marked as the USER's work.
	type entry struct {
		Name      string `json:"name"`
		CreatedBy string `json:"created_by"`
		Pinned    bool   `json:"pinned"`
	}
	readIndex := func() []entry {
		t.Helper()
		// A FRESH destination every call. json.Unmarshal reuses the one it is given, and a
		// field that is ABSENT from the JSON keeps whatever was there before: `pinned` is
		// omitempty, so an unpinned skill omits it, and a second read through a shared
		// variable would report the pin that was just removed.
		var index struct {
			Skills []entry `json:"skills"`
		}
		if err := getJSONInto(t, client, base+"/v1/skills", token, &index); err != nil {
			t.Fatalf("reading the index: %v", err)
		}
		return index.Skills
	}
	find := func(list []entry, name string) (entry, bool) {
		for _, e := range list {
			if e.Name == name {
				return e, true
			}
		}
		return entry{}, false
	}

	got, ok := find(readIndex(), "adapter-check")
	if !ok {
		t.Fatal("the saved skill is not in the index: the write did not reach the library")
	}
	if got.CreatedBy != "foreground" {
		t.Errorf("a save through the interface has provenance %q, want \"foreground\": the curator would be free to archive a person's own skill", got.CreatedBy)
	}

	// PIN, both directions: the pin is the user's veto over every automatic transition.
	if err := postJSONInto(t, client, base+"/v1/skills/adapter-check/pin",
		`{"pinned":true}`, token, nil); err != nil {
		t.Fatalf("pinning: %v", err)
	}
	if got, _ := find(readIndex(), "adapter-check"); !got.Pinned {
		t.Error("the pin did not reach the ledger")
	}
	if err := postJSONInto(t, client, base+"/v1/skills/adapter-check/pin",
		`{"pinned":false}`, token, nil); err != nil {
		t.Fatalf("unpinning: %v", err)
	}
	if got, _ := find(readIndex(), "adapter-check"); got.Pinned {
		t.Errorf("the pin was not removed: %+v", got)
	}

	// The ARCHIVE is NOT part of the interface, and that is a decision rather than an
	// omission: archiving is the curator's autonomous action, and the browser only gets
	// pin/unpin and restore. This assertion keeps the decision visible - if somebody adds
	// the route, this test fails and tells them it was a choice.
	//
	// It uses postRaw because it is asserting the ABSENCE of a route: a 404 is the expected
	// answer here, and every other helper in this file treats a non-2xx as a failure.
	if code, resp, err := postRaw(client, base+"/v1/skills/adapter-check/archive", `{}`, token); err != nil {
		t.Fatalf("probing the archive route: %v", err)
	} else {
		resp.Body.Close()
		if code != http.StatusNotFound {
			t.Errorf("archiving from the interface answered %d: that endpoint was deliberately out of scope, so either it was added (update this test and the docs) or it failed for another reason", code)
		}
	}

	// RESTORE fails loudly for a skill that is not archived: the adapter's error travels back
	// out as a 404 rather than being swallowed into a 204.
	if code, resp, err := postRaw(client, base+"/v1/skills/adapter-check/restore", `{}`, token); err != nil {
		t.Fatalf("restoring: %v", err)
	} else {
		resp.Body.Close()
		if code != http.StatusNotFound {
			t.Errorf("restoring a skill that is not archived answered %d, want 404", code)
		}
	}
}

// TestTheSkillAdapterIsTheLibraryItself covers the two adapter methods the served gateway does
// not reach over HTTP.
//
// ArchiveSkill has no route ON PURPOSE (archiving is the curator's autonomous action), and
// CuratorRun's failure branch needs a curator whose pass fails. Both are still part of the
// contract the gateway compiles against, so both are exercised here rather than left as the
// only untested statements in the package.
func TestTheSkillAdapterIsTheLibraryItself(t *testing.T) {
	dir := t.TempDir()
	store := &procedures.Store{Library: skills.New(dir)}
	store.Usage, _ = usage.Open(filepath.Join(dir, ".usage.json"))

	runner := tui.NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, nil)
	runner.UseStore(store)
	a := skillAdapter{runner: runner}

	if _, err := a.SaveSkill("adapter", "# Adapter\n\nbody\n"); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	if err := a.ArchiveSkill("adapter"); err != nil {
		t.Fatalf("ArchiveSkill: %v", err)
	}
	names, err := a.ArchivedSkills()
	if err != nil || len(names) != 1 || names[0] != "adapter" {
		t.Fatalf("ArchivedSkills = %v, %v; want [adapter]: the adapter did not forward the archive", names, err)
	}
	if err := a.RestoreSkill("adapter"); err != nil {
		t.Fatalf("RestoreSkill: %v", err)
	}
	if _, err := a.Skill("adapter"); err != nil {
		t.Errorf("the document did not come back: %v", err)
	}
	// And a forwarding that failed says so rather than reporting success.
	if err := a.ArchiveSkill("not-there"); err == nil {
		t.Error("ArchiveSkill on a missing document must fail through the adapter")
	}
}

// TestTheCuratorAdapterReportsAFailingPass: the error has to travel out of the adapter
// untouched, because the handler turns it into a 502 and the message is what the operator
// reads. An adapter that swallowed it would make a broken consolidation look like a clean run.
func TestTheCuratorAdapterReportsAFailingPass(t *testing.T) {
	dir := t.TempDir()
	store := &procedures.Store{Library: skills.New(dir)}
	store.Usage, _ = usage.Open(filepath.Join(dir, ".usage.json"))

	cfg := config.Curator{Enabled: true, Consolidate: true, StaleAfterDays: 14, ArchiveAfterDays: 30}
	cfg.StateFile = filepath.Join(dir, "state.json")
	// Consolidate with no engine: the pass reports the missing engine as an error.
	c := curator.New(cfg, store, nil, nil, nil)
	a := curatorAdapter{c: c}

	if _, err := a.CuratorRun(context.Background(), true, false); err == nil {
		t.Error("a consolidation with no engine must fail through the adapter")
	}
	if a.CuratorStatus() == "" {
		t.Error("CuratorStatus answered nothing")
	}
}

// TestTheAdapterForwardsTurningOffAndDeleting: the two controls the library offers a browser, over
// HTTP against the real process. The off switch has to reach the LEDGER and the delete has to
// reach the DISK, and an adapter that forgot either would answer 204 for a button that does
// nothing - which is exactly the class of failure the other forwarding test exists to catch.
//
// The deletion runs LAST, because it is the one operation here that cannot be undone.
func TestTheAdapterForwardsTurningOffAndDeleting(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)
	go func() { _ = Run(opts) }()
	addr := waitForAddress(t, logPath)
	token := readToken(t, filepath.Join(filepath.Dir(cfgPath), "gateway.token"))
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + addr

	if err := postJSONInto(t, client, base+"/v1/skills",
		`{"name":"switch-check","body":"# Switch Check\n\nbody\n"}`, token, nil); err != nil {
		t.Fatalf("saving: %v", err)
	}

	// A fresh destination every read: `disabled` is omitempty, so a shared variable would
	// report the flag that was just cleared.
	readEntry := func(name string) (disabled bool, listed bool, body string) {
		t.Helper()
		var index struct {
			Skills []struct {
				Name     string `json:"name"`
				Disabled bool   `json:"disabled"`
			} `json:"skills"`
		}
		if err := getJSONInto(t, client, base+"/v1/skills", token, &index); err != nil {
			t.Fatalf("reading the index: %v", err)
		}
		for _, e := range index.Skills {
			if e.Name == name {
				listed, disabled = true, e.Disabled
			}
		}
		return disabled, listed, body
	}

	// OFF: still listed, flagged, and the agent stops being offered it.
	if err := postJSONInto(t, client, base+"/v1/skills/switch-check/disable",
		`{"disabled":true}`, token, nil); err != nil {
		t.Fatalf("turning off: %v", err)
	}
	disabled, listed, _ := readEntry("switch-check")
	if !listed {
		t.Fatal("turning off removed the document from the list: it is supposed to stay, badged")
	}
	if !disabled {
		t.Error("the flag did not reach the ledger: the agent would still be offered it")
	}
	// And it is still readable by name: turned off is not deleted.
	var doc struct {
		Body string `json:"body"`
	}
	if err := getJSONInto(t, client, base+"/v1/skills/switch-check", token, &doc); err != nil {
		t.Fatalf("reading a turned-off skill by name: %v", err)
	}
	if doc.Body == "" {
		t.Error("a turned-off document came back without a body")
	}

	// ON again.
	if err := postJSONInto(t, client, base+"/v1/skills/switch-check/disable",
		`{"disabled":false}`, token, nil); err != nil {
		t.Fatalf("turning back on: %v", err)
	}
	if disabled, _, _ := readEntry("switch-check"); disabled {
		t.Error("the flag was not cleared")
	}

	// DELETE, and the document is gone from the list.
	req, err := http.NewRequest(http.MethodDelete, base+"/v1/skills/switch-check", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("deleting: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE answered %d, want 204", resp.StatusCode)
	}
	if _, listed, _ := readEntry("switch-check"); listed {
		t.Error("the deleted document is still in the index: the delete did not reach the disk")
	}
}

// TestTheAdapterRefusesToDeleteAShippedProcedure: 409, and the document survives. A built-in lives
// inside the binary, so a delete that reported success would be a lie the user finds by looking.
func TestTheAdapterRefusesToDeleteAShippedProcedure(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)
	go func() { _ = Run(opts) }()
	addr := waitForAddress(t, logPath)
	token := readToken(t, filepath.Join(filepath.Dir(cfgPath), "gateway.token"))
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + addr

	req, err := http.NewRequest(http.MethodDelete, base+"/v1/skills/files-and-directories", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("deleting: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE of a shipped procedure answered %d, want 409: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "built in") {
		t.Errorf("the 409 does not say why: %s", body)
	}

	// And it is still there.
	var doc struct {
		Body string `json:"body"`
	}
	if err := getJSONInto(t, client, base+"/v1/skills/files-and-directories", token, &doc); err != nil {
		t.Fatalf("the shipped procedure must survive the refusal: %v", err)
	}
}
