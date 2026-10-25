# Auditoría completa de motita

**Fecha:** 2026-10-25 · **Commit auditado:** `8f45366` · **Go instalado:** 1.27.1 (el `go.mod` declara `go 1.26` y `toolchain go1.26.10`)

## 1. Alcance y método

Auditoría de solo lectura sobre el repositorio `motita` (módulo `github.com/madkoding/motita`): código Go bajo `internal/`, frontend en `web/`, dependencias, tests y arquitectura. No se ha modificado código de la aplicación; los únicos ficheros añadidos son este informe y `.motita/anchor`.

Método: lectura del árbol, ejecución real de los comandos de verificación del proyecto, `staticcheck`, `govulncheck`, `npm audit`, y ejecución aislada de cada test que falla para separar causa ambiental de causa de código. Los fallos de test se han reproducido además en un worktree limpio en `HEAD` para descartar que los introdujera esta auditoría.

## 2. Resumen ejecutivo

El proyecto está en buen estado en lo que respecta a cadena de suministro (Go sin dependencias externas, `npm audit` limpio) y a saneado de HTML en el frontend (DOMPurify en todas las rutas que tocan `innerHTML`). Los problemas reales son tres: (1) el toolchain de Go instalado arrastra vulnerabilidades conocidas de la biblioteca estándar; (2) hay errores ignorados en rutas destructivas (borrado de worktrees, ramas y sesiones) y en llamadas al gateway; y (3) el gate del proyecto no puede pasar en un clon limpio porque `internal/webui/assets/index.html` está versionado y obsoleto, y porque los tests de `internal/sandbox` y `internal/plan` dependen de que `bwrap` funcione, cosa que no ocurre en un contenedor sin privilegios.

## 3. Checks ejecutados (resultado real)

| Check | Comando | Resultado |
|---|---|---|
| Formato | `test -z "$(gofmt -l .)"` | **PASS** — 0 archivos sin formatear |
| Vet | `go vet ./...` | **PASS** — sin salida |
| Build | `go build ./...` | **PASS** — sin salida |
| Tests Go | `GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 go test ./...` | **FAIL** — 55 paquetes `ok`, 2 paquetes en fallo (`internal/plan`, `internal/sandbox`); `internal/webui` pasa una vez construidos los assets con `npm run build` |
| staticcheck | `staticcheck ./...` | **PASS** — 0 hallazgos |
| govulncheck | `govulncheck ./...` | **FAIL** — 12 vulnerabilidades en la stdlib de go1.27.1 (la primera, `GO-2026-6809`) |
| Typecheck web | `npm run typecheck` | **PASS** — `tsc --noEmit` sin errores |
| Tests web | `npm test` | **FAIL** — `ℹ tests 1417`, `ℹ pass 1416`, `ℹ fail 1` |
| Build web | `npm run build` | **PASS** — `precache 126 entries`, `files generated` |
| Auditoría npm | `npm audit` | **PASS** — 0 vulnerabilidades |

Fallos de `go test ./...`, con su causa aislada:

- `--- FAIL: TestSearchInFiles` (`internal/plan/plan_test.go:274`) y `--- FAIL: TestSearchInFilesSearchesNativelyWhereThereIsNoGrep` (`internal/plan/search_test.go:162`): el sandbox no puede ejecutar el comando (`motita: could not execute "/bin/true": exec refused`). Causa ambiental, no del código bajo prueba.
- `--- FAIL: TestAConfinedCommandCannotReadMotitasSecrets` (`internal/sandbox/secrets_linux_test.go:52`): `cat: .../notes.txt: Permission denied` — el confinamiento no llega a montar el árbol legible porque `bwrap` no puede arrancar. Verificado ejecutando `bwrap --ro-bind / / --dev /dev --proc /proc --unshare-net /bin/true`, que devuelve `bwrap: Failed to make / slave: Operation not permitted`.
- `--- FAIL: TestThePageNamesAssetsThatAreActuallyThere` (`internal/webui/page_test.go:494`): el `index.html` versionado nombra bundles con hashes que no existen en el checkout; pasa en cuanto se ejecuta `npm run build`.
- `✖ whenever the modal closes during the last answer, that answer connects nothing` (`web/src/__tests__/cov-git-connect.test.mjs:674`): test inestable, ver M1.

Reproducción en worktree limpio en `HEAD` (sin ningún cambio de esta auditoría): los mismos tres paquetes Go fallan con los mismos tests. Los fallos no los introduce este trabajo.

## 4. Seguridad

**Bien resuelto:**

- El gateway se niega a arrancar sin token: `internal/gateway/server.go:427-433` (`"the gateway needs a token: an unauthenticated agent is a remote shell"`).
- Límite de cuerpo en todas las entradas HTTP relevantes: `internal/gateway/server.go:1393`, `internal/gateway/voice.go:223`, `internal/gateway/artifacts.go:342` (`http.MaxBytesReader`).
- Todo el HTML que llega al DOM pasa por DOMPurify: `web/src/Markdown.tsx:343` (`sanitize`), `web/src/Markdown.tsx:385` (`sanitizeMessage`), `web/src/Term.tsx:335-350`. El `html: true` de marked está documentado y justificado en `web/src/Markdown.tsx:57-61`.
- TLS con versión mínima fijada: `internal/gateway/server.go:561` (1.2), `internal/peers/identity.go:97` y `:152` (1.3), `internal/llm/certpool.go:35` (1.2).
- El sandbox oculta los secretos de motita por ruta y por enlace simbólico plantado en el workspace (`internal/sandbox/secrets_linux_test.go:31-60`), aunque en este entorno no pueda ejecutarse (M2).

**Hallazgos:**

- **A1 (alto) — Toolchain con vulnerabilidades conocidas.** `govulncheck ./...` reporta 12 vulnerabilidades en la biblioteca estándar de go1.27.1 (`GO-2026-6809` y siguientes), corregidas en 1.27.2/1.27.3. El `go.mod` fija `toolchain go1.26.10`, así que el binario que se construya dependerá de qué toolchain resuelva cada máquina. *Recomendación:* subir `toolchain` a `go1.27.3` o superior en `go.mod` y añadir `govulncheck ./...` al CI.
- **A2 (alto) — Errores ignorados en rutas destructivas.** `internal/gateway/sessions.go:1101` y `:1103` (`_ = os.Remove(dir)`, `_ = os.RemoveAll(dir)`), `internal/gateway/sessiondelete.go:412`, `:416` y `:464` (quitar worktree, `PruneWorktrees`, borrar rama), `internal/gateway/projectfolder.go:70`, `internal/gateway/preview.go:147`, `internal/gateway/sessionnested.go:44` y `:94`. Si el borrado de un worktree o de una rama falla, el usuario recibe confirmación de una limpieza que no ocurrió y queda estado huérfano en disco y en git. *Recomendación:* registrar el error y devolverlo en la respuesta de la operación.
- **A3 (alto) — Errores ignorados en llamadas al gateway.** `internal/gateway/client.go:271`, `:282` y `:392` (`_ = c.postJSON(...)`, `_ = c.do(...)`): un cambio de nivel de razonamiento, de configuración o un reset que falla pasa completamente desapercibido. *Recomendación:* propagar el error al llamador o, como mínimo, registrarlo.
- **M3 (medio) — Versión mínima de TLS desigual.** `internal/gateway/server.go:561` y `internal/llm/certpool.go:35` aceptan TLS 1.2 mientras `internal/peers/identity.go:97,152` exigen 1.3. *Recomendación:* unificar en 1.3 donde el par lo soporte y documentar la excepción si algún cliente legítimo necesita 1.2.
- **B3 (bajo) — Página webui servida sin token.** `internal/webui/webui.go:8` documenta que la página se sirve sin token, «como una página de login». Es coherente con el diseño (la página no lleva secretos y el gateway exige token en cada petición), pero conviene que quede explícito en la documentación de despliegue. *Recomendación:* mantener, y añadir una nota en el README sobre qué queda expuesto sin autenticar.

## 5. Calidad de código

- **B1 (bajo) — Respuesta truncada en silencio.** `internal/gateway/server.go:1371`: `_ = json.NewEncoder(w).Encode(v)`. Si la codificación falla a mitad, el cliente recibe un JSON incompleto sin señal de error. *Recomendación:* codificar a un buffer primero y escribir solo si la codificación tuvo éxito.
- **B2 (bajo) — Deadline de escritura ignorado.** `internal/gateway/wshandler.go:491`: `_ = cl.conn.SetWriteDeadline(...)`. Un fallo al fijar el deadline deja la conexión WebSocket sin límite de escritura. *Recomendación:* cerrar la conexión si el deadline no se puede fijar.
- **B4 (bajo) — Cierre de listener ignorado.** `internal/gateway/server.go:452`: `_ = ln.Close()` en la ruta de error. Es aceptable (ya se va a devolver el error), pero se anota por completitud.

Positivo: `gofmt`, `go vet`, `go build` y `staticcheck` están limpios; el código sigue una estructura de paquetes coherente y los comentarios explican el *porqué* de las decisiones delicadas (sandbox, saneado, TLS).

## 6. Dependencias

- **Go:** `go.mod` no declara ninguna dependencia externa (`go list -m all` devuelve 1 módulo, el propio). Superficie de suministro mínima; no hay `go.sum` que auditar. El único riesgo es el toolchain (A1).
- **Web:** `npm audit` sin vulnerabilidades. `web/package.json` fija scripts explícitos (`typecheck`, `test`, `build`, `format:check`) y usa `node --test` en vez de un runner externo, lo que reduce dependencias.
- **Licencia:** `LICENSE` presente en la raíz.

## 7. Tests

- Cobertura funcional amplia: 54 paquetes Go en verde y 1416 de 1417 tests web en verde.
- **M1 (medio) — Test inestable en el frontend.** `web/src/__tests__/cov-git-connect.test.mjs:674` («whenever the modal closes during the last answer, that answer connects nothing») recorre `hops` de 0 a 11 esperando un número fijo de microtareas (`await Promise.resolve()`), y falla cuando el número real de microtareas cambia (`actual: [['github','octo']]`, `expected: []`). Es una carrera del propio test, no del componente. *Recomendación:* esperar una condición observable (un contador de cancelaciones, `await settle()`) en lugar de contar microtareas.
- **M2 (medio) — Tests que dependen de `bwrap` y fallan en vez de saltarse.** `internal/sandbox/secrets_linux_test.go:52` y los tests de `internal/plan` que ejecutan comandos fallan cuando el entorno no permite crear namespaces (`bwrap: Failed to make / slave: Operation not permitted`). En un contenedor sin privilegios esto es lo normal. *Recomendación:* detectar la disponibilidad de `bwrap` al inicio del test y hacer `t.Skip` con el motivo, para que un entorno sin privilegios no se confunda con una regresión de seguridad.
- **A4 (alto) — El gate no pasa en un clon limpio.** `internal/webui/assets/index.html` está versionado (añadido con `-f`, pese a que `.gitignore:37` ignora `/internal/webui/assets/*`) y nombra bundles con hashes que no están en el checkout. `TestThePageNamesAssetsThatAreActuallyThere` (`internal/webui/page_test.go:494`) falla hasta que se ejecuta `npm run build`. *Recomendación:* o no versionar `index.html` y construirlo en CI antes de `go test`, o reconstruirlo y commitearlo en el mismo cambio que mueve los hashes; hoy el orden del gate (`go test` antes de `npm run build`) garantiza el fallo.

## 8. Arquitectura

Capas observadas, todas bajo `internal/`:

- `gateway` — HTTP/WebSocket, sesiones, borrado de sesiones y worktrees, artefactos, voz. Es la capa más grande y concentra la mayoría de los errores ignorados (A2, A3, B1, B2).
- `plan` — bucle del agente y herramientas (búsqueda en ficheros, ejecución). Depende de `sandbox` para ejecutar comandos.
- `sandbox` — confinamiento de los comandos (Landlock/bwrap, ocultación de secretos, red propia).
- `peers` — identidad y transporte TLS entre pares.
- `llm` — cliente del modelo, pool de certificados.
- `gitx` — operaciones git (worktrees, ramas).
- `auth`, `oauth`, `config`, `redact` — credenciales, flujo OAuth de escritorio, configuración y redacción de secretos en logs.
- `webui` — sirve los assets construidos por `web/`.
- `tools/` — `clitest`, `mockapi`, `mockllm`, `seccomptrap` (arneses de prueba).

El acoplamiento es razonable y en una sola dirección (`gateway` → `plan` → `sandbox`/`llm`/`gitx`). El punto débil arquitectónico es que `gateway` hace de fachada y de orquestador a la vez: mezcla el transporte HTTP con la lógica de borrado de sesiones y worktrees, lo que hace que un fallo de limpieza (A2) se traduzca en una respuesta de éxito. *Recomendación:* extraer la limpieza de sesión a una función que devuelva error y que el handler traduzca a código HTTP.

## 9. Recuento de hallazgos por severidad

| Severidad | Nº | IDs |
|---|---|---|
| Crítico | 0 | — |
| Alto | 4 | A1, A2, A3, A4 |
| Medio | 3 | M1, M2, M3 |
| Bajo | 3 | B1, B2, B3 |
| **Total** | **10** | |

## 10. Acciones recomendadas (ordenadas)

1. **A1** — Subir el toolchain de Go a 1.27.3+ en `go.mod` y añadir `govulncheck ./...` al CI.
2. **A4** — Resolver el desfase de `internal/webui/assets/index.html`: dejar de versionarlo y construirlo en CI antes de `go test`, o reconstruirlo y commitearlo junto a los bundles.
3. **A2** — Propagar los errores de borrado en `internal/gateway/sessions.go`, `sessiondelete.go`, `projectfolder.go`, `preview.go` y `sessionnested.go`.
4. **A3** — Propagar o registrar los errores de `internal/gateway/client.go:271,282,392`.
5. **M2** — Hacer que los tests de `internal/sandbox` y `internal/plan` se salten con motivo cuando `bwrap` no esté disponible, en vez de fallar.
6. **M1** — Sustituir el conteo de microtareas en `web/src/__tests__/cov-git-connect.test.mjs:674` por una espera sobre una condición observable.
7. **M3** — Unificar la versión mínima de TLS en 1.3 donde sea posible.
8. **B1** — Codificar a buffer antes de escribir la respuesta JSON en `internal/gateway/server.go:1371`.
9. **B2** — Cerrar la conexión WebSocket si `SetWriteDeadline` falla (`internal/gateway/wshandler.go:491`).
10. **B3** — Documentar en el README qué queda expuesto sin autenticar (`internal/webui/webui.go:8`).

---

*Auditoría de solo lectura: no se ha modificado código de la aplicación. Los comandos de verificación del proyecto están declarados en `.motita/anchor`.*

## 11. Verificación final

Ejecución definitiva sobre `HEAD` = `8f45366`, con los siete comandos de `.motita/anchor` y sin tuberías (el código de salida que se registra es el del comando, no el de `tail`):

| # | Comando | Exit | Resultado |
|---|---|---|---|
| 1 | `test -z "$(gofmt -l .)"` | 0 | **PASS** — ningún archivo sin formatear |
| 2 | `go vet ./...` | 0 | **PASS** — sin salida |
| 3 | `go build ./...` | 0 | **PASS** — sin salida |
| 4 | `GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 go test ./...` | 1 | **FAIL** — 55 paquetes `ok`, 2 paquetes en fallo (`internal/plan`, `internal/sandbox`) |
| 5 | `npm run typecheck` | 0 | **PASS** — `tsc --noEmit` sin errores |
| 6 | `npm test` | 1 | **FAIL** — `ℹ tests 1417`, `ℹ pass 1416`, `ℹ fail 1` |
| 7 | `npm run build` | 0 | **PASS** — `precache 126 entries`, `files generated` |

Los dos fallos son ambientales y están demostrados como preexistentes, no introducidos por esta auditoría:

- Se ejecutó el mismo `go test ./...` en un worktree limpio de `HEAD`, sin ningún cambio de esta auditoría, y aparecieron los mismos tests en rojo: `TestSearchInFiles`, `TestSearchInFilesSearchesNativelyWhereThereIsNoGrep` y `TestAConfinedCommandCannotReadMotitasSecrets`, con el mismo recuento de paquetes `ok` (55). En el worktree también falló `internal/webui`, porque un checkout limpio no tiene los assets construidos; en el árbol principal ese paquete pasa tras `npm run build`.
- La causa es el entorno: el contenedor donde se audita no permite crear los namespaces que `bwrap` necesita (`bwrap: Failed to make / slave: Operation not permitted`), así que el sandbox no puede ejecutar ningún comando y los tests que dependen de él fallan por el entorno, no por el código.
- El fallo de `npm test` es el test inestable M1 (`web/src/__tests__/cov-git-connect.test.mjs:674`), que cuenta microtareas en vez de esperar una condición observable; falla de forma reproducible en esta máquina.

**NO EJECUTADO:** ninguno. Los siete comandos se ejecutaron de verdad; dos de ellos fallaron por las causas descritas arriba y así se reportan, nunca como aprobados.
