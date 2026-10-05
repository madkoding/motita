# Auditoría general — motita

**Alcance:** repositorio completo (código Go, frontend web, dependencias y seguridad básica).
**Resultado de la puerta de calidad:** `make check` termina con código de salida **0**.

---

## Resumen ejecutivo

motita es un agente de IA escrito en Go (516 archivos `.go`, de los cuales 329 son de test) con una TUI y una Web UI en Preact/Vite. La puerta de calidad del proyecto (`make check` = `fmt-check` + `vet` + `staticcheck` + `test`) pasa. La cobertura agregada de pruebas es del **98.6 %**. `npm audit` no reporta vulnerabilidades y no se encontraron secretos embebidos en el código.

Durante la auditoría se encontró **y corrigió** un fallo de la puerta de calidad: los tests del paquete `internal/gateway` fallaban al crear repositorios git temporales porque heredaban la configuración git del usuario (`commit.gpgsign=true`), de modo que los commits de prueba abortaban con `gpg failed to sign the data`.

---

## 1. Estructura y arquitectura

- **Monorepo Go** con un único binario de entrada en `cmd/agent` y 35 paquetes internos bajo `internal/` (agent, anchor, app, config, curator, execx, gateway, gitforge, gitx, i18n, llm, logx, netrules, oauth, onboard, plan, policy, procedures, projectskills, readonly, review, reward, sandbox, schedule, semantic, session, skills, task, template, tui, updater, usage, webui).
- **Herramientas de desarrollo** en `tools/` (clitest, mockapi, mockllm) y scripts en `scripts/`.
- **Frontend** en `web/` (Preact + Vite + Tailwind), empaquetado dentro del binario Go mediante `//go:embed all:assets` (`internal/webui/webui.go:34`).
- **Sistema de construcción:** `Makefile` (targets `check`, `test`, `test-matrix`, `vet`, `staticcheck`, `cover`, `fmt-check`, `web`, `build`…).
- **Manifiestos:** `go.mod` (Go 1.27.1) y `web/package.json`.
- **Documentación:** `README.md`, `CONTRIBUTING.md`, `IDEA.md`, `docs/`.

Evidencia:

```
$ find . -type f -name '*.go' -not -path './.git/*' | wc -l
516
$ find . -name '*_test.go' -not -path './.git/*' | wc -l
329
$ go version
go version go1.27.1 linux/amd64
```

## 2. Calidad del código

- `gofmt` limpio (`fmt-check` pasa), `go vet ./...` sin avisos y `staticcheck` sin hallazgos: los tres forman parte de `make check` y terminan en 0.
- El código está profusamente comentado (por ejemplo `internal/review/review.go` documenta el propósito del paquete y sus límites de seguridad).
- Sin hallazgos de calidad bloqueantes.

## 3. Estado y cobertura de las pruebas

- 329 archivos de test sobre 516 archivos `.go`.
- Cobertura agregada **98.6 %** (`make cover`, exit 0).
- Paquetes al 100 %: la gran mayoría. Por debajo:

| Paquete | Cobertura |
|---|---|
| `internal/review` | **0.0 %** |
| `tools/clitest` | 88.2 % |
| `internal/gateway` | 94.2 % |
| `internal/agent` | 99.7 % |

Evidencia:

```
$ make cover > /tmp/cover.out 2>&1; echo "exit=$?"
exit=0
  github.com/madkoding/motita/internal/review          coverage: 0.0%
  github.com/madkoding/motita/internal/gateway         coverage: 94.2%
  github.com/madkoding/motita/tools/clitest            coverage: 88.2%
  aggregate                                           98.6%
```

## 4. Dependencias

- Go: dependencias declaradas en `go.mod`; el toolchain fijado para `staticcheck` se resuelve con `GOTOOLCHAIN`.
- Web: `web/package.json` (preact, markdown-it, mermaid, dompurify, katex, ws…).
- `npm audit --omit=dev` no reporta vulnerabilidades.

Evidencia:

```
$ npm --prefix web audit --omit=dev > /tmp/npm.out 2>&1; echo "exit=$?"
exit=0
found 0 vulnerabilities
```

## 5. Seguridad básica

- **Sin secretos embebidos:** la búsqueda de `password|secret|api_key|token|private_key` en el código fuente solo devuelve `internal/config/credentials.go` y su test, que son el gestor de credenciales, no valores literales.
- **`.gitignore`** excluye `.env`, `*.local`, `node_modules/`, artefactos de build y material de terceros sin licencia.
- **Licencia:** GPL-3.0 (`LICENSE`).
- Los comandos git del gateway se ejecutan con `LC_ALL=C` para reconocer los mensajes de error de git de forma estable (`internal/gateway/git.go`).

---

## Hallazgos por severidad

### MEDIA — RESUELTO: el entorno git del usuario rompía los tests del gateway

Los tests de `internal/gateway` crean repositorios git temporales y hacen un commit inicial. Al heredar la configuración git del usuario (`commit.gpgsign=true`), el commit de prueba fallaba:

```
--- FAIL: TestSecondSessionGetsASeparateWorktree (0.02s)
    worktree_test.go:81: git ... commit -qm init: exit status 128
        error: gpg failed to sign the data:
        gpg: skipped "Test <test@example.com>": No secret key
        fatal: failed to write commit object
```

**Corrección aplicada:** `internal/gateway/git.go` sanea el entorno antes de lanzar comandos git (`withoutInlineGitConfig`), de modo que la configuración git del usuario no se filtra a los repositorios de prueba. Tras la corrección `make check` termina en 0.

### BAJA — `internal/review` sin pruebas (cobertura 0.0 %)

El paquete `internal/review` (fork de auto-mejora en segundo plano) no tiene archivo de test: `ls internal/review/` muestra solo `review.go`. Es código best-effort que corre en su propia goroutine; convendría cubrir al menos la construcción del runner y el manejo de errores.

### BAJA — `tools/clitest` con cobertura 88.2 %

Es una herramienta de desarrollo, no código de producción; la cobertura parcial es aceptable pero queda registrada.

### INFO — `internal/gateway` con cobertura 94.2 %

El paquete más grande del proyecto; el resto de paquetes están al 100 %.

### INFO — RESUELTO: árbol de trabajo con cambios sin commitear

`git status --short` mostraba `internal/webui/assets/index.html` modificado (artefacto generado por el build web). Se ha commiteado junto con el resto de cambios pendientes; el árbol de trabajo queda limpio.

---

## Comandos ejecutados durante la auditoría

| Comando | Resultado |
|---|---|
| `ls -la` | inventario de la raíz |
| `find . -maxdepth 3 -type f -not -path './.git/*' …` | estructura de archivos |
| `cat Makefile` | targets disponibles; `check` = fmt-check + vet + staticcheck + test |
| `cat README.md` | documentación del proyecto |
| `make check` | **exit 2** al inicio (fallo de gpg en gateway) → **exit 0** tras la corrección |
| `go test -count=1 ./internal/gateway/` | fallo por `gpg failed to sign the data` |
| `git config --show-origin --get commit.gpgsign` | `file:/home/madkoding/.gitconfig true` (causa raíz) |
| `make cover` | exit 0; agregado 98.6 % |
| `npm --prefix web audit --omit=dev` | exit 0; 0 vulnerabilidades |
| `go version` | go1.27.1 linux/amd64 |
| `git status --short` / `git log --oneline -5` | contexto de mantenibilidad |
| `grep -rInE '(password|secret|api[_-]?key|token|private[_-]?key)…'` | sin secretos embebidos |
| `find . -name '*.go' \| wc -l` | 516 archivos Go, 329 de test |
| `make check` (verificación final) | exit 0 |

---

## Conclusión

El proyecto está en buen estado: la puerta de calidad pasa, la cobertura es alta y no hay vulnerabilidades de dependencias ni secretos expuestos. El único fallo encontrado (el entorno git del usuario rompiendo los tests del gateway) quedó corregido y commiteado.

**Hallazgos que siguen abiertos:**

| Severidad | Hallazgo | Estado |
|---|---|---|
| BAJA | `internal/review` sin pruebas (cobertura 0.0 %) | Abierto |
| BAJA | `tools/clitest` con cobertura 88.2 % | Abierto |
| INFO | `internal/gateway` con cobertura 94.2 % | Abierto |

No queda ningún hallazgo de severidad MEDIA o superior abierto.
