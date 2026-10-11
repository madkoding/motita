package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// updatingRunner is a runner that speaks to a gateway able to upgrade itself. The embedded
// fakeRunner is every other capability; these fields are the two this file is about.
type updatingRunner struct {
	*fakeRunner
	current, latest string
	available       bool
	checkErr        error
	runErr          error
	stages          []string
	installed       string
	runs            int
}

func (u *updatingRunner) UpdateAvailable(context.Context) (string, string, bool, error) {
	return u.current, u.latest, u.available, u.checkErr
}

func (u *updatingRunner) RunUpdate(_ context.Context, progress func(string)) (string, error) {
	u.runs++
	for _, s := range u.stages {
		progress(s)
	}
	return u.installed, u.runErr
}

func updateTUI(r *updatingRunner) *TUI {
	tu := newFakeTUI("", r)
	tu.Width, tu.Height = 110, 30
	return tu
}

func messagesOf(tu *TUI) string {
	var b strings.Builder
	for _, m := range tu.messages {
		b.WriteString(m.Text + "\n")
	}
	return b.String()
}

// TestANewerReleaseIsOfferedNotInstalled: the announcement names both versions and the command,
// and installs nothing - the user decides.
func TestANewerReleaseIsOfferedNotInstalled(t *testing.T) {
	r := &updatingRunner{fakeRunner: &fakeRunner{}, current: "v1.0.0", latest: "v1.1.0", available: true}
	tu := updateTUI(r)
	tu.announceUpdate(context.Background())
	for _, want := range []string{"v1.1.0", "v1.0.0", "/update"} {
		if !strings.Contains(tu.Notice, want) {
			t.Errorf("the offer %q does not mention %q", tu.Notice, want)
		}
	}
	if r.runs != 0 {
		t.Error("announcing must never install")
	}
}

func TestTheOfferKeepsTheNoticeThatWasAlreadyThere(t *testing.T) {
	r := &updatingRunner{fakeRunner: &fakeRunner{}, current: "v1", latest: "v2", available: true}
	tu := updateTUI(r)
	tu.Notice = "Setup complete."
	tu.announceUpdate(context.Background())
	if !strings.Contains(tu.Notice, "Setup complete.") || !strings.Contains(tu.Notice, "v2") {
		t.Errorf("both must survive: %q", tu.Notice)
	}
}

// TestTheOfferDoesNotPaintWhileATurnRuns: the turn's goroutine repaints after each step, so the
// announcement only records the notice and leaves the screen to it.
func TestTheOfferDoesNotPaintWhileATurnRuns(t *testing.T) {
	r := &updatingRunner{fakeRunner: &fakeRunner{}, current: "v1", latest: "v2", available: true}
	tu := updateTUI(r)
	tu.beginTurn()
	tu.announceUpdate(context.Background())
	if !strings.Contains(tu.Notice, "v2") {
		t.Errorf("the notice must still be recorded: %q", tu.Notice)
	}
}

// TestNothingIsSaidWhenThereIsNothingToSay: up to date, a failed check, and a runner that cannot
// upgrade all leave the welcome screen as it was.
func TestNothingIsSaidWhenThereIsNothingToSay(t *testing.T) {
	for name, tu := range map[string]*TUI{
		"up to date":   updateTUI(&updatingRunner{fakeRunner: &fakeRunner{}, current: "v1", latest: "v1"}),
		"check failed": updateTUI(&updatingRunner{fakeRunner: &fakeRunner{}, checkErr: errors.New("offline"), available: true}),
		"no gateway":   newFakeTUI("", &fakeRunner{}),
	} {
		tu.announceUpdate(context.Background())
		if tu.Notice != "" {
			t.Errorf("%s: the notice must stay empty, got %q", name, tu.Notice)
		}
	}
}

// TestRunAnnouncesTheOfferBesideTheLoop drives the real Run: the check happens, the notice is
// set, and Run still exits cleanly.
func TestRunAnnouncesTheOfferBesideTheLoop(t *testing.T) {
	r := &updatingRunner{fakeRunner: &fakeRunner{}, current: "v1.0.0", latest: "v1.1.0", available: true}
	tu := newFakeTUI("q\n", r)
	if code := tu.Run(context.Background()); code != ExitSuccess {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(tu.Notice, "v1.1.0") {
		t.Errorf("Run never offered the upgrade: %q", tu.Notice)
	}
}

func TestUpdateInstallsAndTellsTheUserWhatToDoNext(t *testing.T) {
	r := &updatingRunner{
		fakeRunner: &fakeRunner{}, current: "v1.0.0", latest: "v1.1.0", available: true,
		stages: []string{"Downloading v1.1.0", "  ", "Verifying checksum"}, installed: "v1.1.0",
	}
	tu := updateTUI(r)
	tu.Notice = "motita v1.1.0 is available"
	tu.runUpdate(context.Background())
	out := messagesOf(tu)
	for _, want := range []string{"installing v1.1.0", "Downloading v1.1.0", "Verifying checksum", "v1.1.0 is installed", "/quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("the conversation lacks %q:\n%s", want, out)
		}
	}
	if tu.Notice != "" {
		t.Errorf("the offer was taken, so it must go: %q", tu.Notice)
	}
}

func TestUpdateFailureIsReportedAsFailure(t *testing.T) {
	r := &updatingRunner{fakeRunner: &fakeRunner{}, current: "v1", latest: "v2", available: true, runErr: errors.New("checksum mismatch")}
	tu := updateTUI(r)
	tu.runUpdate(context.Background())
	out := messagesOf(tu)
	if !strings.Contains(out, "update failed") || !strings.Contains(out, "checksum mismatch") {
		t.Errorf("the failure was not reported:\n%s", out)
	}
	if strings.Contains(out, "is installed") {
		t.Error("a failed upgrade must not claim success")
	}
}

func TestUpdateWhenThereIsNothingToInstall(t *testing.T) {
	r := &updatingRunner{fakeRunner: &fakeRunner{}, current: "v1.0.0", latest: "v1.0.0"}
	tu := updateTUI(r)
	tu.runUpdate(context.Background())
	if !strings.Contains(messagesOf(tu), "newest release") || r.runs != 0 {
		t.Errorf("up to date must not install: runs=%d\n%s", r.runs, messagesOf(tu))
	}
}

func TestUpdateWhenTheCheckFails(t *testing.T) {
	r := &updatingRunner{fakeRunner: &fakeRunner{}, checkErr: errors.New("offline")}
	tu := updateTUI(r)
	tu.runUpdate(context.Background())
	if !strings.Contains(messagesOf(tu), "could not check") || r.runs != 0 {
		t.Errorf("a failed check must say so and install nothing:\n%s", messagesOf(tu))
	}
}

func TestUpdateWithoutAGatewayExplainsWhy(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	tu.runUpdate(context.Background())
	if !strings.Contains(messagesOf(tu), "not attached to a gateway") {
		t.Errorf("got %q", messagesOf(tu))
	}
}

// TestUpdateWaitsForTheTurnToEnd: the gateway restarts, and a turn in flight would be cut.
func TestUpdateWaitsForTheTurnToEnd(t *testing.T) {
	if !busyCommands["/update"] {
		t.Fatal("/update must be refused while a turn is running")
	}
}
