package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/i18n"
)

// spanishTUI is the fake interface drawn in Spanish.
func spanishTUI() *TUI {
	tui := newFakeTUI("", &fakeRunner{})
	tui.Lang = i18n.ES
	return tui
}

func frameText(t *TUI, w, h int) string {
	lines, _ := t.layout(w, h)
	return strings.Join(lines, "\n")
}

// The first screen, its footer and the input box's title, in Spanish - and the English one
// untouched by the change, since English is the text itself.
func TestTheWelcomeScreenSpeaksSpanish(t *testing.T) {
	es := frameText(spanishTUI(), 80, 40)
	for _, want := range []string{"¿Qué debería hacer motita?", "Prueba algo como", "Tarea", "enviar", "ayuda", "Modo plan"} { // spanish-fixture: the Spanish screen
		if !strings.Contains(es, want) {
			t.Errorf("the Spanish welcome must show %q:\n%s", want, es)
		}
	}
	en := frameText(newFakeTUI("", &fakeRunner{}), 80, 40)
	if !strings.Contains(en, "What should motita do?") || strings.Contains(en, "Tarea") {
		t.Errorf("the English welcome must stay English:\n%s", en)
	}
}

// Every screen of the welcome, each in Spanish.
func TestEveryWelcomeScreenSpeaksSpanish(t *testing.T) {
	for screen, want := range map[Screen]string{
		ScreenPlan:   "Modo plan: pregunta lo que quieras",                     // spanish-fixture: the Spanish screen
		ScreenModels: "Presiona Enter para preguntarle al proveedor",           // spanish-fixture: the Spanish screen
		ScreenConfig: "Presiona Enter para volver a ejecutar la configuración", // spanish-fixture: the Spanish screen
	} {
		tui := spanishTUI()
		tui.screen = screen
		if got := frameText(tui, 80, 40); !strings.Contains(got, want) {
			t.Errorf("screen %v must show %q:\n%s", screen, want, got)
		}
	}
}

// Spanish runs longer than English; at the narrowest width the gate accepts, no row may spill.
func TestSpanishFitsTheNarrowestWindow(t *testing.T) {
	for _, tui := range []*TUI{spanishTUI(), func() *TUI { x := agentsTUI(sampleAgents()); x.Lang = i18n.ES; x.agentsOpen = true; return x }()} {
		tui.busy = true
		tui.scroll = 3
		tui.Width, tui.Height = minWidth, 30
		for _, l := range strings.Split(frameText(tui, minWidth, 30), "\n") {
			if n := visibleLen(l); n > minWidth {
				t.Errorf("a row is %d columns at width %d: %q", n, minWidth, l)
			}
		}
	}
}

func TestTheHelpSpeaksSpanish(t *testing.T) {
	help := buildHelp(i18n.ES)
	for _, want := range []string{"Teclas (sin Enter)", "Modos y configuración", "id de sesión", "el idioma de la interfaz", "/language"} { // spanish-fixture: the Spanish help
		if !strings.Contains(help, want) {
			t.Errorf("the Spanish help must show %q:\n%s", want, help)
		}
	}
	if helpText != buildHelp(i18n.EN) || !strings.Contains(helpText, "Keys (no Enter needed)") {
		t.Error("the English help must be the one the package keeps")
	}
}

func TestTheAgentsPanelSpeaksSpanish(t *testing.T) {
	tui := agentsTUI(sampleAgents())
	tui.Lang = i18n.ES
	panel := strings.Join(tui.agentsLines(100), "\n")
	if !strings.Contains(panel, "Agentes") || !strings.Contains(panel, "principal") { // spanish-fixture: the Spanish panel
		t.Errorf("panel:\n%s", panel)
	}
	if got := tui.agentsLabel(); !strings.Contains(got, "en curso") { // spanish-fixture: the Spanish footer
		t.Errorf("footer = %q", got)
	}
	empty := agentsTUI(nil)
	empty.Lang = i18n.ES
	if got := strings.Join(empty.agentsLines(60), "\n"); !strings.Contains(got, "ninguno todavía") { // spanish-fixture: the Spanish panel
		t.Errorf("empty panel:\n%s", got)
	}
}

// Event lines are stored in English and translated when drawn, so a switch redraws the history.
func TestEventLabelsAreTranslatedWhenDrawn(t *testing.T) {
	tui := spanishTUI()
	for in, want := range map[string]string{
		"using a tool":         "usando una herramienta", // spanish-fixture: a translated label
		"using read_file a.go": "usando read_file a.go",  // spanish-fixture: a translated label
		"3 actions":            "3 acciones",             // spanish-fixture: a translated label
		"1 action":             "1 acción",               // spanish-fixture: a translated label
		"many actions":         "many actions",
		"something else":       "something else",
	} {
		if got := tui.eventLabel(in); got != want {
			t.Errorf("eventLabel(%q) = %q, want %q", in, got, want)
		}
	}
	tui.messages = []Message{{Author: AuthorAgent, Text: "3 actions", Frozen: true}, {Author: AuthorAgent, Text: "[using tool: x]"}}
	if got := frameText(tui, 80, 40); !strings.Contains(got, "3 acciones") || !strings.Contains(got, "usando x") { // spanish-fixture: a translated label
		t.Errorf("frame:\n%s", got)
	}
}

func TestAnErrorAndItsHintInSpanish(t *testing.T) {
	tui := spanishTUI()
	got := tui.errorText(errors.New("dial tcp: connection refused"))
	if !strings.HasPrefix(got, "error: dial tcp") || !strings.Contains(got, "pista: motita no pudo conectarse") { // spanish-fixture: the Spanish hint
		t.Errorf("errorText = %q", got)
	}
	if got := tui.errorText(errors.New("the key is missing")); !strings.Contains(got, "no hay clave de API para") { // spanish-fixture: the Spanish hint
		t.Errorf("errorText = %q", got)
	}
}

func TestTheConfirmationSpeaksSpanish(t *testing.T) {
	tui := spanishTUI()
	tui.confirm = &confirmState{req: agent.ApprovalRequest{Command: "rm -rf build"}, reply: make(chan bool, 1)}
	window := strings.Join(tui.confirmLines(0), "\n")
	if !strings.Contains(window, "El agente quiere ejecutar:") || !strings.Contains(window, "s o y = sí") { // spanish-fixture: the Spanish window
		t.Errorf("window:\n%s", window)
	}
	req := agent.ApprovalRequest{Command: " ls "}
	if got := confirmHintText(i18n.ES, req, true); got != "[aprobado por el usuario] ls" { // spanish-fixture: the Spanish record
		t.Errorf("approved = %q", got)
	}
	if got := confirmHintText(i18n.EN, req, false); got != "[rejected by the user] ls" {
		t.Errorf("rejected = %q", got)
	}
}

// langRunner keeps a ui.language setting the way the gateway client and the local runner do.
type langRunner struct {
	*fakeRunner
	setting string
	err     error
	saved   []string
}

func (r *langRunner) UILanguage() string { return r.setting }
func (r *langRunner) SetUILanguage(lang string) error {
	if r.err != nil {
		return r.err
	}
	r.saved = append(r.saved, lang)
	r.setting = lang
	return nil
}

func lastSystem(t *TUI) string {
	for i := len(t.messages) - 1; i >= 0; i-- {
		if t.messages[i].Author == AuthorSystem {
			return t.messages[i].Text
		}
	}
	return ""
}

func TestLanguageCommand(t *testing.T) {
	ctx := context.Background()
	run := commandActions["/language"]

	// Without an argument: the setting and what it resolves to; auto when nothing was saved.
	plain := newFakeTUI("", &fakeRunner{})
	run(plain, ctx, "")
	if got := lastSystem(plain); !strings.Contains(got, "language: auto (showing English)") {
		t.Errorf("show = %q", got)
	}
	// A runner that cannot save says so instead of pretending.
	run(plain, ctx, "es")
	if got := lastSystem(plain); !strings.Contains(got, "cannot save the language") || plain.Lang != "" {
		t.Errorf("no saver: %q, lang %q", got, plain.Lang)
	}
	run(plain, ctx, "fr")
	if got := lastSystem(plain); !strings.Contains(got, `unknown language "fr"`) {
		t.Errorf("invalid = %q", got)
	}

	// Saved, and the interface switches at once - the confirmation is already in Spanish.
	lr := &langRunner{fakeRunner: &fakeRunner{}}
	tui := newFakeTUI("", lr)
	run(tui, ctx, " ES ")
	if tui.Lang != i18n.ES || len(lr.saved) != 1 || lr.saved[0] != "es" {
		t.Fatalf("lang %q, saved %v", tui.Lang, lr.saved)
	}
	if got := lastSystem(tui); got != "idioma cambiado a es: la interfaz ahora está en español." { // spanish-fixture: the Spanish confirmation
		t.Errorf("set = %q", got)
	}
	run(tui, ctx, "")
	if got := lastSystem(tui); !strings.Contains(got, "idioma: es (se muestra en español)") { // spanish-fixture: the Spanish show
		t.Errorf("show = %q", got)
	}
	run(tui, ctx, "en")
	if tui.Lang != i18n.EN || !strings.Contains(lastSystem(tui), "the interface is now in English") {
		t.Errorf("back to English: %q %q", tui.Lang, lastSystem(tui))
	}

	// A save that fails changes nothing on screen.
	lr.err = errors.New("read-only file")
	run(tui, ctx, "es")
	if tui.Lang != i18n.EN || !strings.Contains(lastSystem(tui), "the language could not be saved: read-only file") {
		t.Errorf("failed save: %q %q", tui.Lang, lastSystem(tui))
	}
}

func TestTheLocalRunnerSavesTheLanguage(t *testing.T) {
	old := saveUILanguage
	defer func() { saveUILanguage = old }()
	var gotPath, gotLang string
	saveUILanguage = func(path, lang string) error { gotPath, gotLang = path, lang; return nil }

	r := &AppRunner{}
	if r.UILanguage() != "" {
		t.Fatal("nothing saved yet")
	}
	if err := r.SetUILanguage("es"); err != nil || r.UILanguage() != "es" || gotLang != "es" || gotPath != setupPath() {
		t.Fatalf("err %v, setting %q, saved %q to %q", err, r.UILanguage(), gotLang, gotPath)
	}
	saveUILanguage = func(string, string) error { return errors.New("disk full") }
	if err := r.SetUILanguage("en"); err == nil || r.UILanguage() != "es" {
		t.Fatalf("a failed save must keep the setting: err %v, %q", err, r.UILanguage())
	}
}
