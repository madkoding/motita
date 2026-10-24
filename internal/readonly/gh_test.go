package readonly

import "testing"

// TestGhReadsPullRequests: the subcommands a coding agent needs to look at a pull request
// and its checks are allowed.
func TestGhReadsPullRequests(t *testing.T) {
	for _, args := range [][]string{
		{"pr", "list"},
		{"pr", "view"},
		{"pr", "view", "12"},
		{"pr", "checks"},
		{"pr", "diff"},
		{"pr", "status"},
		{"issue", "list"},
		{"run", "list"},
		{"run", "view", "123"},
		{"search", "prs", "coerce"},
		{"api", "repos/o/r/pulls"},
		{"api", "--method", "GET", "repos/o/r/pulls"},
		{"status"},
		{"--version"},
		// The subcommands added when the table was reviewed for the read-only verbs a
		// coding agent reaches for: keys, codespaces, attestations and the extension and
		// ruleset lookups.
		{"gpg-key", "list"},
		{"ssh-key", "list"},
		{"codespace", "list"},
		{"codespace", "logs"},
		{"attestation", "verify", "artifact.json"},
		{"extension", "search", "gh-copilot"},
		{"ruleset", "check", "main"},
	} {
		if d := Check("gh", args); !d.Allowed {
			t.Errorf("gh %v must be allowed, got %q", args, d.Reason)
		}
	}
}

// TestGhWritesAreRefused: gh is not a reader by name; its writing forms are refused.
func TestGhWritesAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"pr", "create"},
		{"pr", "merge", "12"},
		{"pr", "close", "12"},
		{"pr", "comment", "12"},
		{"issue", "create"},
		{"repo", "delete", "o/r"},
		{"api", "-X", "POST", "repos/o/r/issues"},
		{"api", "--method", "PATCH", "repos/o/r"},
		{"api", "-f", "title=x", "repos/o/r/issues"},
		{"api", "--input", "body.json", "repos/o/r/issues"},
		{"release", "create", "v1"},
		{"workflow", "run", "ci.yml"},
		{"auth", "login"},
		{"config", "set", "x", "y"},
		// The writing forms of the subcommands the review added: a key is added or
		// deleted, a codespace is created or entered, an attestation is downloaded, an
		// extension is installed, and a verb the table does not know is refused.
		{"gpg-key", "add", "key.gpg"},
		{"gpg-key", "delete", "1"},
		{"ssh-key", "add", "key.pub"},
		{"ssh-key", "delete", "1"},
		{"codespace", "create"},
		{"codespace", "delete", "-c", "x"},
		{"codespace", "ssh", "-c", "x"},
		{"attestation", "download", "repo"},
		{"extension", "install", "x"},
		{"extension", "remove", "x"},
		{"ruleset", "frobnicate"},
	} {
		if d := Check("gh", args); d.Allowed {
			t.Errorf("gh %v must be refused", args)
		}
	}
}

// TestGhAPIReadsWithGET: the method decides for `gh api`, not the subcommand. The same
// `api` reads with GET and changes GitHub with POST, so the pair (api, GET) is the one
// the allowlist has to answer: `--method GET` is allowed and `-X POST` is refused.
func TestGhAPIReadsWithGET(t *testing.T) {
	if d := Check("gh", []string{"api", "--method", "GET", "repos/o/r/pulls"}); !d.Allowed {
		t.Errorf("gh api --method GET must be allowed, got %q", d.Reason)
	}
	if d := Check("gh", []string{"api", "-X", "POST", "repos/o/r/issues"}); d.Allowed {
		t.Error("gh api -X POST must be refused")
	}
}

// TestGhUnknownSubcommandIsRefused: a subcommand not in the table is refused, not guessed.
func TestGhUnknownSubcommandIsRefused(t *testing.T) {
	if d := Check("gh", []string{"frobnicate"}); d.Allowed {
		t.Error("an unknown gh subcommand must be refused")
	}
}

// TestEveryGhReadingVerbIsAllowed: the table IS the policy, so every pair in it has to be
// a pair the rule allows. A verb added to the table that the rule cannot reach is a verb
// the agent cannot use, and this test names it instead of leaving it to be discovered.
func TestEveryGhReadingVerbIsAllowed(t *testing.T) {
	for sub, verbs := range ghReadingVerbs {
		if len(verbs) == 0 {
			t.Errorf("gh %s is in ghReadingVerbs with no verb: it can never be allowed", sub)
		}
		for verb := range verbs {
			if d := Check("gh", []string{sub, verb}); !d.Allowed {
				t.Errorf("gh %s %s is in ghReadingVerbs and must be allowed, got %q", sub, verb, d.Reason)
			}
		}
	}
}

// TestAnUnlistedCommandIsStillRefused: adding gh must not open the list.
func TestAnUnlistedCommandIsStillRefused(t *testing.T) {
	if d := Check("frobnicate", nil); d.Allowed {
		t.Error("a command not on the list must still be refused")
	}
}
