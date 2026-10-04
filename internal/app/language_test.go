package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/i18n"
)

func clearLocale(t *testing.T, lang string) {
	t.Helper()
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", lang)
	t.Setenv("MOTITA_UI_LANGUAGE", "")
}

// The wizard speaks the language of the file it is about to rewrite, and on a first run - no
// file yet - the terminal's locale, or MOTITA_UI_LANGUAGE when it is set.
func TestTheWizardSpeaksTheConfiguredOrTheLocalesLanguage(t *testing.T) {
	clearLocale(t, "en_US.UTF-8")
	dir := t.TempDir()
	path := filepath.Join(dir, "motita.yaml")
	if got := wizardAnswers(path); got.Lang != i18n.EN || got.LanguageSetting != "" {
		t.Errorf("no file, English locale = %+v", got)
	}
	t.Setenv("MOTITA_UI_LANGUAGE", "es")
	if got := wizardAnswers(path); got.Lang != i18n.ES || got.LanguageSetting != "es" {
		t.Errorf("no file, MOTITA_UI_LANGUAGE=es = %+v", got)
	}
	t.Setenv("MOTITA_UI_LANGUAGE", "")
	if err := os.WriteFile(path, []byte("llm:\n  provider: ollama\nui:\n  language: es\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := wizardAnswers(path); got.Lang != i18n.ES || got.LanguageSetting != "es" {
		t.Errorf("a file saying es = %+v (the setting must travel, to be written back)", got)
	}
}

type langGateway struct{ setting string }

func (g langGateway) UILanguage() string { return g.setting }

// An interface attached to a gateway speaks the GATEWAY's setting, falling back to its own
// configuration when the gateway cannot say, and resolves auto against its own locale.
func TestAnAttachedInterfaceSpeaksTheGatewaysLanguage(t *testing.T) {
	clearLocale(t, "es_CL.UTF-8")
	cfg := config.Default()
	cfg.UI.Language = "en"
	if got := clientLanguage(langGateway{"es"}, cfg); got != i18n.ES {
		t.Errorf("the gateway says es = %s", got)
	}
	if got := clientLanguage(langGateway{""}, cfg); got != i18n.EN {
		t.Errorf("the gateway cannot say, the config says en = %s", got)
	}
	if got := clientLanguage(langGateway{"auto"}, cfg); got != i18n.ES {
		t.Errorf("auto on a Spanish terminal = %s", got)
	}
	if got := clientLanguage(struct{}{}, cfg); got != i18n.EN {
		t.Errorf("a runner that cannot be asked uses the config = %s", got)
	}
}
