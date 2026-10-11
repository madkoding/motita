package policy

import (
	"fmt"
	"slices"
	"strings"
)

// ---------------------------------------------------------------------------
// The project's own tooling
// ---------------------------------------------------------------------------

// devTools are the programs `npx` and its kin run that belong to a project's own toolchain. Any
// OTHER package `npx` would fetch is unreviewed code from the network, and is still asked about.
var devTools = map[string]bool{
	"vitest": true, "jest": true, "tsc": true, "eslint": true, "prettier": true, "next": true,
	"vite": true, "tsx": true, tsNode: true, "mocha": true, "playwright": true, "cypress": true,
	"biome": true, "stylelint": true, "turbo": true, "nx": true, "webpack": true, "rollup": true,
	"esbuild": true, "svelte-check": true, "vue-tsc": true, "astro": true, "nuxt": true, "c8": true,
	"nyc": true, "ava": true, "tap": true, "knip": true, "depcheck": true, "prisma": true,
	"drizzle-kit": true, "tailwindcss": true, "postcss": true, "sass": true, "storybook": true,
	"expo": true, "ng": true, "remix": true,
}

// projectToolDecision allows two families of command that every project needs and that the
// default classification would ask about:
//
//   - a dependency install that takes its list FROM THE MANIFEST: `npm ci`, `npm install`,
//     `yarn`, `pnpm install`, `pip install -r requirements.txt`, `pip install -e .`,
//     `go mod download`, `bundle install`, `composer install`, `poetry install`, `uv sync`. What
//     gets installed is what the project already declares, and stays in the project. Naming a
//     package (`npm install left-pad`), a global install (`-g`) and anything with a privilege
//     wrapper are not this and still go to the user.
//   - `npx` (and `pnpm exec`) running one of the project's own dev tools.
func projectToolDecision(name string, args []string) (Decision, bool) {
	positional, global, fromFile := installArgs(args)
	if global {
		return Decision{}, false
	}
	what, ok := projectToolCommand(name, positional, fromFile)
	if !ok {
		return Decision{}, false
	}
	return Decision{Allow, fmt.Sprintf("%s installs or runs what the project itself declares", what),
		"project-tooling", false}, true
}

// globalInstallFlags are the options that send an install outside the project, or fetch it from
// somewhere other than the project's own registry; globalInstallPrefixes are the same options
// written with their value glued (`--registry=URL`).
var (
	globalInstallFlags = map[string]bool{
		"-g": true, "--global": true, "--prefix": true, "--user": true, "--target": true,
		"--registry": true, "--index-url": true, "--extra-index-url": true, "-i": true,
	}
	globalInstallPrefixes = []string{"--registry=", "--index-url=", "--extra-index-url=", "--prefix="}
)

// manifestFileFlags are the options whose value is a file or a path in the project, not a
// package name; the value says whether that file is the list of what is installed (-r, -e) or
// only constrains it (-c).
var manifestFileFlags = map[string]bool{
	"-r": true, "--requirement": true, "-e": true, "--editable": true, "-c": false, "--constraint": false,
}

// installArgs reads the arguments of an install: its positional words, whether it is global (or
// from another registry), and whether its list comes from a file in the project.
func installArgs(args []string) (positional []string, global, fromFile bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		_, takesFile := manifestFileFlags[a]
		switch {
		case isGlobalInstallFlag(a):
			global = true
		case takesFile:
			fromFile = manifestFileFlags[a]
			i++
		case strings.HasPrefix(a, "-r") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			fromFile = true
		case !strings.HasPrefix(a, "-"):
			positional = append(positional, a)
		default:
			// Another option: it does not change what is installed or where.
		}
	}
	return positional, global, fromFile
}

// isGlobalInstallFlag reports an option that sends an install outside the project.
func isGlobalInstallFlag(a string) bool {
	return globalInstallFlags[a] ||
		slices.ContainsFunc(globalInstallPrefixes, func(prefix string) bool { return strings.HasPrefix(a, prefix) })
}

// projectToolCommand reports whether a program, with these positional words, installs what the
// project's manifest declares or runs one of its dev tools, and names it for the reason.
func projectToolCommand(name string, positional []string, fromFile bool) (string, bool) {
	sub := ""
	if len(positional) > 0 {
		sub = positional[0]
	}
	n := len(positional)
	switch name {
	case "npm":
		return "npm", sub == "ci" || sub == "clean-install" || (isInstallVerb(sub) && n == 1)
	case "yarn":
		return "yarn", n == 0 || (sub == "install" && n == 1)
	case "pnpm":
		return pnpmCommand(sub, positional)
	case "bun":
		return "bun", isInstallVerb(sub) && n == 1
	case "pip", "pip3":
		return "pip", sub == "install" && pipFromManifest(positional, fromFile)
	case "go":
		return "go mod", sub == "mod" && n == 2 && goModVerbs[positional[1]]
	case "bundle", "composer", "poetry":
		return name, sub == "install" && n == 1
	case "uv":
		return "uv", sub == "sync" && n == 1
	case "cargo":
		return "cargo", sub == "fetch" && n == 1
	case "npx":
		return npxCommand(positional)
	default:
		return "", false
	}
}

// goModVerbs are the `go mod` subcommands that only fetch or check what go.mod declares.
var goModVerbs = map[string]bool{"download": true, "tidy": true, "verify": true}

// isInstallVerb reports the install subcommand of the JavaScript package managers.
func isInstallVerb(sub string) bool {
	return sub == "install" || sub == "i"
}

// pnpmCommand decides `pnpm install` and `pnpm exec <dev tool>`.
func pnpmCommand(sub string, positional []string) (string, bool) {
	if isInstallVerb(sub) && len(positional) == 1 {
		return "pnpm", true
	}
	if sub == "exec" && len(positional) >= 2 && devTools[baseName(positional[1])] {
		return "pnpm exec", true
	}
	return "", false
}

// pipFromManifest reports a `pip install` of a requirements file or of the project itself.
func pipFromManifest(positional []string, fromFile bool) bool {
	return (len(positional) == 1 && fromFile) || (len(positional) == 2 && positional[1] == ".")
}

// npxCommand decides `npx --no-install tool`, `npx -y tool`, `npx tool args`: the first non-flag
// is the tool.
func npxCommand(positional []string) (string, bool) {
	if len(positional) > 0 && devTools[baseName(positional[0])] {
		return "npx " + baseName(positional[0]), true
	}
	return "", false
}
