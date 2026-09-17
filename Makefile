# starlight — build, verificación y compilación cruzada (incluido i386).
#
# VERSION se puede sobreescribir: make VERSION=v1.0.0 386
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
GO        ?= go
DIST      := dist

.PHONY: help build agent agent-386 386 amd64 arm64 all test vet fmt fmt-check check clean run smoke-386 e2e-agente

help: ## Muestra esta ayuda
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Compila para la arquitectura anfitriona
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight .

386: ## Compila el chat i386 (cmd/chat) — linux/386
	GOOS=linux GOARCH=386 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-linux-386 ./cmd/chat
	@$(MAKE) --no-print-directory verificar-386

agent: ## Compila el agente de 3 capas para la arquitectura anfitriona
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-agent ./cmd/agent

agent-386: ## Compila el agente de 3 capas para linux/386 — linux/386
	GOOS=linux GOARCH=386 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-agent-386 ./cmd/agent
	@clase=$$(head -c 5 $(DIST)/starlight-agent-386 | od -An -tx1 | tr -d ' \n'); \
		[ "$$clase" = "7f454c4601" ] || { echo "ERROR: el agente i386 no es ELFCLASS32"; exit 1; }; \
		echo "  OK: agente ELFCLASS32 (i386)"

verificar-386: ## Comprueba que el binario es ELF de 32 bits (sin depender de `file`)
	@clase=$$(head -c 5 $(DIST)/starlight-linux-386 | od -An -tx1 | tr -d ' \n'); \
		if [ "$$clase" != "7f454c4601" ]; then \
			echo "ERROR: $(DIST)/starlight-linux-386 no es un ELF de 32 bits (cabecera: $$clase)"; \
			exit 1; \
		fi; \
		echo "  OK: ELFCLASS32 (i386) — cabecera $$clase"
	@$(GO) version -m $(DIST)/starlight-linux-386 2>/dev/null | head -2 | sed 's/^/  /'

amd64: ## Compila linux/amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-linux-amd64 .

arm64: ## Compila linux/arm64 (Raspberry Pi, etc.)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/starlight-linux-arm64 .

all: 386 agent-386 amd64 arm64 ## Compila los binarios estáticos

test: ## Ejecuta las pruebas
	$(GO) test -count=1 ./...

vet: ## Análisis estático
	$(GO) vet ./...

fmt: ## Formatea el código
	$(GO) fmt ./...

fmt-check: ## Falla si hay código sin formatear
	@test -z "$$(gofmt -l . )" || { echo "Sin formatear:"; gofmt -l .; exit 1; }

check: fmt-check vet test ## Verificación completa (lo mismo que corre la CI)

run: ## Ejecuta el agente en modo interactivo
	$(GO) run . $(ARGS)

smoke-386: 386 agent-386 ## Prueba los binarios i386 dentro de un contenedor de 32 bits
	docker run --rm --platform linux/386 -v $(CURDIR)/$(DIST):/t:ro i386/debian:bookworm-slim sh -c '/t/starlight-linux-386 -version; /t/starlight-agent-386 -version'

e2e-agente: ## Prueba de extremo a extremo del agente en un contenedor i386
	./scripts/e2e-agente-i386.sh

clean: ## Borra los artefactos
	rm -rf $(DIST)
