# starlight — build, verification and cross-compilation (i386 included).
#
# VERSION can be overridden: make VERSION=v1.0.0 386
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
GO        ?= go
DIST      := dist
COVERPKG  := ./...
# Packages with tests (the coverage report walks them one by one).
PKGS      := ./internal/... ./cmd/... ./tools/...

.PHONY: help build agent agent-386 386 amd64 arm64 all test vet fmt fmt-check check cover clean run smoke-386 e2e e2e-agent

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build for the host architecture
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight ./

386: ## Build the chat for linux/386 (cmd/chat)
	GOOS=linux GOARCH=386 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-linux-386 ./cmd/chat
	@$(MAKE) --no-print-directory verify-386

agent: ## Build the 3-layer agent for the host architecture
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-agent ./cmd/agent

agent-386: ## Build the 3-layer agent for linux/386
	GOOS=linux GOARCH=386 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-agent-386 ./cmd/agent
	@header=$$(head -c 5 $(DIST)/starlight-agent-386 | od -An -tx1 | tr -d ' \n'); \
		[ "$$header" = "7f454c4601" ] || { echo "ERROR: the i386 agent is not ELFCLASS32"; exit 1; }; \
		echo "  OK: agent ELFCLASS32 (i386)"

verify-386: ## Check the binary is a 32-bit ELF (without relying on `file`)
	@header=$$(head -c 5 $(DIST)/starlight-linux-386 | od -An -tx1 | tr -d ' \n'); \
		if [ "$$header" != "7f454c4601" ]; then \
			echo "ERROR: $(DIST)/starlight-linux-386 is not a 32-bit ELF (header: $$header)"; \
			exit 1; \
		fi; \
		echo "  OK: ELFCLASS32 (i386) — header $$header"
	@$(GO) version -m $(DIST)/starlight-linux-386 2>/dev/null | head -2 | sed 's/^/  /'

amd64: ## Build for linux/amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-linux-amd64 ./cmd/chat

arm64: ## Build for linux/arm64 (Raspberry Pi, etc.)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-linux-arm64 ./cmd/chat

all: 386 386-amd64 386-arm64 agent-386 ## Build the static binaries

386-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-agent-linux-amd64 ./cmd/agent

386-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-agent-linux-arm64 ./cmd/agent

test: ## Run the tests
	$(GO) test -count=1 ./...

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

smoke-386: 386 agent-386 ## Run the i386 binaries inside a 32-bit container
	docker run --rm --platform linux/386 -v $(CURDIR)/$(DIST):/t:ro i386/debian:bookworm-slim sh -c '/t/starlight-linux-386 -version; /t/starlight-agent-386 -version'

e2e: ## End-to-end test of the chat in an i386 container
	./scripts/e2e-i386.sh

e2e-agent: ## End-to-end test of the agent in an i386 container
	./scripts/e2e-agent-i386.sh

clean: ## Remove the artifacts
	rm -rf $(DIST) coverage.out
