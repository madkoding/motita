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
// ReasonGoAhead is the Reason of a message that only says to go on with what the agent proposed
// ("do it", "ok", "dale"): a task, and the interface that keeps the last plan carries it out.
const ReasonGoAhead = "go-ahead"

var goAheads = set(
	"ok", "okay", "yes", "yep", "sure", "go", "do it", "go ahead", "proceed", "apply it",
	"do that", "go on", "sounds good", "ok do it", "yes do it", "run it", "implement it",
	"si", "dale", "hazlo", "adelante", "procede", "aplicalo", "va", "ya", "listo", "ok hazlo",
	"si hazlo", "dale hazlo", "ejecutalo", "implementalo", "hazlo asi", "me parece", "de acuerdo",
	"perfecto", "bueno", "ok dale", "si dale", "vamos", "sigue", "continua", "ejecuta el plan",
	"aplica el plan", "haz el plan", "run the plan", "execute the plan", "apply the plan",
)

// GoAhead says the message only tells the agent to go on with what it proposed.
func GoAhead(text string) bool {
	plain := strings.Map(func(r rune) rune {
		if strings.ContainsRune(".,;:!¡?¿", r) {
			return ' '
		}
		return r
	}, fold(text))
	return goAheads[strings.Join(strings.Fields(plain), " ")]
}

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
	"migra", "migrar", "cambia", "cambiar", "mueve", "mover", "sube", "subir",
	"haz", "hacer", // spanish-fixture: the Spanish the classifier reads
	"publica", "publicar", "abre", "abrir", "reemplaza", "reemplazar", "aplica", "aplicar",
	"convierte", "convertir", "limpia", "limpiar", "genera", "generar", "edita", "editar",
	"modifica", "modificar", "traduce", "traducir", "pon", "quita", "quitar", "integra",
	"integrar", "despliega", "desplegar", "configura", "configurar", "mejora", "mejorar",
	"resolve", "solve", "repair", "adjust", "optimize", "document", "enable", "disable", "include",
	"split", "combine", "reduce", "increase", "sync", "use", "revert", "undo", "finish", "complete",
	"resuelve", "resolver", "soluciona", "solucionar", "repara", "reparar", "ajusta", "ajustar",
	"optimiza", "optimizar", "documenta", "documentar", "activa", "activar", "desactiva", "desactivar",
	"habilita", "habilitar", "deshabilita", "deshabilitar", "incluye", "incluir", "separa", "separar",
	"divide", "dividir", "combina", "combinar", "reduce", "reducir", "aumenta", "aumentar",
	"sincroniza", "sincronizar", "usa", "usar", "revierte", "revertir", "deshaz", "deshacer", // spanish-fixture: input
	"termina", "terminar", "completa", "completar",
)

// subjunctives are the Spanish verbs of a wish ("quiero que lo arregles") mapped to the
// imperative of actionVerbs: built from the regular endings, plus the irregular ones listed.
var subjunctives = subjunctiveForms(map[string]string{ // spanish-fixture: the Spanish the classifier reads
	"corrijas": "corrige", "traduzcas": "traduce", "reduzcas": "reduce", "incluyas": "incluye",
	"hagas": "haz", "pongas": "pon", "deshagas": "deshaz",
})

// desires open a wish that is an order: "quiero agregar", "I need you to fix", "vamos a mover".
var desires = set(
	"want", "need", "like", "let's", "lets", "gotta",
	"quiero", "queremos", "necesito", "necesitamos", "gustaria", "encantaria", "vamos",
	"hay", "tienes", "tenemos", "debes", "debemos",
)

// leads are the words that may stand before the verb of an order without changing it: fillers,
// polite openers, the person, a wish. "ok, ahora agrega", "can you please", "I'd like you to".
var leads = set(
	"ok", "okay", "hey", "hi", "hello", "so", "now", "also", "and", "then", "please", "pls", "you",
	"i", "i'd", "id", "we", "just",
	"oye", "hola", "bueno", "pues", "ahora", "tambien", "ademas", "y", "luego", "despues", // spanish-fixture: input
	"entonces", // spanish-fixture: input
	"porfa", "porfavor", "me", "nos", "yo", "solo",
)

// wishJoiners follow a wish before its verb: "quiero QUE LO", "need you TO", "vamos A".
var wishJoiners = set("que", "to", "a", "de", "lo", "la", "los", "las", "le", "les", "te", "se")

// planNouns are what a person asks for when they want a plan made, not the work: "haz un plan",
// "make a proposal". The verbs that MAKE one are in planMakers; executing a plan is a task.
var planNouns = set("plan", "planes", "plans", "propuesta", "propuestas", "proposal", "proposals",
	"roadmap", "estrategia", "strategy", "outline", "esquema")

var planMakers = set(
	"make", "create", "write", "draft", "give", "prepare", "build", "propose", "want", "need",
	"haz", "crea", "escribe", "redacta", "dame", "prepara", "arma", "armame", "elabora", "genera",
	"propon", "proponme", "quiero", "necesito", "hazme", "creame", "escribeme", "preparame",
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
	"como", "que", "cual", "cuales", "donde", "quien", "quienes", "cuanto",
	"porque", "cuando", // spanish-fixture: the Spanish the classifier reads
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
	if GoAhead(trimmed) {
		return Decision{KindTask, trimmed, SourceHeuristic, ReasonGoAhead}
	}
	plain := fold(trimmed)
	for _, phrase := range holdPhrases {
		if strings.Contains(plain, phrase) {
			return Decision{KindPlan, trimmed, SourceHeuristic, "asked not to change anything"}
		}
	}
	if kind, reason, ok := byWording(splitSentences(plain)); ok {
		return Decision{kind, trimmed, SourceHeuristic, reason}
	}
	return Decision{KindTask, trimmed, SourceDefault, "default"}
}

// byWording reads the sentences of a message for its mode, and the reason to give.
func byWording(sentences []sentence) (kind, reason string, ok bool) {
	// Asked to make a plan is a plan, though "haz" and "make" are orders: "hazme un plan".
	for _, s := range sentences {
		if noun, ok := asksForPlan(s.words); ok {
			return KindPlan, `asks for a "` + noun + `"`, true
		}
	}
	// An order anywhere is a task: "why does it fail? fix it" wants the fix, and so does
	// "revisa por qué falla y arréglalo". (spanish-fixture: the Spanish the classifier reads)
	for _, s := range sentences {
		if verb, ok := sentenceOrder(s); ok {
			return KindTask, `action "` + verb + `"`, true
		}
	}
	for _, s := range sentences {
		if planVerbs[s.words[0]] {
			return KindPlan, `asks to "` + s.words[0] + `"`, true
		}
		if s.question || questionWords[s.words[0]] || isPorQue(s.words) {
			return KindPlan, "question", true
		}
	}
	return "", "", false
}

// sentenceOrder is the order a sentence gives: at its head, or joined to it by "and". A question
// that joins two verbs ("how do I add and remove items?") asks about both instead.
func sentenceOrder(s sentence) (string, bool) {
	if verb, ok := orders(s.words); ok {
		return verb, true
	}
	asking := (s.question || questionWords[s.words[0]]) && !politeOpeners[s.words[0]]
	if asking {
		return "", false
	}
	return chainedOrder(s.words)
}

// InitPrompt is the task behind /init: write the AGENTS.md that later sessions in this project
// load (see the gateway's project instructions). It asks for what the agent cannot guess from the
// code, and for a file that is kept short, because every line of it is paid for in every session.
const InitPrompt = "Read this project and write an AGENTS.md at its root for the agents that will work here next. " +
	"Look first at what exists (README, Makefile, package.json, go.mod or the like, CI files, the folder layout, " +
	"a CLAUDE.md if there is one). Include only what is true and cannot be guessed from the code: the commands " +
	"to build, test and lint (copied from the project's own files, never invented), the layout of the main " +
	"folders, the conventions that are followed, and the traps that cost time. Keep it under 80 lines, in the " +
	"language the project's docs use, with no secrets. If an AGENTS.md already exists, keep what is right in " +
	"it and fix or add only what is missing instead of rewriting it. Do not change anything else."

// explicitMode reads a leading /plan, /task, /goal or /init.
func explicitMode(text string) (Decision, bool) {
	head, rest, _ := strings.Cut(text, " ")
	if strings.EqualFold(head, "/init") {
		return Decision{KindTask, InitPrompt, SourceExplicit, "/init"}, true
	}
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

// orders reports whether a sentence is an order, and the verb that makes it one. What may stand
// before the verb is skipped: a polite opener ("can you", "puedes", "por favor"), fillers ("ok",
// "ahora", "also") and a wish ("quiero que lo", "I need you to", "vamos a").
func orders(words []string) (string, bool) {
	i, _ := leadLength(words)
	if i == len(words) {
		return "", false
	}
	verb := imperative(words[i])
	return verb, actionVerbs[verb]
}

// leadLength is how many words open the sentence before the verb of a possible order, and
// whether they made a wish.
func leadLength(words []string) (int, bool) {
	i, wished, inWish := 0, false, false
	for i < len(words) && i < 8 {
		w := words[i]
		switch {
		case w == "por" && i+1 < len(words) && words[i+1] == "favor":
			i += 2
			continue
		case politeOpeners[w] || leads[w]:
			// "I'd like YOU to": a filler does not end a wish.
		case desires[w]:
			wished, inWish = true, true
		case inWish && wishJoiners[w]:
			// Still in the wish: "quiero que lo ...".
		default:
			return i, wished
		}
		i++
	}
	return i, wished
}

// chainedOrder finds an order joined to the sentence by "and"/"y"/"then": "look at the logs and
// fix it". Only the word right after the joiner (fillers aside) is read, so "what is X and why"
// is not an order.
func chainedOrder(words []string) (string, bool) {
	for i := 1; i < len(words)-1; i++ {
		if !chainWords[words[i]] {
			continue
		}
		if verb, ok := orders(words[i+1:]); ok {
			return verb, true
		}
	}
	return "", false
}

// chainWords join a second clause to a sentence.
var chainWords = set("and", "then", "y", "luego", "despues", "e")

// asksForPlan reports whether the sentence asks for a plan to be made, and the noun that says so:
// a verb that makes one, or a wish ("quiero un plan"), with a plan noun among the next words. A
// verb that carries a plan out ("implementa el plan") is an order, not this.
func asksForPlan(words []string) (string, bool) {
	i, wished := leadLength(words)
	if i == len(words) {
		return "", false
	}
	maker := planMakers[words[i]]
	if !maker && (!wished || actionVerbs[imperative(words[i])]) {
		return "", false
	}
	for _, w := range words[i:min(len(words), i+5)] {
		if planNouns[w] {
			return w, true
		}
	}
	return "", false
}

// imperative is a Spanish imperative without the pronouns fused onto it: "arreglalo" is
// "arregla". A word that is already a verb, or that has no such ending, is returned as it is.
func imperative(word string) string {
	if actionVerbs[word] {
		return word
	}
	if verb, ok := subjunctives[word]; ok {
		return verb
	}
	for _, c := range clitics {
		if stem, ok := strings.CutSuffix(word, c); ok && actionVerbs[stem] {
			return stem
		}
	}
	return word
}

// subjunctiveForms is the second-person subjunctive of every imperative in actionVerbs that has
// a regular one ("agrega" → "agregues", "sube" → "subas"), plus the irregular ones given.
// (spanish-fixture: the Spanish the classifier reads)
func subjunctiveForms(irregular map[string]string) map[string]string {
	out := make(map[string]string, len(actionVerbs))
	for verb := range actionVerbs {
		if form, ok := regularSubjunctive(verb); ok {
			out[form] = verb
		}
	}
	for form, verb := range irregular {
		out[form] = verb
	}
	return out
}

// spelledStems keeps the sound of an -ar stem before the subjunctive's "e": "publica" is
// "publiques", "agrega" "agregues", "actualiza" "actualices". (spanish-fixture: classifier input)
var spelledStems = map[string]string{"c": "qu", "g": "gu", "z": "c"}

// regularSubjunctive is the second-person subjunctive of an imperative with a regular one: an -a
// imperative takes -es and an -e one -as. Infinitives, English verbs and the short irregular
// imperatives have none.
func regularSubjunctive(verb string) (string, bool) {
	if len(verb) < 3 || strings.HasSuffix(verb, "r") {
		return "", false
	}
	stem := verb[:len(verb)-1]
	if strings.HasSuffix(verb, "e") {
		return stem + "as", true
	}
	if !strings.HasSuffix(verb, "a") {
		return "", false
	}
	last := stem[len(stem)-1:]
	if spelled, ok := spelledStems[last]; ok {
		stem = stem[:len(stem)-1] + spelled
	}
	return stem + "es", true
}

// isPorQue reports whether a sentence opens with "por que" (why),
// folded from "por qué". (spanish-fixture: the Spanish the classifier reads)
func isPorQue(words []string) bool {
	return len(words) > 1 && words[0] == "por" && words[1] == "que"
}

// sentence is one sentence of a message, already folded and split into words.
type sentence struct {
	words    []string
	question bool
}

// splitSentences cuts folded text at . ! ? and line breaks. A sentence is a question when it
// ends with ? or opened with ¿. (spanish-fixture: the Spanish the classifier reads)
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
		case '¿': // spanish-fixture: the Spanish the classifier reads
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
	"á", "a", "é", "e", "í", "i", "ó", "o", // spanish-fixture: the Spanish the classifier reads
	"ú", "u", "ü", "u", "ñ", "n", "’", "'", // spanish-fixture: the Spanish the classifier reads
)

// fold lowercases text and takes the accents off, so "Añade" (spanish-fixture: classifier input)
// and "anade" are the same word.
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
