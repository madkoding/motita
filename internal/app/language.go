package app

import (
	"os"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/i18n"
)

// wizardLanguage is the language the setup wizard speaks: ui.language from the file it is about
// to rewrite, when there is one, resolved against this terminal's locale. A first run has no file
// and no setting, so "auto" applies and the wizard speaks the locale's language - the question of
// which language to use is answered before anyone has had to understand a question in English.
func wizardLanguage(path string) i18n.Lang {
	setting := os.Getenv("MOTITA_UI_LANGUAGE")
	if cfg, err := config.LoadWithoutKey(path); err == nil {
		setting = cfg.UI.Language
	}
	return i18n.Resolve(setting, os.Getenv)
}

// uiLanguageReader is the part of a gateway client that reads the interface language setting.
type uiLanguageReader interface {
	UILanguage() string
}

// clientLanguage is the language of an interface attached to a gateway: the GATEWAY's setting,
// because that is the one every interface shares and the one the browser's settings change, and
// this process's own configuration only when the gateway cannot say. "auto" is resolved here,
// against this terminal's locale, which is the environment the person reading it is in.
func clientLanguage(runner any, cfg config.Config) i18n.Lang {
	setting := ""
	if gw, ok := runner.(uiLanguageReader); ok {
		setting = gw.UILanguage()
	}
	if setting == "" {
		setting = cfg.UI.Language
	}
	return i18n.Resolve(setting, os.Getenv)
}
