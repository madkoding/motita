package config

// ---------------------------------------------------------------------------
// Default prompt templates.
//
// They are in English, but they are 100% replaceable from the YAML: the engine
// never writes prompt text on its own, it only substitutes the {{...}} variables
// of these templates.
// ---------------------------------------------------------------------------

// BaseAnalyzeTemplate asks for the analysis of the task and the definition of
// the success criteria that the anchor will check afterwards.
var BaseAnalyzeTemplate = Template{
	System: `You are Layer B (the reasoning engine) of an agent with deterministic validation.
You work in cycles: you propose a result, an independent Layer A validates it by
running real checks, and if it fails you receive the logs of the failure.
ALWAYS answer in English and in the requested JSON format, with no extra text.`,
	User: `## TASK
{{task}}

## CONTEXT
- Working directory: {{workspace}}
- Attempt: {{attempt}} of {{max_attempts}}

## VALIDATION RULES THAT WILL BE APPLIED
{{rules}}

## ANALYSIS OF THE TASK
Return a JSON object with this exact shape:
{
  "understandable": true,
  "summary": "what has to be achieved, in one sentence",
  "success_criteria": ["verifiable criterion 1", "verifiable criterion 2"],
  "risks": ["risk or ambiguity detected"],
  "needs_subtasks": false
}
If the task is ambiguous or impossible with the available tools, set
"understandable": false and explain the reason in "risks". Do not invent data.`,
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
  "subtasks": ["independent subtask, if the task has to be split"],
  "expected_result": "what should be seen when it is done right"
}
The commands must be verifiable and non-destructive unless the task explicitly
requires otherwise. One single command per step.`,
}

// BaseExecuteTemplate asks for the concrete action to run and, when applicable,
// the final action (commit, submission, save) that only runs after a PASS.
var BaseExecuteTemplate = Template{
	System: BaseAnalyzeTemplate.System,
	User: `## TASK
{{task}}

## PLAN
{{plan}}

{{history}}

## ACTION
Return a JSON object with this exact shape:
{
  "reasoning": "why this action fulfils the task",
  "summary": "the concrete answer for the user: what was found, produced, changed or verified. Be specific and cite real values.",
  "actions": [
    {"kind": "command", "description": "what it does", "command": "exact shell command"}
  ],
  "final_action": {"description": "commit, submission or save planned", "command": "exact command or empty"}
}
Rules:
- "summary" is the answer the user will read. It must be factual and complete.
- "actions" are the steps that produce the result; they will be run isolated.
- "final_action" runs ONLY if the validation passes; if it does not apply, leave
  the command as "" and describe why.
- If an attempt failed before, correct it from the logs; do not repeat the same
  action expecting a different result.`,
}
