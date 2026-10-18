## TASK
{{task}}

## SUCCESS CRITERIA
{{criteria}}

## WHAT THE RUN DID
{{output}}

## CRITERIA AUDIT

The run reports the task is finished. Before the project's own checks judge it, decide for EACH
success criterion above whether the work shows it is met. Be strict and literal: a criterion is
met only when the output above contains evidence of it (a command that printed the result, a test
that passed, a file that was written). Something the run says it did, with nothing to show it, is
not met. A criterion the request itself made unnecessary (it asked a question, nothing was to
change) is met when the run says so.

Return a JSON object with this exact shape, one entry per criterion, in the same order:
{
"criteria": [
{"criterion": "the criterion, as written above", "met": true, "evidence": "what in the output shows it, or what is missing"}
]
}
