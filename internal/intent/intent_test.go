package intent

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		text, kind, source, prompt string
	}{
		// An explicit mode always wins, and the command that chose it is not part of the prompt.
		{"/plan add a login page", KindPlan, SourceExplicit, "add a login page"},
		{"/P how does it work?", KindPlan, SourceExplicit, "how does it work?"},
		{"/task what is here?", KindTask, SourceExplicit, "what is here?"},
		{"/t", KindTask, SourceExplicit, ""},
		{"/goal ship the release", KindTask, SourceExplicit, "/goal ship the release"},
		{"/init", KindTask, SourceExplicit, InitPrompt},
		{"/INIT now", KindTask, SourceExplicit, InitPrompt},
		// Orders, in both languages, polite or not, with pronouns fused on.
		{"Add a dark theme to the settings", KindTask, SourceHeuristic, ""},
		{"can you please fix the flaky test?", KindTask, SourceHeuristic, ""},
		{"Arréglalo", KindTask, SourceHeuristic, ""},                             // spanish-fixture: input
		{"¿Puedes agregar un botón de exportar?", KindTask, SourceHeuristic, ""}, // spanish-fixture: input
		{"por favor actualiza las dependencias", KindTask, SourceHeuristic, ""},
		{"¿Por qué falla el build? Corrígelo.", KindTask, SourceHeuristic, ""}, // spanish-fixture: input
		// Questions and asks for a plan.
		{"como puede motita tener un auto mode?", KindPlan, SourceHeuristic, ""},         // spanish-fixture: input
		{"Por qué el gateway guarda la sesión dos veces", KindPlan, SourceHeuristic, ""}, // spanish-fixture: input
		{"What does the scheduler do", KindPlan, SourceHeuristic, ""},
		{"the build is red, any idea?", KindPlan, SourceHeuristic, ""},
		{"Diseña una migración a SQLite", KindPlan, SourceHeuristic, ""}, // spanish-fixture: input
		{"explain the retry policy", KindPlan, SourceHeuristic, ""},
		// A wish is an order, and so are fillers before one and an order joined by "and".
		{"quiero que lo arregles", KindTask, SourceHeuristic, ""},                     // spanish-fixture: input
		{"necesito que agregues un botón de exportar", KindTask, SourceHeuristic, ""}, // spanish-fixture: input
		{"me gustaría que corrijas el login", KindTask, SourceHeuristic, ""},          // spanish-fixture: input
		{"vamos a mover el cache a SQLite", KindTask, SourceHeuristic, ""},            // spanish-fixture: input
		{"hay que actualizar las dependencias", KindTask, SourceHeuristic, ""},        // spanish-fixture: input
		{"I'd like you to add a dark theme", KindTask, SourceHeuristic, ""},
		{"I need to fix the flaky test", KindTask, SourceHeuristic, ""},
		{"ok, ahora agrega el botón", KindTask, SourceHeuristic, ""},                 // spanish-fixture: input
		{"revisa por qué falla el build y arréglalo", KindTask, SourceHeuristic, ""}, // spanish-fixture: input
		{"look at the logs and fix it", KindTask, SourceHeuristic, ""},
		{"write the tests for the parser", KindTask, SourceHeuristic, ""},
		{"implementa el plan", KindTask, SourceHeuristic, ""},             // spanish-fixture: input
		{"quiero que implementes el plan", KindTask, SourceHeuristic, ""}, // spanish-fixture: input
		// Asked to make a plan, with a verb that is otherwise an order.
		{"hazme un plan para migrar a SQLite", KindPlan, SourceHeuristic, ""}, // spanish-fixture: input
		{"crea una propuesta de diseño", KindPlan, SourceHeuristic, ""},       // spanish-fixture: input
		{"quiero un plan para el cache", KindPlan, SourceHeuristic, ""},       // spanish-fixture: input
		{"make a plan to split the gateway", KindPlan, SourceHeuristic, ""},
		{"I need a roadmap for v2", KindPlan, SourceHeuristic, ""},
		// A question that names two verbs asks about them.
		{"how do I add and remove items?", KindPlan, SourceHeuristic, ""},
		{"¿qué agrega este cambio?", KindPlan, SourceHeuristic, ""}, // spanish-fixture: input
		// Holding off beats an order: the safe mistake.
		{"Add OAuth, but don't change anything yet", KindPlan, SourceHeuristic, ""},
		{"implementa el cache, solo el plan", KindPlan, SourceHeuristic, ""},
		// Nothing matched: the mode every message had before auto mode.
		{"hola!", KindTask, SourceDefault, ""},
		{"can", KindTask, SourceDefault, ""},
		{"ok then", KindTask, SourceDefault, ""},
		{"thanks, looks good", KindTask, SourceDefault, ""},
		{"", KindTask, SourceDefault, ""},
	} {
		t.Run(tc.text, func(t *testing.T) {
			d := Classify(tc.text)
			if d.Kind != tc.kind || d.Source != tc.source {
				t.Fatalf("Classify(%q) = %+v, want %s/%s", tc.text, d, tc.kind, tc.source)
			}
			want := tc.prompt
			if tc.source != SourceExplicit {
				want = tc.text
			}
			if d.Prompt != want {
				t.Errorf("prompt = %q, want %q", d.Prompt, want)
			}
			if d.Reason == "" {
				t.Error("a decision with no reason cannot be explained to the person")
			}
		})
	}
}

func TestNoteNamesTheModeAndWhy(t *testing.T) {
	got := Classify("fix it").Note()
	if got != `mode: task (action "fix")` {
		t.Errorf("Note() = %q", got)
	}
}

// TestSubjunctivesNameTheirImperative: the wish forms are derived from the imperatives, with the
// spelling Spanish asks for. (spanish-fixture: the Spanish the classifier reads)
func TestSubjunctivesNameTheirImperative(t *testing.T) {
	for form, verb := range map[string]string{
		"agregues": "agrega", "publiques": "publica", "actualices": "actualiza", "subas": "sube", // spanish-fixture: input
		"corrijas": "corrige", "hagas": "haz", "arregles": "arregla", "despliegues": "despliega", // spanish-fixture: input
	} {
		if got := imperative(form); got != verb {
			t.Errorf("imperative(%q) = %q, want %q", form, got, verb)
		}
	}
}

func TestImperativeLeavesAWordThatIsNotAVerbAlone(t *testing.T) {
	if got := imperative("hola"); got != "hola" {
		t.Errorf("imperative(hola) = %q", got)
	}
}

// TestGoAheadIsATaskThatSaysSo: a go-ahead is a task with its own reason, whatever the wording
// or the language, and a sentence that merely starts with one is not.
func TestGoAheadIsATaskThatSaysSo(t *testing.T) {
	for _, text := range []string{"ok", "Do it!", "go ahead",
		"  dale  ", "Hazlo.", "Sí", "ejecuta el plan"} { // spanish-fixture: input
		d := Classify(text)
		if d.Kind != KindTask || d.Reason != ReasonGoAhead || d.Source != SourceHeuristic {
			t.Errorf("%q: %+v", text, d)
		}
	}
	for _, text := range []string{"ok, but explain the retry policy first", "do it in /tmp",
		"dale una vuelta al diseño"} { // spanish-fixture: input
		if Classify(text).Reason == ReasonGoAhead {
			t.Errorf("%q is not a bare go-ahead", text)
		}
	}
}

func TestEnglishBaseFindsTheVerbOfAGerund(t *testing.T) {
	for word, want := range map[string]string{
		"fixing": "fix", "making": "make", "running": "run", "looking": "looking", "fix": "fix",
	} {
		if got := englishBase(word); got != want {
			t.Errorf("englishBase(%q) = %q, want %q", word, got, want)
		}
	}
}

func TestAimsAtPlanLooksOnlyAFewWordsAhead(t *testing.T) {
	for words, want := range map[string]bool{
		"a plan": true, "me a quick summary": true, "me a": false, "the button": false,
		"me a quick short plan": false,
	} {
		if got := aimsAtPlan(strings.Fields(words)); got != want {
			t.Errorf("aimsAtPlan(%q) = %v, want %v", words, got, want)
		}
	}
}

func TestAsksForPlan(t *testing.T) {
	cases := map[string]bool{
		"make a plan to migrate the database":    true,
		"Planifica la migración":                 true, // spanish-fixture: input
		"¿cuáles son los pasos para instalarlo?": true, // spanish-fixture: input
		"what are the differences?":              false,
		"dame una comparación de las opciones":   false, // spanish-fixture: input
		"explain the diff":                       false,
	}
	for in, want := range cases {
		if got := AsksForPlan(in); got != want {
			t.Errorf("AsksForPlan(%q) = %v, want %v", in, got, want)
		}
	}
}
