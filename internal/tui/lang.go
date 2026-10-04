package tui

import (
	"os"
	"strings"

	"github.com/madkoding/motita/internal/i18n"
)

// tr is text in the interface's language: the Spanish catalog's entry, or the English itself.
func (t *TUI) tr(s string) string { return i18n.T(t.Lang, s) }

// trf is tr for a format string: the format is translated, then filled in.
func (t *TUI) trf(format string, args ...any) string { return i18n.Tf(t.Lang, format, args...) }

// languageRunner is a runner that keeps the ui.language setting: the gateway client, which asks the
// gateway to save it in the configuration, and the local runner, which saves it itself. It is an
// optional interface so the small runners the tests use need not grow two methods they never call.
type languageRunner interface {
	UILanguage() string
	SetUILanguage(lang string) error
}

// languageName is a language as a sentence names it, in the interface's language.
func (t *TUI) languageName(l i18n.Lang) string {
	if l == i18n.ES {
		return t.tr("Spanish")
	}
	return t.tr("English")
}

// runLanguage is /language: without an argument it says which setting is in use and what it
// resolves to; with en, es or auto it saves the setting and redraws in the new language at once,
// because a setting that only applies after a restart reads as one that did nothing.
func (t *TUI) runLanguage(arg string) {
	lr, canSave := t.Runner.(languageRunner)
	arg = strings.ToLower(strings.TrimSpace(arg))
	if arg == "" {
		setting := i18n.Auto
		if canSave {
			if s := strings.TrimSpace(lr.UILanguage()); s != "" {
				setting = s
			}
		}
		t.addMessage(AuthorSystem, t.trf("language: %s (showing %s). /language en, es or auto changes it.",
			setting, t.languageName(t.currentLang())))
		return
	}
	if !i18n.Valid(arg) {
		t.addMessage(AuthorSystem, t.trf("unknown language %q: use en, es or auto.", arg))
		return
	}
	if !canSave {
		t.addMessage(AuthorSystem, t.tr("this interface cannot save the language: set ui.language in the configuration file."))
		return
	}
	if err := lr.SetUILanguage(arg); err != nil {
		t.addMessage(AuthorSystem, t.trf("the language could not be saved: %v", err))
		return
	}
	t.draw.Lock()
	t.Lang = i18n.Resolve(arg, os.Getenv)
	t.draw.Unlock()
	// Said in the language just chosen: it is the first sentence the user reads in it.
	t.addMessage(AuthorSystem, t.trf("language set to %s: the interface is now in %s.", arg, t.languageName(t.currentLang())))
}

// currentLang is the language the interface is drawn in, read under the lock /language writes it
// with.
func (t *TUI) currentLang() i18n.Lang {
	t.draw.Lock()
	defer t.draw.Unlock()
	if t.Lang == i18n.ES {
		return i18n.ES
	}
	return i18n.EN
}
