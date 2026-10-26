package readonly

import (
	"strings"
	"testing"
)

// check runs a whole line through the tokeniser and the policy, the way plan mode does for one step.
func check(t *testing.T, line string) Decision {
	t.Helper()
	name, args, err := SplitCommand(line)
	if err != nil {
		t.Fatalf("%q does not split: %v", line, err)
	}
	return Check(name, args)
}

// Reads a plan was refused and needs: each one must now be allowed, and the writing form of the
// same program must still be refused.
func TestPlanReadsAreAllowedAndTheirWritingFormsAreNot(t *testing.T) {
	allowed := []string{
		"git fetch", "git fetch origin", "git fetch --all --prune", "git fetch origin main",
		"git fetch -p", "git fetch -pt origin", "git fetch -j4 --all", "git fetch --depth 1 origin",
		"git fetch --dep=1", "git fetch -- origin", "git -C sub fetch",
		"gh pr view 12", "gh pr list --state open", "gh pr diff 3", "gh pr checks 3", "gh pr status",
		"gh issue view 4", "gh run list", "gh run view 5 --log", "gh repo view", "gh release list",
		"gh search code foo", "gh auth status", "gh label list", "gh workflow list",
		"gh api repos/x/y", "gh api repos/x/y -X GET", "gh api repos/x/y --method=get",
		"gh api repos/x/y -XGET", "gh api repos/x/y --paginate",
		"npm ls", "npm list --depth=0", "npm explain left-pad", "pnpm why x", "pnpm ls",
		"yarn list", "yarn why x", "pip list", "pip3 freeze", "pip show requests", "pip check",
		"npm --version", "yarn -v", "pnpm --version", "pip --version",
		"python3 --version", "python -V", "node -v", "java -version", "ruby --version", "php -v",
		"mvn -v", "gradle --version", "dotnet --info", "dotnet --list-sdks", "dotnet --version",
		"pwd", "sleep 1", "pgrep -a sshd", "nm a.o", "objdump -d a.o", "readelf -h a.out", "lsattr f",
		"getfacl f", "zcat f.gz", "zgrep x f.gz", "fd name", "fd -e go -H", "fdfind name",
		"go mod graph", "go mod why x", "go mod verify",
		"kubectl config get-contexts", "kubectl config current-context", "kubectl auth can-i get pods",
		"kubectl rollout status deploy/x", "kubectl rollout history deploy/x", "kubectl events",
		"kubectl -n kube-system get pods", "kubectl --namespace x get pods", "kubectl -A get pods",
		"docker context ls", "docker events", "podman context inspect x",
	}
	for _, line := range allowed {
		if d := check(t, line); !d.Allowed {
			t.Errorf("%q only reads and must be allowed, got refused: %s", line, d.Reason)
		}
	}

	refused := []string{
		"git fetch --upload-pack=evil origin", "git fetch -u origin", "git fetch --update-head-ok",
		"git fetch --set-upstream origin", "git fetch origin main:main", "git fetch ext::sh",
		"git fetch https://example.com/x.git", "git fetch -x", "git fetch --no-such-option",
		"git pull", "git push", "git fetch --up=x",
		"gh pr merge 1", "gh pr view 1 --web", "gh pr view 1 -w", "gh issue create", "gh pr",
		"gh repo delete", "gh extension install x", "gh run rerun 1", "gh auth login",
		"gh api repos/x/y -X POST", "gh api repos/x/y --method DELETE", "gh api repos/x/y -XPATCH",
		"gh api repos/x/y -f a=b", "gh api repos/x/y -fa=b", "gh api repos/x/y -F a=b",
		"gh api repos/x/y --field a=b", "gh api repos/x/y --input f", "gh api graphql",
		"npm install", "npm run build", "npm publish", "npm", "yarn add x", "pnpm install", "pip install x",
		"pip uninstall x", "pip3 download x", "pip",
		"python3 -c 1", "python3 script.py", "node app.js", "node", "java -jar x.jar", "mvn install",
		"dotnet run", "dotnet build", "dotnet --version x", "ruby -e 1",
		"fd -x rm", "fd --exec rm", "fd -X rm", "fd --exec-batch rm", "fd -HX rm",
		"go mod tidy", "go mod download", "go mod",
		"kubectl config use-context x", "kubectl config set x y", "kubectl auth reconcile -f x",
		"kubectl rollout restart deploy/x", "kubectl delete pod x", "kubectl",
		"docker context rm x", "docker context use x",
	}
	for _, line := range refused {
		if d := check(t, line); d.Allowed {
			t.Errorf("%q can change something and must be refused (%s)", line, d.Reason)
		}
	}
}

// A wrapper in front of a plan-only read is judged by the program behind it.
func TestPlanReadsThroughAWrapper(t *testing.T) {
	if d := check(t, "timeout 20 git fetch origin"); !d.Allowed {
		t.Errorf("a wrapped fetch must be allowed: %s", d.Reason)
	}
	if d := check(t, "timeout 20 git push"); d.Allowed {
		t.Error("a wrapped push must be refused")
	}
}

// The reads only plan mode allows must stay out of Classify: the wider policy keeps its own answer.
func TestPlanReadsAreNotReadersForTheWiderPolicy(t *testing.T) {
	for _, line := range []string{"git fetch", "gh pr view 1", "node --version"} {
		name, args, _ := SplitCommand(line)
		if kind, _ := Classify(name, args); kind == KindReader {
			t.Errorf("%q must not be a silent reader outside plan mode", line)
		}
	}
}

func TestPlanReadDoesNotKnowOtherCommands(t *testing.T) {
	if _, _, ok := PlanRead("ls", nil); ok {
		t.Error("ls is not a plan-only read")
	}
	if _, _, ok := PlanRead("git", []string{"-c", "x=y", "fetch"}); ok {
		t.Error("an unchecked git option must fall through to the ordinary refusal")
	}
	if _, _, ok := PlanRead("git", nil); ok {
		t.Error("git with no subcommand is not a fetch")
	}
	if _, _, ok := PlanRead("git", []string{"log"}); ok {
		t.Error("git log is not a plan-only read")
	}
	if got := baseOf(`C:\bin\git.exe`); got != "git.exe" {
		t.Errorf("baseOf = %q", got)
	}
}

func TestVerbSetMessagesAreStable(t *testing.T) {
	d := check(t, "npm install")
	if !strings.Contains(d.Reason, "bin, explain, la, list, ll, ls, prefix, root, why") {
		t.Errorf("the reason should list the reporting verbs, got %q", d.Reason)
	}
	if got := joinKeys(map[string]bool{"b": true, "a": true, "c": true}); got != "a, b, c" {
		t.Errorf("joinKeys = %q", got)
	}
}
