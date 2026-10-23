// Package intent decides, from the text of a message alone, whether a turn should run in plan
// mode (read-only: explore and explain) or task mode (make changes and prove them).
//
// It is the "auto mode" of the front ends: the person writes, and motita picks the mode instead
// of making them pick it first. Where a model is at hand it decides (Refine): the cheap model the
// models block gives small calls (summary) is asked about every message that does not name its
// mode. Classify is the rules that stand in for it - a few rules over the first words of each
// sentence - where there is no model (the terminal's input loop, agent.auto_mode_rules_only) or
// the call fails. Either way the answer is explained in one line ("mode: plan (question)").
//
// Both are biased toward plan. Running a task when the person wanted a plan changes their
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
	reason := d.Reason
	if d.Source == SourceFallback {
		reason += "; " + NoteNoModel
	}
	return "mode: " + d.Kind + " (" + reason + ")"
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
	"clean", "generate", "edit", "modify", "translate", "test", "resolve", "optimize", "optimise",
	"document", "finish", "complete", "retry", "revert", "undo", "restore", "adjust", "enable",
	"disable", "split", "extract", "simplify", "rebase", "release", "format", "lint", "connect",
	"wire", "configure", "integrate", "improve", "checkout", "debug", "solve", "patch", "port",
	"agrega", "agregar", "anade", "anadir", "arregla", "arreglar", "corrige", "corregir",
	"implementa", "implementar", "crea", "crear", "elimina", "eliminar", "borra", "borrar",
	"actualiza", "actualizar", "refactoriza", "refactorizar", "renombra", "renombrar",
	"escribe", "escribir", "ejecuta", "ejecutar", "corre", "correr", "instala", "instalar",
	"migra", "migrar", "cambia", "cambiar", "mueve", "mover", "sube", "subir",
	"haz", "hacer", // spanish-fixture: the Spanish the classifier reads
	"publica", "publicar", "abre", "abrir", "reemplaza", "reemplazar", "aplica", "aplicar",
	"convierte", "convertir", "limpia", "limpiar", "genera", "generar", "edita", "editar",
	"modifica", "modificar", "traduce", "traducir", "pon", "poner", "quita", "quitar", "integra",
	"integrar", "despliega", "desplegar", "configura", "configurar", "mejora", "mejorar",
	"resuelve", "resolver", "soluciona", "solucionar", "optimiza", "optimizar", "documenta",
	"documentar", "termina", "terminar", "completa", "completar", "reintenta", "reintentar",
	"revierte", "revertir", "deshaz", "deshacer", "restaura", "restaurar", "ajusta", "ajustar",
	"habilita", "habilitar", "deshabilita", "deshabilitar", "activa", "activar", "desactiva",
	"desactivar", "divide", "dividir", "extrae", "extraer", "simplifica", "simplificar",
	"prueba", "probar", "conecta", "conectar", "formatea", "formatear", "depura", "depurar",
	"repara", "reparar", "programa", "programar", "codifica", "codificar", "cambie", "agregue",
	"repair", "include", "combine", "reduce", "increase", "sync", "incluye", "incluir", "separa",
	"separar", "combina", "combinar", "reducir", "aumenta", "aumentar", "sincroniza", "sincronizar",
)

// subjunctives are the Spanish verbs of a wish ("quiero que lo arregles") mapped to the
// imperative of actionVerbs: built from the regular endings, plus the irregular ones listed.
var subjunctives = subjunctiveForms(map[string]string{ // spanish-fixture: the Spanish the classifier reads
	"corrijas": "corrige", "traduzcas": "traduce", "reduzcas": "reduce", "incluyas": "incluye",
	"hagas": "haz", "pongas": "pon", "deshagas": "deshaz",
})

// makingVerbs are the actions that, aimed at a plan noun, ask for a plan rather than for the
// work: "make a plan", "haz un plan" (spanish-fixture: classifier input), "write a proposal".
var makingVerbs = set(
	"make", "create", "write", "draft", "prepare", "generate", "build", "give",
	"haz", "hacer", "crea", "crear", "escribe", "escribir", "genera", "generar", // spanish-fixture: input
	"arma", "armar", "prepara", "preparar", "redacta", "redactar", "dame", "da",
)

// planNouns are what a plan is called: asking for one of them is asking for a plan.
var planNouns = set(
	"plan", "plans", "proposal", "roadmap", "outline", "strategy", "design", "analysis", "summary",
	"overview", "review", "comparison", "estimate", "explanation", "diagnosis", "ideas", "options",
	"planes", "propuesta", "estrategia", "diseno", "analisis", "resumen", "revision", "comparacion",
	"estimacion", "explicacion", "diagnostico", "ideas", "opciones", "esquema", "borrador", // spanish-fixture: input
)

// planWords open a sentence that asks for a plan or an explanation rather than for the work.
var planWords = set(
	"plan", "design", "propose", "explain", "describe", "review", "analyze", "analyse",
	"investigate", "compare", "evaluate", "audit", "summarize", "summarise", "understand", "show",
	"list", "find", "search", "check", "tell", "look", "think", "suggest", "recommend", "estimate",
	"brainstorm", "diagnose", "trace", "inspect", "read", "learn", "verify", "assess", "see",
	"know", "outline", "ver",
)

// planStems open a Spanish word that asks to understand rather than to change: "explicame",
// "entender", "muestrame" (spanish-fixture: classifier input). They are prefixes because Spanish
// fuses pronouns onto its verbs and conjugates them in more ways than a list keeps up with.
var planStems = []string{
	"plane", "planif", "disen", "propon", "explic", "expliq", "verifiq", "diagnostiq", "describ",
	"revis", "analiz", "analic",
	"investig", "compar", "evalu", "audit", "resum", "entend", "muestr", "mostr", "ensen",
	"lista", "busca", "busqu", "encuentr", "chequea", "verific", "comprueb", "cuentame", "dime",
	"digas", "mira", "lee", "piens", "pens", "opin", "sugier", "suger", "recomiend", "recomend",
	"estim", "diagnostic", "rastre", "aprend", "conoc", "saber", "inspeccion", "valida", "dame",
}

// planWord reports whether a word opens a sentence that asks to understand.
func planWord(word string) bool {
	if planWords[word] {
		return true
	}
	for _, stem := range planStems {
		if strings.HasPrefix(word, stem) {
			return true
		}
	}
	return false
}

// questionWords open a question.
var questionWords = set(
	"how", "what", "why", "where", "which", "who", "when", "is", "are", "does", "should",
	"como", "que", "cual", "cuales", "donde", "quien", "quienes", "cuanto",
	"porque", "cuando", // spanish-fixture: the Spanish the classifier reads
	"cuantos", "deberia", "conviene", "existe", "hay", "do", "can't", "isn't", "any",
)

// politeOpeners turn the question that follows into a request: "can you add X?" is an order.
var politeOpeners = set(
	"can", "could", "would", "will", "please", "puedes", "podrias", "podria", "porfa", "porfavor",
)

// fillers are the words a message opens with before what it asks: "ok, now add the tests",
// "y también actualiza el README" (spanish-fixture: classifier input). They are skipped.
var fillers = set(
	"ok", "okay", "now", "also", "then", "and", "so", "but", "please", "just", "hey", "yes",
	"sure", "great", "cool", "thanks", "alright", "next", "finally", "first", "lastly",
	"bueno", "bien", "ahora", "tambien", "luego", "despues", "y", "e", "pues", "si", "pero", // spanish-fixture: input
	"porfa", "porfavor", "oye", "ya", "vale", "genial", "perfecto", "gracias", "listo", "dale",
	"entonces", "primero", "finalmente", "igual", "mejor", // spanish-fixture: input
)

// conjunctions start a new clause inside a sentence: "revisa si pasan y arreglalos"
// (spanish-fixture: classifier input) holds an order after the "y".
var conjunctions = set(",", ";", "and", "then", "but", "y", "e", "luego", "despues", "pero") // spanish-fixture: input

// request is a way of asking for something without opening with the verb: "quiero que",
// "let's", "help me" (spanish-fixture: classifier input). always says that what follows is
// asked for even when it is not a verb the lists know ("i want you to make the button blue"); a
// request that is not always only counts when the word after it is a verb or a plan noun.
type request struct {
	words  []string
	always bool
	// asked says the request is one even as a question: "me ayudas a arreglarlo?"
	// (spanish-fixture: classifier input), where "hay que reiniciar?" only asks.
	asked bool
}

// requests are tried longest first, so "i want you to" wins over "i want".
var requests = []request{
	{[]string{"i", "would", "like", "you", "to"}, true, false},
	{[]string{"i", "want", "you", "to"}, true, false},
	{[]string{"i", "need", "you", "to"}, true, false},
	{[]string{"i'd", "like", "you", "to"}, true, false},
	{[]string{"me", "gustaria", "que"}, true, false},
	{[]string{"me", "ayudarias", "a"}, true, true},
	{[]string{"me", "ayudas", "a"}, true, true},
	{[]string{"would", "you", "mind"}, true, true},
	{[]string{"do", "you", "mind"}, true, true},
	{[]string{"we", "need", "to"}, true, false},
	{[]string{"we", "have", "to"}, true, false},
	{[]string{"you", "need", "to"}, true, false},
	{[]string{"i", "want", "to"}, false, false},
	{[]string{"i", "need", "to"}, false, false},
	{[]string{"i'd", "like", "to"}, false, false},
	{[]string{"quiero", "que"}, true, false},
	{[]string{"necesito", "que"}, true, false},
	{[]string{"quisiera", "que"}, true, false},
	{[]string{"ayudame", "a"}, true, false},
	{[]string{"vamos", "a"}, true, false},
	{[]string{"hay", "que"}, true, false},
	{[]string{"tenemos", "que"}, true, false},
	{[]string{"tienes", "que"}, true, false},
	{[]string{"help", "me"}, true, false},
	{[]string{"let", "us"}, true, false},
	{[]string{"we", "should"}, true, false},
	{[]string{"you", "should"}, true, false},
	{[]string{"let's"}, true, false},
	{[]string{"lets"}, true, false},
	{[]string{"i", "want"}, false, false},
	{[]string{"i", "need"}, false, false},
	{[]string{"quiero"}, false, false},
	{[]string{"necesito"}, false, false},
	{[]string{"quisiera"}, false, false},
	{[]string{"me", "gustaria"}, false, false},
}

// pronouns sit between a request and its verb: "quiero que me expliques", "help me to fix".
var pronouns = set(
	"me", "te", "lo", "la", "le", "les", "nos", "se", "to", "you", "us", "un", "una", "a", "an", "the", "el",
)

// problemWords say that something is broken: a report of a problem with no order in it is
// planned (diagnosed) first, and the go-ahead after the plan fixes it.
var problemWords = set(
	"error", "errors", "errores", "fails", "failing", "failed", "failure", "broken", "bug", "bugs",
	"crash", "crashes", "crashing", "panic", "exception", "timeout", "exceeded", "problem",
	"problems", "issue", "issues", "wrong", "falla", "fallan", "fallando", "fallo", "roto",
	"rota", "rompe", "problema", "problemas", "excepcion", "crashea", "cuelga", "lento", "slow",
)

// problemPhrases say the same in more than one word.
var problemPhrases = []string{
	"doesn't work", "does not work", "not working", "isn't working", "won't start", "no funciona",
	"no anda", "no carga", "no compila", "no arranca", "no sirve", "se cae", "me sale",
	"me aparece", "i'm getting", "im getting", "i get a",
}

// holdPhrases say not to do the work yet. Any of them makes the message a plan, whatever else
// it says: that is the bias the package documents.
var holdPhrases = []string{
	"don't change", "do not change", "don't implement", "do not implement", "without changing",
	"just a plan", "only a plan", "only plan", "just plan", "no changes", "read-only",
	"don't touch", "do not touch", "don't modify", "before changing", "before you change",
	"no cambies", "no lo implementes", "no implementes", "sin cambiar", "sin tocar", "solo el plan",
	"solo un plan", "solo planea", "no hagas cambios", "sin cambios", "no modifiques", "no toques",
	"antes de cambiar", "no lo hagas", "no hagas nada", "todavia no", "aun no", // spanish-fixture: input
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
	// An order anywhere is a task: "why does it fail? fix it" wants the fix. The first sign of a
	// plan is kept and used only when no order turns up.
	plan := ""
	for _, s := range splitSentences(plain) {
		kind, reason := s.read()
		if kind == KindTask {
			return Decision{KindTask, trimmed, SourceHeuristic, reason}
		}
		if kind == KindPlan && plan == "" {
			plan = reason
		}
	}
	if plan != "" {
		return Decision{KindPlan, trimmed, SourceHeuristic, plan}
	}
	if reportsProblem(plain) {
		return Decision{KindPlan, trimmed, SourceHeuristic, ReasonProblem}
	}
	return Decision{KindTask, trimmed, SourceDefault, "default"}
}

// ReasonProblem is the Reason of a message that reports something broken without saying what
// to do: it is planned (looked into) first, and a go-ahead fixes it.
const ReasonProblem = "reports a problem"

// read decides what one sentence asks: KindTask, KindPlan or "" with the reason. Every clause of
// a statement can hold an order ("revisa si pasan y arreglalos", spanish-fixture: input); in a
// question only the first one does, unless the question is a polite request ("can you check it
// and fix it?"), because "what is the difference between add and remove?" orders nothing.
func (s sentence) read() (string, string) {
	clauses := splitClauses(s.words)
	first := skipFillers(clauses[0])
	if s.question && !politeOpening(first) {
		if r, ok := requestOf(first); !ok || !r.asked {
			return s.readQuestion(first)
		}
	}
	if kind, reason := readClauses(clauses); kind != "" {
		return kind, reason
	}
	if len(first) > 0 && (s.question || questionWords[first[0]] || isPorQue(first)) {
		return KindPlan, "question"
	}
	return "", ""
}

// politeOpening says whether a clause opens with a polite opener: "can you", "me puedes"
// (spanish-fixture: classifier input).
func politeOpening(words []string) bool {
	return len(words) > 0 && politeOpeners[words[skipMe(words)]]
}

// skipMe is where a clause starts once a "me" before a polite opener is skipped: 1 for
// "me puedes", 0 otherwise. (spanish-fixture: classifier input)
func skipMe(words []string) int {
	if len(words) > 1 && words[0] == "me" && politeOpeners[words[1]] {
		return 1
	}
	return 0
}

// readClauses reads every clause: the first order wins, else the first sign of a plan.
func readClauses(clauses [][]string) (string, string) {
	plan := ""
	for _, c := range clauses {
		kind, reason := readClause(skipFillers(c))
		if kind == KindTask {
			return kind, reason
		}
		if kind == KindPlan && plan == "" {
			plan = reason
		}
	}
	if plan != "" {
		return KindPlan, plan
	}
	return "", ""
}

// readQuestion decides a question that is not a polite request: only an explicit ask for a plan
// changes that it is a question, and a question is a plan.
func (s sentence) readQuestion(first []string) (string, string) {
	if kind, reason := readClause(first); kind == KindPlan {
		return kind, reason
	}
	return KindPlan, "question"
}

// requestOf is the request a clause opens with, if any.
func requestOf(words []string) (request, bool) {
	for _, r := range requests {
		if hasPrefix(words, r.words) {
			return r, true
		}
	}
	return request{}, false
}

// readClause decides what one clause asks, from its first words.
func readClause(words []string) (string, string) {
	if len(words) == 0 {
		return "", ""
	}
	if kind, reason, ok := asks(words); ok {
		return kind, reason
	}
	verb, at, ok := orders(words)
	if at < len(words) && makingVerbs[verb] && aimsAtPlan(words[at+1:]) {
		return KindPlan, "asks for a plan"
	}
	if ok {
		return KindTask, `action "` + verb + `"`
	}
	if i := skipPolite(words); i < len(words) && planWord(words[i]) && !questionWords[words[i]] {
		return KindPlan, `asks to "` + words[i] + `"`
	}
	return "", ""
}

// asks reads a clause that opens with a request ("quiero que", "let's", spanish-fixture: input)
// and decides it from the word that follows.
func asks(words []string) (string, string, bool) {
	if r, ok := requestOf(words); ok {
		rest := words[len(r.words):]
		for len(rest) > 0 && pronouns[rest[0]] {
			rest = rest[1:]
		}
		phrase := strings.Join(r.words, " ")
		switch {
		case aimsAtPlan(words[len(r.words):]):
			return KindPlan, "asks for a plan", true
		case len(rest) > 0 && (planWord(rest[0]) || planWord(strings.TrimSuffix(rest[0], "ing"))):
			return KindPlan, `asks to "` + rest[0] + `"`, true
		case len(rest) > 0 && actionVerbs[englishBase(rest[0])]:
			return KindTask, `action "` + englishBase(rest[0]) + `"`, true
		case len(rest) > 0 && r.always:
			return KindTask, `asks "` + phrase + `"`, true
		default:
			// A request with nothing it recognises after it decides nothing.
		}
	}
	return "", "", false
}

// aimsAtPlan reports whether the words after a verb name a plan: "a plan", "un plan para"
// (spanish-fixture: classifier input), "me a proposal".
func aimsAtPlan(words []string) bool {
	for i := 0; i < len(words) && i < 4; i++ {
		if planNouns[words[i]] {
			return true
		}
		if !pronouns[words[i]] && words[i] != "quick" && words[i] != "short" && words[i] != "breve" {
			return false
		}
	}
	return false
}

// reportsProblem says whether a message describes something broken.
func reportsProblem(plain string) bool {
	for _, phrase := range problemPhrases {
		if strings.Contains(plain, phrase) {
			return true
		}
	}
	for _, w := range strings.FieldsFunc(plain, notWordRune) {
		if problemWords[w] {
			return true
		}
	}
	return false
}

// englishBase is the verb behind an English gerund: "fixing" is "fix", "making" is "make",
// "running" is "run". Any other word is returned as it is.
func englishBase(word string) string {
	stem, ok := strings.CutSuffix(word, "ing")
	if !ok {
		return word
	}
	switch {
	case actionVerbs[stem]:
		return stem
	case actionVerbs[stem+"e"]:
		return stem + "e"
	case len(stem) > 1 && stem[len(stem)-1] == stem[len(stem)-2] && actionVerbs[stem[:len(stem)-1]]:
		return stem[:len(stem)-1]
	default:
		return word
	}
}

// skipFillers drops the words a clause opens with before what it asks.
func skipFillers(words []string) []string {
	for len(words) > 0 {
		switch {
		case fillers[words[0]]:
			words = words[1:]
		case len(words) > 1 && words[0] == "por" && words[1] == "favor":
			words = words[2:]
		default:
			return words
		}
	}
	return words
}

// splitClauses cuts a sentence's words at commas and conjunctions. It always returns at least one
// clause, possibly empty.
func splitClauses(words []string) [][]string {
	out := [][]string{nil}
	for _, w := range words {
		if conjunctions[w] {
			if len(out[len(out)-1]) > 0 {
				out = append(out, nil)
			}
			continue
		}
		out[len(out)-1] = append(out[len(out)-1], w)
	}
	return out
}

func hasPrefix(words, prefix []string) bool {
	if len(words) < len(prefix) {
		return false
	}
	for i, w := range prefix {
		if words[i] != w {
			return false
		}
	}
	return true
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

// orders reports whether a clause is an order, the verb that makes it one and where it is. A
// polite opener ("can you", "puedes") is skipped, along with a "you" or "please" after it.
func orders(words []string) (string, int, bool) {
	i := skipPolite(words)
	if i == len(words) {
		return "", i, false
	}
	verb := imperative(words[i])
	return verb, i, actionVerbs[verb]
}

// skipPolite is where a clause's verb is once a polite opener ("can you please", "me puedes",
// spanish-fixture: classifier input) is skipped: 0 when there is none.
func skipPolite(words []string) int {
	i := skipMe(words)
	if !politeOpeners[words[i]] {
		return 0
	}
	i++
	for i < len(words) && (words[i] == "you" || words[i] == "please" || words[i] == "me") {
		i++
	}
	return i
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
			return notWordRune(r) && r != ',' && r != ';'
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
		case ',', ';':
			cur.WriteString(" " + string(r) + " ")
		default:
			cur.WriteRune(r)
		}
	}
	flush(false)
	return out
}

// notWordRune is a rune that is not part of a word.
func notWordRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '-'
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
