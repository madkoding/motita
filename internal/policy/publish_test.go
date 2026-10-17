package policy

import "testing"

// A local build tool's ordinary verbs are work in the workspace, but its publish, deploy and
// install verbs reach outside it, like `npm publish` and `pip install` already did. So does
// running a package fetched by name (`npm exec`, `go run pkg@version`).
func TestPublishAndRemoteVerbsOfTheLocalToolsAreAsked(t *testing.T) {
	m := Mode{Enforce: true}
	for _, line := range []string{
		"uv publish", "uv add requests", "uv pip install requests", "uv tool install ruff",
		"poetry publish", "poetry add requests", "poetry self add x",
		"mvn deploy", "mvn clean deploy", "mvn -q release:perform",
		"gradle publish", "gradle clean publishToMavenLocal", "gradle :lib:publish",
		"dotnet nuget push x.nupkg", "dotnet tool install -g x",
		"npm exec cowsay", "go run github.com/x/y@latest",
	} {
		if d := m.DecideLine(line, "/tmp/w"); d.Verdict != Ask {
			t.Errorf("%q = %v (%s: %s), want ask", line, d.Verdict, d.Rule, d.Reason)
		}
	}
	for _, line := range []string{
		"uv run pytest", "poetry run pytest", "mvn test", "mvn clean package",
		"gradle build", "gradle :lib:test", "dotnet build", "dotnet test", "dotnet publish -o out",
		"go run ./cmd/x", "go run .", "npm run build", "npm exec vitest", "uv pip list", "uv pip show x",
		// What the manifest declares is still the project's own install.
		"uv sync", "poetry install",
	} {
		if d := m.DecideLine(line, "/tmp/w"); d.Verdict != Allow {
			t.Errorf("%q = %v (%s: %s), want allow", line, d.Verdict, d.Rule, d.Reason)
		}
	}
}
