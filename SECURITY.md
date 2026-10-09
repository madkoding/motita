# Security policy

## Supported versions

motita ships from `main` through automatic releases, and only the **latest
release** receives security fixes. A fix is released as a new version rather
than backported; `motita` updates itself (`/update`) or can be reinstalled with
the installer. If you run a build from source, update to the latest `main`.

## Reporting a vulnerability

Please report vulnerabilities **privately**, through GitHub's Private
Vulnerability Reporting:
**Security → Report a vulnerability** on
<https://github.com/madkoding/motita/security/advisories/new>.

Do not open a public issue, pull request or discussion for a vulnerability.
Include what you found, the version (`motita -version`) and platform, the
steps or input that reproduce it, and what an attacker gains. You will get an
acknowledgement within a few days; once a fix is released, the advisory is
published with credit unless you prefer otherwise.

## Threat model

motita is an agent: a language model proposes shell commands and file edits,
and motita's own code decides what runs. The security boundary is that code,
never the model.

**Untrusted input.** All of the following are treated as attacker-controlled,
because any of them can carry a prompt injection or a crafted payload:

- **Repository contents**: files in the workspace or in a cloned repository,
  including project skills, configuration and documentation it ships;
- **Tool output**: anything a command prints, fetched web content, API
  responses, test logs;
- **LLM output**: the commands, paths, patches and text the model produces,
  whatever it was told.

**What is protected.** The user's machine outside the workspace, their
credentials (API keys, git host tokens, the gateway token), and their control
over what runs: commands that reach the network or the system, or write outside
the workspace, need the user's approval, and a fixed floor of destructive
commands is refused in every mode.

**The release path.** Binaries are built and published by CI. The updater and
the installers verify each download against the release's `SHA256SUMS`, which
is signed with the project's ed25519 release key once one is configured, and
each binary carries a build provenance attestation
(`gh attestation verify <binary> --repo madkoding/motita`).

**Out of scope.** An attacker who already controls the user's account or
machine, a model provider that is itself malicious, and commands the user
explicitly approves.
