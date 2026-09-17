# starlight — build, verification and cross-compilation.
#
# VERSION can be overridden: make VERSION=v1.0.0 dist
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
GO        ?= go
DIST      := dist
COVERPKG  := ./...
# Packages with tests (the coverage report walks them one by one).
PKGS      := ./internal/... ./cmd/... ./tools/...

# Every platform Go can build these two programs for. windows/arm, darwin/386 and
# darwin/arm do not exist in Go: the toolchain refuses them, so they are not listed.
PLATFORMS := linux/386 linux/amd64 linux/arm linux/arm64 \
             windows/386 windows/amd64 windows/arm64 \
             darwin/amd64 darwin/arm64

.PHONY: help build dist dist-agent dist-chat test vet fmt fmt-check check cover \
        clean run smoke e2e e2e-agent test-matrix

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build both programs for the host architecture
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight ./cmd/chat
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-agent ./cmd/agent

dist: ## Build both programs for every supported platform
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/starlight-agent-$$os-$$arch$$ext ./cmd/agent || exit 1; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/starlight-$$os-$$arch$$ext ./cmd/chat || exit 1; \
		printf '  %-24s %s\n' "$$os/$$arch" "ok"; \
	done
	@$(MAKE) --no-print-directory verify-dist

dist-agent: ## Build the agent for every supported platform
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/starlight-agent-$$os-$$arch$$ext ./cmd/agent || exit 1; \
	done

dist-chat: ## Build the chat for every supported platform
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/starlight-$$os-$$arch$$ext ./cmd/chat || exit 1; \
	done

verify-dist: ## Check every built binary really is what its name says
# The three systems use three different container formats, and a wrong
# cross-compile is invisible until the binary is run somewhere else:
#   ELF    (linux)    7f 45 4c 46, fifth byte 01 = 32-bit, 02 = 64-bit
#   PE     (windows)  4d 5a ("MZ")
#   Mach-O (darwin)   cf fa ed fe (64-bit little endian) or ce fa ed fe (32-bit)
	@fail=0; \
	for f in $(DIST)/starlight-agent-* $(DIST)/starlight-linux-* $(DIST)/starlight-darwin-* $(DIST)/starlight-windows-*.exe; do \
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
	[ "$$fail" = "0" ] && echo "  every binary matches its platform (ELF / PE / Mach-O)"

test: ## Run the tests
	$(GO) test -count=1 ./...

test-matrix: ## Check the tests build for every supported platform
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=$$(GOOS=$$os GOARCH=$$arch $(GO) test -c -o /dev/null ./... 2>&1 | grep -vE '^$$|no test files' | head -2); \
		if [ -z "$$out" ]; then printf '  %-24s %s\n' "$$os/$$arch" "ok"; \
		else printf '  %-24s %s\n' "$$os/$$arch" "FAILS TO BUILD"; echo "$$out" | sed 's/^/      /'; exit 1; fi; \
	done

vet: ## Static analysis
	$(GO) vet ./...

fmt: ## Format the code
	$(GO) fmt ./...

fmt-check: ## Fail if any file is unformatted
	@test -z "$$(gofmt -l . )" || { echo "Unformatted:"; gofmt -l .; exit 1; }

check: fmt-check vet test ## Full verification (what CI runs)

cover: ## Coverage per package (the gate is 100%) and aggregate
	@$(GO) list $(PKGS) | while read -r pkg; do \
		out=$$($(GO) test -count=1 -cover $$pkg 2>/dev/null | grep -oE 'coverage: [0-9.]+%'); \
		[ -z "$$out" ] && out='(no test files)'; \
		printf '  %-52s %s\n' "$$pkg" "$$out"; \
	done
	@printf '  %-52s' aggregate; \
		$(GO) test -coverpkg=$(COVERPKG) -coverprofile=coverage.out -covermode=atomic ./... >/dev/null; \
		$(GO) tool cover -func=coverage.out | tail -1 | awk '{print $$3}'

run: ## Run the chat in interactive mode
	$(GO) run ./cmd/chat $(ARGS)

smoke: dist ## Run the linux binaries inside their own container
	@for p in linux/386 linux/amd64 linux/arm linux/arm64; do \
		arch=$${p#*/}; image="$${arch}/debian:bookworm-slim"; pl="linux/$$arch"; \
		case "$$arch" in i386) ;; 386) image="i386/debian:bookworm-slim";; esac; \
		case "$$arch" in amd64) image="debian:bookworm-slim";; esac; \
		case "$$arch" in arm) image="arm32v7/debian:bookworm-slim"; pl="linux/arm/v7";; esac; \
		case "$$arch" in arm64) image="arm64v8/debian:bookworm-slim";; esac; \
		printf '  %-12s ' "$$arch"; \
		docker run --rm --platform "$$pl" -v "$(CURDIR)/$(DIST):/t:ro" "$$image" \
			sh -c "/t/starlight-agent-linux-$$arch -version" || exit 1; \
	done

e2e: ## End-to-end test of the chat (default: i386)
	./scripts/e2e.sh $${ARCH:-386}

e2e-agent: ## End-to-end test of the agent (default: i386)
	./scripts/e2e-agent.sh $${ARCH:-386}

clean: ## Remove the artifacts
	rm -rf $(DIST) coverage.out .e2e
