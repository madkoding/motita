## TASK
{{task}}

## EXECUTED ACTIONS AND THEIR OUTPUT
{{output}}

## VALIDATION RESULT
{{validation}}

## FINAL ANSWER

Return a JSON object with this exact shape. It is DATA: a program lays it out, so every fact goes
in the field that names it, not into the summary.
{
"status": "done",
"summary": "the concrete answer for the user, as if answering directly. Real numbers, names, paths or facts from the output above. Short.",
"changes": [
{"path": "relative/path", "kind": "added", "description": "what changed there, in one line"}
],
"verification": [
{"check": "what was checked, e.g. the command or the test name", "result": "pass", "evidence": "what the output showed, e.g. the count"}
],
"evidence": [
{"title": "what is shown", "before": "login-before.png", "after": "login-after.png", "caption": "what to look at, in one line"}
],
"risks": ["what the reader should know before trusting this"],
"next_steps": ["what is left, or what you would do next"],
"decisions": [
{"decision": "the choice that was taken", "why": "the reason, in one line"}
],
"suggestions": [
{"title": "what you propose to do, in one line", "detail": "why it is worth doing and what applying it would change"}
]
}

- "status": "done" when the whole request is met and validated; "partial" when part of it is not; "failed" when it is not met.
- "kind" is one of "added", "modified", "deleted", "other". "result" is one of "pass", "fail", "skipped".
- "changes" and "verification" come ONLY from the output above; never list a file you did not see touched or a check that did not run. A check that was not run is "skipped", with the reason as evidence.
- "evidence" is only for screenshots that were really taken and saved as files; "before" and "after" are those file names exactly as they appear in the output above, with no directory. Leave a side empty when it does not exist. Never invent a file name; with no screenshots, use [].
- "decisions" is only for a task that says it is a GOAL: every choice taken in place of the user (scope, approach, interface, trade-off). For any other task use [].
- "suggestions" is where you are PROACTIVE: up to five things you noticed and did NOT do, that the user may want next. An issue next to the change (a bug, an edge case, a missing test, a risk), an improvement the change makes worth it, or a better alternative to what was asked. Take them from the "Findings:" in the notes and from the output above; each one is concrete, names the file or behaviour it is about, and is something you could carry out if the user says yes. They are proposals, never claims of work done: the user is asked about each one (apply this one, apply this one and the following, or apply none) and only what they accept is applied. Do not repeat what "next_steps" already says is left of the request itself, and do not pad the list: with nothing worth proposing, use [].
- Every list may be empty ([]); never omit a key and never use null.
- Write "summary", each "description", "evidence", "risks", "next_steps" and each suggestion in the user's language.
  You MAY use Markdown inside "summary" to format your answer: **bold**, `inline code`, fenced code blocks, lists. The front end renders it. But the JSON structure must be valid: the Markdown goes INSIDE the string value, not outside it.
  Use only the evidence above. If the output is empty, say so explicitly.
