package onboard

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/i18n"
)

// openAIPass answers one whole pass for OpenAI: the provider, its endpoint, no key, the
// recommended model, no check, and the review accepted.
var openAIPass = []string{"openai", "", "", "", "3", ""}

// TestTheWholeSetupInSpanish: started in Spanish (the caller resolved ui.language), every step
// speaks it - the banner, the step headers, the menus, the prompts, the review and the summary -
// and the configuration keeps the setting it had.
func TestTheWholeSetupInSpanish(t *testing.T) {
	out, res, err := run(context.Background(), t, t.TempDir(), openAIPass, Answers{Lang: i18n.ES})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	plain := stripANSI(out)
	for _, want := range []string{
		"Vamos a conectar motita",             // spanish-fixture: the banner
		"Idioma: español",                     // spanish-fixture: the language hint
		"1 Proveedor",                         // spanish-fixture: the breadcrumb
		"¿Qué proveedor de IA quieres usar?",  // spanish-fixture: step 1
		"clave de API de platform.openai.com", // spanish-fixture: a provider's note
		"Proveedor [1]:",                      // spanish-fixture: a prompt
		"Conexión con OpenAI",                 // spanish-fixture: step 2
		"URL base de la API [",                // spanish-fixture: a formatted prompt
		"Tu clave de API",                     // spanish-fixture: the key question
		"¿Qué modelo debe usar motita?",       // spanish-fixture: step 3
		"recomendado · barato y rápido",       // spanish-fixture: the model notes
		"Detectarla del proyecto",             // spanish-fixture: the check menu
		"Revisar y guardar",                   // spanish-fixture: the review
		"todavía sin clave",                   // spanish-fixture: the sign-in field
		"[S/n]",                               // spanish-fixture: the review prompt
		"¡Listo!",                             // spanish-fixture: the summary
		"Antes de tu primera tarea",           // spanish-fixture: the summary
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("the Spanish transcript lacks %q:\n%s", want, plain)
		}
	}
	for _, english := range []string{"Which AI provider", "Review and save", "All set!", "Before your first task"} {
		if strings.Contains(plain, english) {
			t.Errorf("an English line was left in the Spanish setup: %q", english)
		}
	}
	if got := readWritten(t, res.ConfigPath); !strings.Contains(got, "language: auto") {
		t.Errorf("an unchanged language must be written back as the setting it was (auto):\n%s", got)
	}
}

// TestTheLanguageIsSwitchedAtTheFirstQuestion: es typed as the first answer switches the rest of the
// setup to Spanish, does not use up an attempt (three switches and the provider still lands), and
// the choice is what the configuration records. The review accepts the Spanish "s".
func TestTheLanguageIsSwitchedAtTheFirstQuestion(t *testing.T) {
	answers := append([]string{"es", "en", "español"}, "openai", "", "", "", "3", "s") // spanish-fixture: typed answers
	out, res, err := run(context.Background(), t, t.TempDir(), answers, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	plain := stripANSI(out)
	first := strings.Index(plain, "Which AI provider do you want to use?")
	spanish := strings.LastIndex(plain, "¿Qué proveedor de IA quieres usar?") // spanish-fixture: step 1
	if first < 0 || spanish < first {
		t.Fatalf("the question must be asked in English, then again in Spanish:\n%s", plain)
	}
	if !strings.Contains(plain[spanish:], "Revisar y guardar") { // spanish-fixture: the review
		t.Errorf("the rest of the setup must speak the language chosen:\n%s", plain[spanish:])
	}
	if res.Provider.ID != "openai" {
		t.Fatalf("provider = %q", res.Provider.ID)
	}
	if got := readWritten(t, res.ConfigPath); !strings.Contains(got, "language: es") {
		t.Errorf("the language chosen in the setup must be written:\n%s", got)
	}
}

// TestTheLanguageSettingIsWrittenBack: without a switch, the setting the configuration had is kept
// (normalised), and one motita would refuse is written as auto rather than copied into a file that
// would then fail to load.
func TestTheLanguageSettingIsWrittenBack(t *testing.T) {
	cases := map[string]string{"es": "language: es", " EN ": "language: en", "fr": "language: auto", "": "language: auto"}
	for setting, want := range cases {
		_, res, err := run(context.Background(), t, t.TempDir(), openAIPass, Answers{LanguageSetting: setting})
		if err != nil {
			t.Fatalf("%q: %v", setting, err)
		}
		if got := readWritten(t, res.ConfigPath); !strings.Contains(got, want) {
			t.Errorf("setting %q: want %q in:\n%s", setting, want, got)
		}
	}
	if got := string(renderConfig(configValues{provider: "openai", model: "m"})); !strings.Contains(got, "ui:\n") || !strings.Contains(got, "language: auto") {
		t.Errorf("a configuration rendered with no language records auto:\n%s", got)
	}
}

// TestLanguageAnswers: the names a user types for each language, in either of them.
func TestLanguageAnswers(t *testing.T) {
	for answer, want := range map[string]i18n.Lang{"es": i18n.ES, "Español": i18n.ES, "spanish": i18n.ES, "EN": i18n.EN, "inglés": i18n.EN, "English": i18n.EN} { // spanish-fixture: typed answers
		if got, ok := languageAnswer(answer); !ok || got != want {
			t.Errorf("languageAnswer(%q) = %q, %v", answer, got, ok)
		}
	}
	if _, ok := languageAnswer("openai"); ok {
		t.Error("a provider is not a language")
	}
}

func readWritten(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
