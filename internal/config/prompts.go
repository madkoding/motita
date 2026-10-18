package config

import _ "embed"

// Template is a prompt with {{name}} variables.
type Template struct {
	System string `yaml:"system"`
	User   string `yaml:"user"`
}

// Prompts groups the templates used by the agent's main loop.
type Prompts struct {
	Analyze    Template `yaml:"analyze"`
	Plan       Template `yaml:"plan"`
	Execute    Template `yaml:"execute"`
	Synthesize Template `yaml:"synthesize"`
	// Audit checks a claim of "done" against the success criteria the analysis stated, before the
	// anchor is spent on it. See agent.criteria_audit.
	Audit Template `yaml:"audit"`
}

// ---------------------------------------------------------------------------
// Default prompt templates.
//
// They are in English, but they are 100% replaceable from the YAML: the engine
// never writes prompt text on its own, it only substitutes the {{...}} variables
// of these templates.
// ---------------------------------------------------------------------------

// The longer prompt bodies live in prompts/ as plain text, embedded verbatim: they are prose a
// model reads, with lines as long as their paragraphs, and keeping them out of the Go source
// keeps them readable as text and editable without escaping a single backtick.
var (
	//go:embed prompts/analyze_user.md
	analyzeUserPrompt string
	//go:embed prompts/execute_user.md
	executeUserPrompt string
	//go:embed prompts/synthesize_system.md
	synthesizeSystemPrompt string
	//go:embed prompts/synthesize_user.md
	synthesizeUserPrompt string
	//go:embed prompts/audit_user.md
	auditUserPrompt string
)

// BaseAnalyzeTemplate asks for the analysis of the task and the definition of
// the success criteria that the anchor will check afterwards.
//
// The System block states the LANGUAGE rule for the whole template set, and it is the one the
// other three templates already assumed: answer in the language the user wrote in. It used to
// say "ALWAYS answer in English", which the rest of this file contradicted — the analyse prompt
// asks for "a normal conversational answer in the user's own language", and for a question "in
// the user's own language". Two rules in one set, and the one the user READ was the wrong one.
//
// The split is: the JSON KEYS stay English because the program parses them, and the VALUES are
// prose the user reads, so they mirror the request.
var BaseAnalyzeTemplate = Template{
	System: `You are Layer B (the reasoning engine) of an agent with deterministic validation.
You work in cycles: you propose a result, an independent Layer A validates it by
running real checks, and if it fails you receive the logs of the failure.
Speak the language the user wrote in. The keys and the JSON structure are always
English, because they are what the program parses; the VALUES are the user's own
language, because they are what the user reads. This applies to every field a person
sees — a question, an assumption, a summary, a reply — and a request in Spanish gets
Spanish, a request in Portuguese gets Portuguese.`,
	User: analyzeUserPrompt,
}

// BasePlanTemplate asks for the action plan.
var BasePlanTemplate = Template{
	System: BaseAnalyzeTemplate.System,
	User: `## TASK
{{task}}

## PREVIOUS ANALYSIS
{{analysis}}

## ACTION PLAN
Return a JSON object with this exact shape:
{
  "plan": [
    {"step": 1, "action": "what is done", "command": "exact shell command or empty"}
  ],
  "subtasks": [],
  "independent": false,
  "expected_result": "what should be seen when it is done right"
}
The commands must be verifiable and non-destructive unless the task explicitly
requires otherwise. One single command per step.

"subtasks" is EMPTY for almost every task: the steps of "plan" are carried out one round
after another by the same worker, with everything already done in view. Split into
subtasks ONLY when the request is really several separate pieces of work that can each
be finished and verified on its own (for example "add the endpoint, then write its
documentation page"). Each subtask is then run as its own task, in the order listed, so
write each one as a complete instruction that names what it applies to, and list them
in the order the work has to happen. Never split a single change into its steps.
"independent" is true ONLY when the subtasks touch different files and none of them
builds on another's result: they are then carried out at the same time, each in a
working copy of its own, and merged when all have ended. When one subtask reads or
changes what another produces, leave it false.`,
}

// BaseExecuteTemplate asks for the concrete action to run and, when applicable,
// the final action (commit, submission, save) that only runs after a PASS.
var BaseExecuteTemplate = Template{
	System: BaseAnalyzeTemplate.System,
	User:   executeUserPrompt,
}

// BaseSynthesizeTemplate asks for the final, evidence-based answer to the user
// after the actions have run and the validation has passed.
var BaseSynthesizeTemplate = Template{
	System: synthesizeSystemPrompt,
	User:   synthesizeUserPrompt,
}

// BaseAuditTemplate asks whether a claim of "done" meets each success criterion the analysis
// stated, with the evidence from the run. It is a second reading of the work before the anchor,
// so a criterion the project's own checks cannot see (a behaviour, a file nobody tests) is not
// left to the user to notice.
var BaseAuditTemplate = Template{
	System: BaseAnalyzeTemplate.System,
	User:   auditUserPrompt,
}
