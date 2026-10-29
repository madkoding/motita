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
"suggestions": [
{"title": "what you propose to do, in one line", "detail": "why it is worth doing and what applying it would change", "needed": false, "why_needed": ""}
],
"lessons": ["a fact about this project the next task should not have to rediscover"]
}

- "status": "done" when the whole request is met and validated; "partial" when part of it is not; "failed" when it is not met.
- "kind" is one of "added", "modified", "deleted", "other". "result" is one of "pass", "fail", "skipped", "blocked".
- "changes" and "verification" come ONLY from the output above; never list a file you did not see touched or a check that did not run. A check that was not run is "skipped" (or "blocked" when it could not run), with the reason as evidence.
- A check has THREE outcomes, never two: "pass" (it ran and the output shows it passed), "fail" (it ran and failed), "skipped" (you chose not to run it, with the reason) and "blocked" (it could NOT run: missing tool or dependencies, no network; say the cause). A check that could not run is NEVER "pass", and a stand-in that only exits 0 is not a check. When a check the task needs was "blocked", the "status" is "partial" at best, the summary says in its first sentence that the work is NOT VALIDATED and why, and "next_steps" says how to run it.
- "evidence" is only for screenshots that were really taken and saved as files; "before" and "after" are those file names exactly as they appear in the output above, with no directory. Leave a side empty when it does not exist. Never invent a file name; with no screenshots, use [].
- "suggestions" are REAL findings, not filler. Propose something only when the output above or the "Findings:" in the notes shows concrete evidence that it is worth the user's decision: a bug or failing check you saw, a risk the change introduced or exposed, debt that costs something measurable, or a clearly better alternative to what was asked. Each one names the file, symbol or behaviour it is about, says WHY (the evidence you saw) and WHAT applying it would change, and is something you could carry out if the user says yes. They are proposals, never claims of work done: the user is asked about each one and only what they accept is applied.
  NEVER propose generic advice that fits any task: "add more tests", "update the documentation", "improve error handling", "refactor", "add logging", "add comments", a style or taste preference, or anything you did not see a reason for in the output. Do not repeat what "next_steps" or "risks" already say. At most three; the usual number is ZERO, and [] is the right answer when nothing clears this bar. Never pad the list.
- "needed" is true only when the answer is obvious from the context: the request is not really done or correct without it (a bug the change introduced or exposed, a test the new behaviour lacks, a check the change broke, a step the request plainly implies), it stays inside what was asked, it is safe to undo, and anyone who made this request would say yes. Everything else is false: an improvement, an alternative, more scope, a change of behaviour nobody asked for, or anything destructive or hard to undo. In auto mode the needed ones are applied without asking, so when in doubt, use false. When "needed" is true, "why_needed" says in one line, in the user's language, what makes it needed; otherwise it is "". A needed suggestion without a reason is treated as optional.
- "lessons" is what this task LEARNED about the project that is not written in its docs and that the next task would otherwise rediscover: the command that runs its tests or build and how long it takes, a convention the code follows, a trap that cost a round (a tool that is missing, a check that needs a flag), a decision the person made in the conversation. Up to three, each one line, concrete, true of the project and not of this task alone. They are kept in the project and handed to later tasks, so nothing about what was changed here, and [] when nothing new was learned.
- Every list may be empty ([]); never omit a key and never use null.
- Write "summary", each "description", "evidence", "risks", "next_steps" and each suggestion in the user's language.
  You MAY use Markdown inside "summary" to format your answer: **bold**, `inline code`, fenced code blocks, lists. The front end renders it. But the JSON structure must be valid: the Markdown goes INSIDE the string value, not outside it.
  Use only the evidence above. If the output is empty, say so explicitly.
