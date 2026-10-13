// Package intent decides, from the text of a message alone, whether a turn should run in plan
// mode (read-only: explore and explain) or task mode (make changes and prove them).
//
// It is the "auto mode" of the front ends: the person writes, and motita picks the mode instead
// of making them pick it first. The decision is deliberately cheap and deterministic - a few
// rules over the first words of each sentence, no model call - because it runs before every
// turn and its answer has to be explainable in one line ("mode: plan (question)").
//
// The rules are biased toward plan. Running a task when the person wanted a plan changes their
// files; planning when they wanted a task costs one more message. So a message that both asks
// and orders is a task only when nothing in it says to hold off ("don't change anything",
// "solo el plan"), and an explicit /plan or /task always wins over every rule.
//
// What is not a plan or an order (a greeting, a thank-you) goes to task mode, which is what
// every message did before this package existed: the task agent's own analysis already answers
// those as chat without running the pipeline.
package intent

import (
	"strings"
	"unicode"
)

// Kinds of turn a message can be routed to. They are the same strings as schedule.KindPlan and
// schedule.KindTask, so a Decision's Kind can be handed to the gateway as it is.
const (
	KindPlan = "plan"
	KindTask = "task"
)

// Sources of a decision, from strongest to weakest.
const (
	// SourceExplicit means the message named its mode: /plan, /task, /goal.
	SourceExplicit = "explicit"
	// SourceHeuristic means a rule matched the wording.
	SourceHeuristic = "heuristic"
	// SourceDefault means nothing matched and the message keeps the mode it had before auto
	// mode: task.
	SourceDefault = "default"
)

// Decision is where a message goes and why.
type Decision struct {
	// Kind is KindPlan or KindTask.
	Kind string
	// Prompt is the text to run: the message without the /plan or /task that chose the mode.
	// A /goal stays, because the task agent reads it.
	Prompt string
	// Source says how sure the decision is: see the Source constants.
	Source string
	// Reason is a few words for the person, such as "question" or `action "fix"`.
	Reason string
}

// Note is the progress line that tells the person which mode was picked and how to change it.
func (d Decision) Note() string {
	return "mode: " + d.Kind + " (" + d.Reason + ")"
}

// explicit maps the commands that name a mode to the kind they pick. keep is whether the
// command stays in the prompt.
var explicit = map[string]struct {
	kind string
	keep bool
}{
	"/plan": {KindPlan, false},
	"/p":    {KindPlan, false},
	"/task": {KindTask, false},
	"/t":    {KindTask, false},
	"/goal": {KindTask, true},
}

// actionVerbs open a sentence that orders work, in English and in Spanish. Spanish ones are
// written without accents because words are compared after the accents are taken off.
var actionVerbs = set(
	"add", "fix", "implement", "create", "remove", "delete", "update", "refactor", "rename",
	"write", "run", "install", "migrate", "bump", "change", "move", "make", "build", "deploy",
	"commit", "push", "merge", "open", "replace", "rewrite", "upgrade", "apply", "set", "convert",
	"clean", "generate", "edit", "modify", "translate", "test",
	"agrega", "agregar", "anade", "anadir", "arregla", "arreglar", "corrige", "corregir",
	"implementa", "implementar", "crea", "crear", "elimina", "eliminar", "borra", "borrar",
	"actualiza", "actualizar", "refactoriza", "refactorizar", "renombra", "renombrar",
	"escribe", "escribir", "ejecuta", "ejecutar", "corre", "correr", "instala", "instalar",
	"migra", "migrar", "cambia", "cambiar", "mueve", "mover", "haz", "hacer", "sube", "subir",
	"publica", "publicar", "abre", "abrir", "reemplaza", "reemplazar", "aplica", "aplicar",
	"convierte", "convertir", "limpia", "limpiar", "genera", "generar", "edita", "editar",
	"modifica", "modificar", "traduce", "traducir", "pon", "quita", "quitar", "integra",
	"integrar", "despliega", "desplegar", "configura", "configurar", "mejora", "mejorar",
)

// planVerbs open a sentence that asks for a plan or an explanation rather than for the work.
var planVerbs = set(
	"plan", "design", "propose", "explain", "describe", "review", "analyze", "analyse",
	"investigate", "compare", "evaluate", "audit", "summarize", "summarise",
	"planea", "planifica", "disena", "propon", "explica", "explicame", "describe", "describeme",
	"revisa", "analiza", "investiga", "compara", "evalua", "audita", "resume", "resumeme",
)

// questionWords open a question.
var questionWords = set(
	"how", "what", "why", "where", "which", "who", "when", "is", "are", "does", "should",
	"como", "que", "cual", "cuales", "porque", "donde", "cuando", "quien", "quienes", "cuanto",
	"cuantos", "deberia", "conviene", "existe", "hay",
)

// politeOpeners turn the question that follows into a request: "can you add X?" is an order.
var politeOpeners = set(
	"can", "could", "would", "please", "puedes", "podrias", "podria", "porfa", "porfavor",
)

// holdPhrases say not to do the work yet. Any of them makes the message a plan, whatever else
// it says: that is the bias the package documents.
var holdPhrases = []string{
	"don't change", "do not change", "don't implement", "do not implement", "without changing",
	"just a plan", "only a plan", "only plan", "just plan", "no changes", "read-only",
	"no cambies", "no lo implementes", "no implementes", "sin cambiar", "sin tocar", "solo el plan",
	"solo un plan", "solo planea", "no hagas cambios", "sin cambios", "no modifiques",
}

// clitics are the pronouns Spanish fuses onto an imperative: "arreglalo", "hazme".
var clitics = []string{"selo", "sela", "melo", "mela", "los", "las", "lo", "la", "le", "les", "me", "nos"}

// Classify decides the mode of a message.
func Classify(text string) Decision {
	trimmed := strings.TrimSpace(text)
	if d, ok := explicitMode(trimmed); ok {
		return d
	}
	plain := fold(trimmed)
	for _, phrase := range holdPhrases {
		if strings.Contains(plain, phrase) {
			return Decision{KindPlan, trimmed, SourceHeuristic, "asked not to change anything"}
		}
	}
	sentences := splitSentences(plain)
	// An order anywhere is a task: "why does it fail? fix it" wants the fix.
	for _, s := range sentences {
		if verb, ok := orders(s.words); ok {
			return Decision{KindTask, trimmed, SourceHeuristic, `action "` + verb + `"`}
		}
	}
	for _, s := range sentences {
		if planVerbs[s.words[0]] {
			return Decision{KindPlan, trimmed, SourceHeuristic, `asks to "` + s.words[0] + `"`}
		}
		if s.question || questionWords[s.words[0]] || isPorQue(s.words) {
			return Decision{KindPlan, trimmed, SourceHeuristic, "question"}
		}
	}
	return Decision{KindTask, trimmed, SourceDefault, "default"}
}

// explicitMode reads a leading /plan, /task or /goal.
func explicitMode(text string) (Decision, bool) {
	head, rest, _ := strings.Cut(text, " ")
	mode, ok := explicit[strings.ToLower(head)]
	if !ok {
		return Decision{}, false
	}
	prompt := strings.TrimSpace(rest)
	if mode.keep {
		prompt = text
	}
	return Decision{mode.kind, prompt, SourceExplicit, strings.ToLower(head)}, true
}

// orders reports whether a sentence is an order, and the verb that makes it one. A polite
// opener ("can you", "puedes") is skipped, along with a "you" or "please" after it.
func orders(words []string) (string, bool) {
	i := 0
	if len(words) > 1 && words[0] == "por" && words[1] == "favor" {
		i = 2
	} else if politeOpeners[words[0]] {
		i++
		for i < len(words) && (words[i] == "you" || words[i] == "please" || words[i] == "me") {
			i++
		}
	}
	if i == len(words) {
		return "", false
	}
	verb := imperative(words[i])
	return verb, actionVerbs[verb]
}

// imperative is a Spanish imperative without the pronouns fused onto it: "arreglalo" is
// "arregla". A word that is already a verb, or that has no such ending, is returned as it is.
func imperative(word string) string {
	if actionVerbs[word] {
		return word
	}
	for _, c := range clitics {
		if stem, ok := strings.CutSuffix(word, c); ok && actionVerbs[stem] {
			return stem
		}
	}
	return word
}

// isPorQue reports whether a sentence opens with "por que" (why), folded from "por qué".
func isPorQue(words []string) bool {
	return len(words) > 1 && words[0] == "por" && words[1] == "que"
}

// sentence is one sentence of a message, already folded and split into words.
type sentence struct {
	words    []string
	question bool
}

// splitSentences cuts folded text at . ! ? and line breaks. A sentence is a question when it
// ends with ? or opened with ¿.
func splitSentences(plain string) []sentence {
	var out []sentence
	var cur strings.Builder
	question := false
	flush := func(endsInQuestion bool) {
		words := strings.FieldsFunc(cur.String(), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '-'
		})
		if len(words) > 0 {
			out = append(out, sentence{words, question || endsInQuestion})
		}
		cur.Reset()
		question = false
	}
	for _, r := range plain {
		switch r {
		case '¿':
			question = true
		case '?':
			flush(true)
		case '.', '!', '\n':
			flush(false)
		default:
			cur.WriteRune(r)
		}
	}
	flush(false)
	return out
}

// accents takes the accents off the letters Spanish and English write with them.
var accents = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n", "’", "'",
)

// fold lowercases text and takes the accents off, so "Añade" and "anade" are the same word.
func fold(text string) string {
	return accents.Replace(strings.ToLower(text))
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}
