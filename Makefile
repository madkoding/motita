# motita — build, verification and cross-compilation.
#
# VERSION can be overridden: make VERSION=v1.0.0 dist
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
GO        ?= go
# The container runtime for the e2e targets: docker when its daemon answers, else podman.
# CONTAINER_RUNTIME=podman forces one. See scripts/container-runtime.sh.
CTR ?= $(shell sh -c '. ./scripts/container-runtime.sh; echo $$CTR')

DIST      := dist
COVERPKG  := ./...
# Packages with tests (the coverage report walks them one by one).
PKGS      := ./internal/... ./cmd/... ./tools/...
# The linter CI runs. It is PINNED, and it is run on the go.mod toolchain
# (GOTOOLCHAIN=go1.26.0): no published staticcheck parses the export data of a
# much newer Go, and running it on this machine's Go reports "internal error in
# importing internal/byteorder" instead of the findings. Without this step,
# `make check` says "what CI runs" and is not: measured, a S1030 in
# checkpoints_test.go passed here and failed there.
STATICCHECK    := honnef.co/go/tools/cmd/staticcheck@v0.6.1
STATICCHECK_GO ?= go1.26.0

# Every platform Go can build cmd/agent for. windows/arm, darwin/386 and
# darwin/arm do not exist in Go: the toolchain refuses them, so they are not listed.
PLATFORMS := linux/386 linux/amd64 linux/arm linux/arm64 \
             windows/386 windows/amd64 windows/arm64 \
             darwin/amd64 darwin/arm64

# Running plain `make` shows the list of commands instead of silently starting a release check.
.DEFAULT_GOAL := help

.PHONY: help web build dist verify-dist test test-matrix vet bench fmt fmt-check check cover \
        staticcheck hooks run smoke e2e e2e-agent e2e-gateway clean test-release release-dry-run release-key

test-release: ## Check that commit messages produce the right version number
	@echo "▶ Checking that each kind of commit message bumps the version correctly…"
	@./scripts/test-release-bump.sh && echo "✔ Version bumps behave as expected." || { echo "✖ A commit type maps to the wrong version bump (details above). The rules live in .releaserc.json."; exit 1; }

release-dry-run: ## Show which version would be published next (publishes nothing)
	@echo "▶ Working out the next version (nothing is published)…"
	@./scripts/release-dry-run.sh

release-key: ## Generate the release signing key pair (once; prints where each half goes)
	@./scripts/release-signing-key.sh

help: ## Show this list of commands
	@printf '\nmotita — what you can run here\n\n'
	@printf 'Start with these three:\n'
	@printf '  \033[36m%-16s\033[0m %s\n' 'make check' 'Everything CI will check, in one go. Run it before you push.'
	@printf '  \033[36m%-16s\033[0m %s\n' 'make cover' 'How much of the code the tests exercise (goal: 100% per package).'
	@printf '  \033[36m%-16s\033[0m %s\n' 'make build' 'Build motita for this computer (result: dist/motita).'
	@printf '\nAll commands:\n'
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@printf '\nEvery command prints what it is doing, whether it worked, and what to do if it did not.\n\n'

web: ## Build the web interface (needed before building motita)
	@echo "▶ Building the web interface (installs its packages first)…"
	@cd web && npm ci && npm run build && echo "✔ Web interface built." || { echo "✖ The web interface did not build. Is Node.js installed? Read the first error above."; exit 1; }

build: web ## Build motita for this computer (result: dist/motita)
	@echo "▶ Compiling motita for this computer…"
	@$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/motita ./cmd/agent && echo "✔ Built $(DIST)/motita ($(VERSION))." || { echo "✖ The build failed. Fix the compile error above, then run: make build"; exit 1; }

dist: web ## Build motita for every supported system (Linux, Windows, macOS)
	@echo "▶ Compiling motita for every supported system…"
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/motita-$$os-$$arch$$ext ./cmd/agent || exit 1; \
		printf '  %-24s %s\n' "$$os/$$arch" "ok"; \
	done
	@$(MAKE) --no-print-directory verify-dist

verify-dist: ## Check each built file really is for the system its name says
# The three systems use three different container formats, and a wrong
# cross-compile is invisible until the binary is run somewhere else:
#   ELF    (linux)    7f 45 4c 46, fifth byte 01 = 32-bit, 02 = 64-bit
#   PE     (windows)  4d 5a ("MZ")
#   Mach-O (darwin)   cf fa ed fe (64-bit little endian) or ce fa ed fe (32-bit)
	@fail=0; \
	for f in $(DIST)/motita-linux-* $(DIST)/motita-darwin-* $(DIST)/motita-windows-*.exe; do \
		[ -f "$$f" ] || continue; \
		case "$$f" in \
			*windows*) \
				hdr=$$(head -c 2 "$$f" | od -An -tx1 | tr -d ' \n'); \
				[ "$$hdr" = "4d5a" ] || { echo "ERROR: $$f is not a PE executable ($$hdr)"; fail=1; }; \
				;; \
			*darwin*) \
				hdr=$$(head -c 4 "$$f" | od -An -tx1 | tr -d ' \n'); \
				[ "$$hdr" = "cffaedfe" ] || { echo "ERROR: $$f is not a 64-bit Mach-O ($$hdr)"; fail=1; }; \
				;; \
			*) \
				want="7f454c4602"; \
				case "$$f" in *-386|*-arm|*-linux-arm) want="7f454c4601";; esac; \
				hdr=$$(head -c 5 "$$f" | od -An -tx1 | tr -d ' \n'); \
				[ "$$hdr" = "$$want" ] || { echo "ERROR: $$f has class $$hdr, expected $$want"; fail=1; }; \
				;; \
		esac; \
	done; \
	[ "$$fail" = "0" ] && echo "✔ Every built file matches its system (ELF / PE / Mach-O)." || { echo "✖ A built file is for the wrong system (listed above). Run: make clean && make dist"; exit 1; }

test: ## Run all the tests
	@echo "▶ Running the tests (this can take a few minutes)…"
	@GIT_CONFIG_NOSYSTEM=1 $(GO) test -count=1 ./... && echo "✔ All tests passed." || { echo "✖ Some tests failed. Look for the first FAIL above. To rerun one package: go test ./internal/<package>"; exit 1; }

test-matrix: ## Check the tests compile on every supported system
	@echo "▶ Checking the tests compile on every supported system…"
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=$$(GOOS=$$os GOARCH=$$arch $(GO) test -c -o /dev/null ./... 2>&1 | grep -vE '^$$|no test files' | head -2); \
		if [ -z "$$out" ]; then printf '  %-24s %s\n' "$$os/$$arch" "ok"; \
		else printf '  %-24s %s\n' "$$os/$$arch" "FAILS TO BUILD"; echo "$$out" | sed 's/^/      /'; exit 1; fi; \
	done

vet: ## Look for suspicious code (go vet)
	@echo "▶ Looking for suspicious code (go vet)…"
	@$(GO) vet ./... && echo "✔ No suspicious code found." || { echo "✖ go vet found problems (listed above). Fix each line it names, then run: make vet"; exit 1; }

bench: ## Measure how fast the screen redraws while typing
	@echo "▶ Measuring how fast the screen redraws on each keystroke…"
	@$(GO) test -run '^$$' -bench . -benchmem ./internal/tui/

fmt: ## Fix the code formatting automatically
	@echo "▶ Formatting the code…"
	@changed="$$($(GO) fmt ./...)"; \
		if [ -n "$$changed" ]; then echo "  reformatted:"; echo "$$changed" | sed 's/^/    /'; else echo "  nothing needed changing"; fi; \
		echo "✔ Code is formatted."

fmt-check: ## Check the formatting only, change nothing
	@echo "▶ Checking the code formatting…"
	@bad="$$(gofmt -l .)"; \
		if [ -z "$$bad" ]; then echo "✔ Formatting is clean."; \
		else echo "✖ These files are not formatted:"; echo "$$bad" | sed 's/^/    /'; echo "  Fix it with: make fmt"; exit 1; fi

hooks: ## Turn on the git safety checks (format on commit, checks on push)
	@git config core.hooksPath .githooks && echo "✔ Git safety checks are on: formatting is checked on every commit, and formatting + go vet on every push."

check: fmt-check vet staticcheck test ## Run every check CI runs (do this before you push)
	@echo ""
	@echo "✔ All checks passed: formatting, code analysis, linter and tests. Next: make cover (the coverage gate)."

staticcheck: ## Run the extra linter that CI uses
	@echo "▶ Running the linter CI uses (the first run downloads it)…"
	@GOTOOLCHAIN=$(STATICCHECK_GO) $(GO) run $(STATICCHECK) ./... && echo "✔ The linter found nothing." || { echo "✖ The linter reported the problems above. Each line is file:line: what is wrong (rule code)."; exit 1; }

cover: ## Show how much of the code the tests exercise (goal: 100% per package)
	@echo "▶ Measuring how much code the tests exercise. Each line is a package and the share of its code the tests run; the goal is 100.0%."
	@if [ "$$(id -u)" = "0" ]; then echo "note: running as root: tests that need a permission error skip themselves, so a package may read below 100% here; run as a normal user for the real figure"; fi
	@$(GO) list $(PKGS) | while read -r pkg; do \
		out=$$($(GO) test -count=1 -cover $$pkg 2>/dev/null | grep -oE 'coverage: [0-9.]+%'); \
		[ -z "$$out" ] && out='(no test files)'; \
		printf '  %-52s %s\n' "$$pkg" "$$out"; \
	done
	@printf '  %-52s' aggregate; \
		$(GO) test -coverpkg=$(COVERPKG) -coverprofile=coverage.out -covermode=atomic ./... >/dev/null; \
		$(GO) tool cover -func=coverage.out | tail -1 | awk '{print $$3}'
	@echo "  Anything below 100.0% needs a test for the lines it does not run (see CONTRIBUTING.md, rule 1)."

run: ## Start motita's interactive screen
	@echo "▶ Starting motita…"
	@$(GO) run ./cmd/agent $(ARGS)

smoke: dist ## Start each Linux build inside a container to prove it runs
	@echo "▶ Starting each Linux build inside its own container…"
	@for p in linux/386 linux/amd64 linux/arm linux/arm64; do \
		arch=$${p#*/}; image="$${arch}/debian:bookworm-slim"; pl="linux/$$arch"; \
		case "$$arch" in i386) ;; 386) image="i386/debian:bookworm-slim";; esac; \
		case "$$arch" in amd64) image="debian:bookworm-slim";; esac; \
		case "$$arch" in arm) image="arm32v7/debian:bookworm-slim"; pl="linux/arm/v7";; esac; \
		case "$$arch" in arm64) image="arm64v8/debian:bookworm-slim";; esac; \
		printf '  %-12s ' "$$arch"; \
		$(CTR) run --rm --platform "$$pl" -v "$(CURDIR)/$(DIST):/t:ro" "$$image" \
			sh -c "/t/motita-linux-$$arch -version" || exit 1; \
	done

e2e: ## Full test of the whole program in a container (default: 32-bit)
	./scripts/e2e.sh $${ARCH:-386}

e2e-agent: ## Full test of the agent loop in a container (default: 32-bit)
	./scripts/e2e-agent.sh $${ARCH:-386}

e2e-gateway: ## Full test of the web/API gateway in a container (default: 64-bit)
	./scripts/e2e-gateway.sh $${ARCH:-amd64}

clean: ## Delete everything the builds and tests generated
	@rm -rf $(DIST) coverage.out .e2e && echo "✔ Removed $(DIST)/, coverage.out and .e2e/."
