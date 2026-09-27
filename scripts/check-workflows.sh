#!/bin/sh
# Check every GitHub Actions workflow file parses and uses only keys GitHub knows.
#
# WHY THIS EXISTS
#
# A malformed workflow does not fail a step - it fails the ENTIRE run in zero
# seconds with "workflow file issue", no job, no log, and nothing in the file
# pointing at the line. It is invisible locally and instant in CI.
#
# Measured here: a `tags:` trigger was added as a SIBLING of `push:` instead of
# nested inside it. `on.tags` is not a trigger GitHub knows, and the pages
# workflow went from green on main to failing every run on the branch that
# carried it - 0s, no log to read.
#
# `gh workflow view` cannot catch it either: it happily lists the workflow and
# its run history. So this checks the things that actually break:
#
#   1. the file parses as YAML at all (a bad indent is the classic);
#   2. every top-level key is one GitHub recognises - an unknown one is either a
#      typo or a real mistake, and GitHub will not tell you which;
#   3. `on` is present and its triggers are real ones, with the shape each needs
#      (push/pull_request take a mapping, workflow_dispatch may be null);
#   4. no tab indentation, which YAML forbids outright;
#   5. every `run:` step is at least syntactically valid shell.
#
# It deliberately does NOT try to be a schema validator. It catches the class of
# defect that costs a whole run and reads as nothing.
#
# Usage: scripts/check-workflows.sh [.github/workflows]
# Exit codes: 0 all workflows are sound · 1 one is not.
set -eu

dir="${1:-.github/workflows}"

if [ ! -d "$dir" ]; then
  printf 'error: %s is not a directory\n' "$dir" >&2
  exit 1
fi

python3_bin="${PYTHON:-python3}"

# PyYAML is not guaranteed on the machine, and a check that cannot run must not
# report a pass. A tiny YAML parser is not an option here, so the dependency is
# real and its absence is an honest skip (exit 2) rather than a silent green.
#
# The probe venv is tried second: it is the one this repo already creates for the
# CDP probes (verify-spinner.sh), and it carries PyYAML, so in practice the check
# runs on a machine that has done any verification at all.
probe_python="${SCRATCH:-/home/madkoding/.hermes/cache/scratch}/.cdp/bin/python"
if ! "$python3_bin" -c 'import yaml' 2>/dev/null; then
  if [ -x "$probe_python" ] && "$probe_python" -c 'import yaml' 2>/dev/null; then
    python3_bin="$probe_python"
  else
    printf 'SKIP: PyYAML is not available to %s\n' "$python3_bin"
    printf '      install it, or set PYTHON= to an interpreter that has it\n' >&2
    exit 2
  fi
fi

"$python3_bin" - "$dir" <<'PY'
import pathlib
import re
import shutil
import subprocess
import sys

import yaml

# The keys GitHub accepts at the top level of a workflow. Anything else is a
# mistake: there is no "unknown key" tolerance, and no warning either.
TOP_LEVEL = {
    "name", "run-name", "on", "permissions", "env", "defaults", "concurrency",
    "jobs",
}

# Triggers that are real. `tags` is NOT one of them - it belongs inside `push`,
# and that is the defect this check was written for.
TRIGGERS = {
    "push", "pull_request", "pull_request_target", "workflow_dispatch",
    "workflow_call", "workflow_run", "schedule", "release", "create",
    "delete", "fork", "gollum", "issue_comment", "issues", "label",
    "milestone", "page_build", "project", "project_card", "project_column",
    "public", "registry_package", "repository_dispatch", "status", "watch",
    "merge_group", "deployment", "deployment_status", "discussion",
    "discussion_comment", "check_run", "check_suite", "branch_protection_rule",
}

failures = []


def fail(path, message):
    failures.append(f"{path}: {message}")


def main():
    target = pathlib.Path(sys.argv[1])
    files = sorted(list(target.glob("*.yml")) + list(target.glob("*.yaml")))
    if not files:
        print(f"error: no workflow files in {target}", file=sys.stderr)
        return 1

    for path in files:
        raw = path.read_text(encoding="utf-8")

        # YAML forbids tab indentation, and the failure it produces elsewhere is
        # confusing enough to be worth naming here.
        if "\t" in raw:
            fail(path, "contains a tab character; YAML forbids tab indentation")

        try:
            doc = yaml.safe_load(raw)
        except yaml.YAMLError as exc:
            fail(path, f"does not parse as YAML: {exc}")
            continue

        if not isinstance(doc, dict):
            fail(path, "is not a mapping at the top level")
            continue

        for key in doc:
            # PyYAML resolves the YAML 1.1 bare `on` key to the BOOLEAN True, so
            # the key arrives here as True and not as the string "on". Reported
            # as-is it reads "unknown top-level key True", which is nonsense - and
            # it was this check's own first false positive.
            if key is True or key == "on":
                continue
            if key not in TOP_LEVEL:
                fail(path, f"unknown top-level key {key!r} (GitHub fails the whole run on this)")

        # PyYAML resolves the bare `on` key to the boolean True, which is why
        # this looks odd. Both spellings have to be accepted.
        triggers = doc.get("on", doc.get(True))
        if triggers is None:
            fail(path, "has no `on:` trigger")
            continue

        if isinstance(triggers, str):
            triggers = [triggers]
        if isinstance(triggers, list):
            names = triggers
        elif isinstance(triggers, dict):
            names = list(triggers)
        else:
            fail(path, f"`on:` is neither a trigger name, a list, nor a mapping ({type(triggers).__name__})")
            continue

        for name in names:
            if name not in TRIGGERS:
                fail(
                    path,
                    f"`on.{name}` is not a trigger GitHub recognises; "
                    "if it belongs inside another trigger, nest it there instead",
                )

        # A shell check on each run step, so a syntax error in a script is caught
        # here rather than as a red job.
        for job_name, job in (doc.get("jobs") or {}).items():
            if not isinstance(job, dict):
                fail(path, f"job {job_name!r} is not a mapping")
                continue
            for step in job.get("steps") or []:
                if not isinstance(step, dict):
                    continue
                script = step.get("run")
                if not script:
                    continue

                # A step may declare its shell; default is bash on Linux runners.
                shell = step.get("shell", "bash")
                if isinstance(shell, str) and shell.startswith("sh"):
                    continue

                # A PowerShell step is NOT bash. Checking it as bash failed it on
                # every run - `$ErrorActionPreference = 'Stop'` is a syntax error to
                # bash - so a perfectly good workflow was reported as broken, which
                # is the worst direction for a gate to fail in. Parse it with
                # PowerShell when it is available, and otherwise leave it alone
                # rather than guess with the wrong interpreter.
                if isinstance(shell, str) and shell in ("pwsh", "powershell"):
                    exe = shutil.which("pwsh") or shutil.which("powershell")
                    if exe:
                        proc = subprocess.run(
                            [
                                exe,
                                "-NoProfile",
                                "-Command",
                                "$t=$null;$e=$null;"
                                "[System.Management.Automation.Language.Parser]::ParseInput("
                                "[Console]::In.ReadToEnd(),[ref]$t,[ref]$e)|Out-Null;"
                                "if($e.Count -gt 0){$e|ForEach-Object{Write-Host $_};exit 1}",
                            ],
                            input=script,
                            capture_output=True,
                            text=True,
                        )
                        if proc.returncode != 0:
                            label = step.get("name", "<unnamed step>")
                            detail = (proc.stdout or "").strip().splitlines()
                            fail(
                                path,
                                f"step {label!r} is not valid PowerShell: "
                                f"{detail[0] if detail else '?'}",
                            )
                    continue

                proc = subprocess.run(
                    ["bash", "-n"], input=script, capture_output=True, text=True
                )
                if proc.returncode != 0:
                    label = step.get("name", "<unnamed step>")
                    detail = (proc.stderr or "").strip().splitlines()
                    fail(path, f"step {label!r} is not valid shell: {detail[0] if detail else '?'}")

        print(f"  ok   {path.name} ({len(doc.get('jobs') or {})} job(s), triggers: {', '.join(names)})")

    if failures:
        print()
        for line in failures:
            print(f"FAIL: {line}")
        return 1
    return 0


sys.exit(main())
PY
