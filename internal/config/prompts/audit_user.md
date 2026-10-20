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

Evidence that is simulated does not count. A criterion about tests, a build or a check is met only
by output of the real tool actually running (a runner that names the tests and their count). It is
NOT met when the run shows a stand-in for the tool (an executable or script that only exits 0),
an edited test/lint/build script, a check that "could not run" or was skipped, a missing
dependency, or an exit status with nothing printed. When the real check could not run, say so in
"evidence" and mark the criterion not met: the run is then sent back to either run it for real or
report plainly that the work is not validated.

Return a JSON object with this exact shape, one entry per criterion, in the same order:
{
"criteria": [
{"criterion": "the criterion, as written above", "met": true, "evidence": "what in the output shows it, or what is missing"}
]
}
