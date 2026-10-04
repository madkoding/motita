package tui

import (
	"context"
	"strings"
	"time"
)

// updateCheckTimeout bounds the check made when the interface opens. The gateway answers from
// the result it cached, which is instant; the bound is for a gateway that has none yet and has to
// ask GitHub. An interface that waited for that would be a terminal that does not open, so a slow
// answer means "no notice this time", never a delay.
const updateCheckTimeout = 3 * time.Second

// updater is the capability of a runner that speaks to a gateway able to replace itself. It is
// declared here, structurally, for the reason every optional capability of the runner is: the
// gateway client satisfies it without this package importing it, and an embedded runner that has
// no gateway to upgrade simply does not.
type updater interface {
	// UpdateAvailable reports the running version, the newest release, and whether the release
	// is newer. An error means nobody could tell, which is not the same as "up to date".
	UpdateAvailable(ctx context.Context) (current, latest string, available bool, err error)
	// RunUpdate downloads, verifies and installs the newest release, reporting each stage, and
	// returns the version installed.
	RunUpdate(ctx context.Context, progress func(string)) (string, error)
}

// announceUpdate offers the upgrade when the gateway knows of a newer release.
//
// It is an OFFER and nothing more: it names the versions and the command that installs, and
// installs nothing. It reuses the welcome screen's notice rather than the conversation, because a
// message would end the welcome screen on its own and would sit in the history of a conversation
// that is about the user's project. Any failure is silent for the same reason: not being able to
// ask is not news, and `/update` reports it when the user does ask.
func (t *TUI) announceUpdate(ctx context.Context) {
	u, ok := t.Runner.(updater)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	current, latest, available, err := u.UpdateAvailable(ctx)
	if err != nil || !available {
		return
	}
	offer := t.trf("motita %s is available (this is %s). Type /update to install it.", latest, current)
	if t.Notice != "" {
		offer = t.Notice + "\n" + offer
	}
	// Held because this runs beside the input loop, which reads the notice to paint.
	t.draw.Lock()
	t.Notice = offer
	t.draw.Unlock()
	t.drawFrame()
}

// runUpdate is /update: check again, and install when there is something to install.
//
// The check is repeated rather than trusting the announcement, because the announcement can be
// hours old and the command is the user's consent to install what is newest NOW.
func (t *TUI) runUpdate(ctx context.Context) {
	u, ok := t.Runner.(updater)
	if !ok {
		t.addMessage(AuthorSystem, t.tr("update: this interface is not attached to a gateway, and the gateway is what upgrades itself."))
		return
	}
	current, latest, available, err := u.UpdateAvailable(ctx)
	switch {
	case err != nil:
		t.addMessage(AuthorSystem, t.trf("update: could not check for a newer release: %s", err.Error()))
		return
	case !available:
		t.addMessage(AuthorSystem, t.trf("update: motita %s is the newest release.", current))
		return
	}
	t.addMessage(AuthorSystem, t.trf("update: installing %s (this is %s)…", latest, current))
	installed, err := u.RunUpdate(ctx, func(stage string) {
		if s := strings.TrimSpace(stage); s != "" {
			t.addMessage(AuthorSystem, t.tr("update:")+" "+s)
		}
	})
	if err != nil {
		t.addMessage(AuthorSystem, t.trf("update failed: %s", err.Error()))
		return
	}
	t.Notice = ""
	t.addMessage(AuthorSystem, t.trf("update: %s is installed. The gateway is restarting: quit (/quit) and start motita again to use it.", installed))
}
