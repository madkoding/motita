# starlight 🌟

Agente autónomo de **3 capas** para máquinas i386 (y cualquier Linux amd64/arm64),
escrito en **Go puro**: sólo biblioteca estándar, **cero dependencias externas**,
sin cgo y **sin Docker**.

El principio que lo gobierna: **el modelo propone, un validador determinista
dispone**. Ninguna tarea se da por completada porque el LLM lo diga; sólo el
ancla puede declarar `PASS`, y lo hace ejecutando comprobaciones reales.

```
TAREA ──► [Capa B] analizar ─► planificar ─► proponer acción
                                                    │
                                    [Capa C] ejecutar aislado
                                                    │
                                    [Capa A] validar (PASS/FAIL)
                                                    │
                       PASS ──► acción final (commit / publicar / notificar)
                       FAIL ──► registros del fallo al LLM y reintentar
                     agotado ──► escalar
```

El repositorio incluye además un **chat interactivo de terminal** (`cmd/chat`,
documentado en [`cmd/chat/README.md`](cmd/chat/README.md)) que comparte el estilo
de ejecución y las lecciones sobre grupos de procesos.

---

## Arquitectura

### Capa A — EL ANCLA (validador determinista)

Componente nativo que valida **siempre** el resultado, sin razonamiento:

| | |
|---|---|
| Entrada | el estado del sistema tras la acción del agente |
| Proceso | validaciones estrictas: comandos, código de salida, expresiones regulares sobre la salida, invariantes de negocio |
| Salida | `PASS`/`FAIL` + registros estructurados JSON |
| Características | sin LLM, rápido y predecible, reglas configurables desde el YAML |

Reglas de diseño que se cumplen en el código:

- **Sin validador no hay éxito.** Con `anchor.tipo=none` el ancla devuelve `FAIL`
  y el agente **se niega a arrancar**, porque no existe autoridad que declare
  `PASS`. Un `PASS` sin comprobación real es exactamente el fallo que esta
  arquitectura existe para evitar.
- **Todas las comprobaciones deben pasar.** Un solo `check` fallido invalida el
  resultado completo.
- **Un ancla incomprobable es un fallo, no un éxito silencioso**: si el comando no
  existe, expira o la expresión regular no compila, devuelve `FAIL` con el motivo.
- **La salida del ancla es JSON** y viaja íntegra al LLM en el siguiente intento,
  junto con la salida real del comando fallido.

### Capa B — EL MOTOR DE RAZONAMIENTO (cliente LLM ligero)

Cliente escrito a mano (sin SDK) para tres familias de API: **OpenAI**
(`/chat/completions`), **Anthropic** (`/v1/messages`) y **Gemini**
(`:generateContent`). Todos se normalizan a la misma estructura de mensajes, así
que el resto del agente no sabe cuál está detrás.

- Lee el contexto: tarea, plan, número de intento y **los registros de los fallos
  previos**.
- Prompts dinámicos desde plantillas con variables `{{...}}`, 100% configurables:
  el motor nunca escribe texto de prompt por su cuenta.
- **Reintentos con backoff exponencial**, con una distinción que importa: `429`,
  `5xx` y errores de red se reintentan; `401`/`400` **no**, porque reintentar una
  credencial inválida sólo gasta tiempo y cuota.
- Parseo de respuestas estructuradas tolerante a lo que de verdad devuelven los
  modelos: bloques ```` ```json ````, texto alrededor, llaves anidadas, comillas
  escapadas, respuestas truncadas.
- **Máximo N intentos** antes de escalar.

### Capa C — EL SANDBOX (ejecución aislada ligera, sin Docker)

Aislamiento en capas, aplicando lo que el sistema permita y **diciendo la verdad
sobre lo que no se pudo aplicar**:

| Capa | Qué hace | Requisitos |
|---|---|---|
| Directorio temporal efímero | `TMPDIR` propio por intento, borrado al terminar | ninguno |
| `setrlimit` | CPU, memoria (espacio de direcciones), procesos, descriptores, tamaño de archivo | ninguno |
| cgroups v1 | cota real de memoria y PIDs (`RLIMIT_AS` es una aproximación) | kernel con cgroups v1 y permiso de escritura |
| chroot + bajar privilegios | raíz de sistema de archivos restringida y usuario sin privilegios | ser root |
| `CLONE_NEWNET` | sin red dentro del comando | `CAP_SYS_ADMIN` |

Detalles que costaron trabajo y están resueltos en el código:

- **Los límites los aplica un proceso hijo que es el propio binario
  re-ejecutado** (marca `__sandbox_exec`): aplica `setrlimit`, entra al `chroot`,
  baja privilegios y hace `syscall.Exec`. Así no se depende de `prlimit` ni de
  util-linux (que en una máquina mínima puede no estar) y no queda ningún proceso
  Go intermedio consumiendo el presupuesto limitado.
- **`syscall.Exec` no busca en `PATH`**: un `comando: make` escrito en el YAML
  fallaría con `ENOENT`. El hijo resuelve la ruta usando el `PATH` **restringido
  del sandbox**, no el del agente.
- **El directorio de trabajo es persistente; el temporal es efímero.** El efecto
  del trabajo debe sobrevivir para que el ancla pueda verlo. Si las acciones
  corrieran en un directorio que se borra al terminar, el validador no encontraría
  nunca el resultado y el agente fallaría siempre (esto ocurrió de verdad durante
  el desarrollo y está cubierto por una prueba).
- **Matar el grupo de procesos, no sólo el hijo.** `exec.CommandContext` mata a
  `sh`, pero sus descendientes siguen vivos con el tubo de salida abierto y `Wait`
  se queda esperando: medido, un `sleep 30` con plazo de 1 s tardaba **5 s** en
  volver. Con grupo propio + `SIGKILL` al grupo corta en el plazo exacto (medido:
  bucle infinito con `cpu_segundos: 2` → corte a los **2.002 s**).
- **El comando no hereda los secretos del agente**: el entorno se construye desde
  cero, sin `OPENAI_API_KEY` ni `STARLIGHT_LLM_API_KEY` dentro del comando.

---

## Flujo del agente

```
INICIO
[1] LEER TAREA     de la fuente configurable (stdin, archivo, cola, API)
[2] EXTRAER        contexto y criterios de éxito (las reglas del ancla)
[3] LLM            analiza la tarea   -> {"comprensible", "criterios_exito", ...}
[4] LLM            genera un plan     -> {"plan", "subtareas", ...}
[5] DIVIDIR         en subtareas si el análisis lo pide (con límite de profundidad)
[6] LLM            genera la acción   -> {"acciones", "accion_final"}
[7] EJECUTAR        en el SANDBOX (Capa C)
[8] VALIDAR         con el ANCLA (Capa A) — siempre, incluso si [7] falló
[9] PASS  -> ejecutar la acción final (command | api | git_commit) y FIN
    FAIL  -> registros del fallo al LLM, volver a [6] mientras intentos < MAX
    agotado -> escalar (agent.escalar) y FIN
```

Si el análisis declara la tarea **no comprensible** (falta información), no se
ejecuta nada: la tarea se descarta con el motivo y cuenta como fallo para el
código de salida.

---

## Instalación y compilación cruzada

Requiere Go 1.23 o superior. **No hace falta compilar en la máquina i386.**

```bash
make agent-386        # agente de 3 capas para linux/386 (comprueba ELFCLASS32)
make all              # chat + agente, en 386, amd64 y arm64
make check            # gofmt + go vet + go test
make e2e-agente       # extremo a extremo en un contenedor i386 real
```

Binarios resultantes (estáticos, sin cgo, sin librerías externas):

| Binario | Tamaño | Requisito |
|---|---|---|
| `dist/starlight-agent-386` | 6.90 MB | < 10 MB ✓ |
| `dist/starlight-agent-amd64` | 7.07 MB | |
| `dist/starlight-agent-arm64` | 6.50 MB | |
| `dist/starlight-linux-386` (chat) | 6.32 MB | |

Se copian a la máquina i386 por `scp`, `ftp` o USB:

```bash
chmod +x starlight-agent-386
./starlight-agent-386 -config agent.yaml
```

---

## Configuración

Todo es configurable **sin recompilar**. Se puede validar sin ejecutar nada ni
llamar al LLM:

```bash
starlight-agent -config configs/agent.yaml.example -validar-config
starlight-agent -config configs/agent.yaml.example -aislamiento   # qué aísla este kernel
```

Cualquier valor se puede sobreescribir con variables `STARLIGHT_<BLOQUE>_<CAMPO>`,
que **ganan sobre el YAML** (ideal para secretos y contenedores). También se
aceptan `OPENAI_API_KEY`, `OPENAI_BASE_URL` y `OPENAI_MODEL`.

| Bloque | Contenido |
|---|---|
| `task_source` | `tipo` (`stdin`/`file`/`api`/`queue`), `ruta`, `dir`, `url`, `metodo`, `campo`, `intervalo`, `headers`, `cuerpo` |
| `anchor` | `tipo` (`command`/`none`), `comando`, `argumentos`, `timeout`, `esperar_exit`, `esperar_salida` (regex), `checks[]` |
| `sandbox` | `tipo` (`none`/`chroot`/`cgroups`), `raiz`, `usuario`, `memoria_mb`, `cpu_segundos`, `procesos`, `archivos_abiertos`, `tamano_max_archivo_mb`, `aislar_red`, `cgroups`, `cgroup_raiz`, `timeout`, `conservar_efimero`, `salida_max_kb` |
| `llm` | `proveedor` (`openai`/`anthropic`/`gemini`), `modelo`, `api_key`, `base_url`, `max_tokens`, `temperature`, `timeout`, `max_intentos`, `backoff_inicial`, `backoff_max` |
| `prompts` | `analyze`, `plan`, `execute`, cada uno con `sistema` y `usuario` |
| `final_action` | `tipo` (`none`/`command`/`api`/`git_commit`), `comando`, `argumentos`, `url`, `metodo`, `mensaje_commit` |
| `agent` | `max_reintentos`, `profundidad_subtareas`, `max_tareas`, `workspace_dir`, `log_file`, `log_level`, `log_consola`, `log_max_mb`, `log_backups`, `graceful_shutdown_timeout`, `escalar` |

### Variables disponibles en los prompts

`{{tarea}}` `{{workspace}}` `{{origen}}` `{{intento}}` `{{max_intentos}}`
`{{reglas}}` `{{analisis}}` `{{plan}}` `{{historial}}` `{{modelo}}`
`{{proveedor}}` `{{contexto_*}}`

Si una plantilla usa una variable sin valor, **se registra un aviso** y la
variable se deja visible: no se envía al modelo un prompt con huecos silenciosos.

El parser YAML es propio (sin dependencias) y **rechaza con un mensaje explícito**
lo que no entiende: claves desconocidas (con el bloque donde están), tabuladores
en la indentación, anclas/alias/tags y listas mal formadas. Nunca adivina.

### Los tres casos de uso

| Caso | Archivo | Flujo |
|---|---|---|
| 1. Desarrollo | `configs/casos/1-desarrollo.yaml` | tarea en archivo → LLM → sandbox sin red → **`go test` + `go vet` + `gofmt` como ancla** → commit |
| 2. Análisis de datos | `configs/casos/2-datos.yaml` | tarea desde API → análisis aislado sin red → **invariantes del informe como ancla** → publicar resultado por API |
| 3. Automatización | `configs/casos/3-automatizacion.yaml` | cola de archivos → script acotado (256 MB, 30 s CPU, sin red) → **comprobación del efecto como ancla** → notificación |

Los tres están verificados por la suite de pruebas: si un YAML de ejemplo deja de
cargar o pierde una de sus tres plantillas, las pruebas fallan.

---

## Uso

```bash
export STARLIGHT_LLM_API_KEY=sk-...        # nunca la clave en el YAML

# ejecución normal, con la fuente configurada
starlight-agent -config configs/casos/1-desarrollo.yaml

# una sola tarea, sin tocar la configuración
starlight-agent -config configs/casos/1-desarrollo.yaml -tarea "arregla TestFoo"

# el contenido de un archivo como tarea
starlight-agent -config configs/casos/2-datos.yaml -archivo-tarea tarea.md

# validar la configuración sin llamar al LLM
starlight-agent -config mi.yaml -validar-config

# ver el aislamiento realmente disponible en esta máquina
starlight-agent -config mi.yaml -aislamiento
```

**Apagado ordenado:** el primer `SIGINT`/`SIGTERM` cancela el trabajo en curso y
concede `agent.graceful_shutdown_timeout` segundos para terminar; el segundo sale
de inmediato con código 130.

**Código de salida:** `0` si todas las tareas pasaron el ancla, `1` si alguna falló
(incluyendo el motivo de cada una *en el propio mensaje de error*) y `2` si la
configuración es inválida. Así se puede usar directamente desde cron o systemd.

---

## Registro

JSON Lines, una línea por evento, con rotación por tamaño:

```json
{"ts":"2026-09-17T05:17:37.726157258Z","nivel":"warn","msg":"validación del ancla","pass":false,"motivo":"comprobaciones fallidas: principal"}
{"ts":"2026-09-17T05:17:37.733481067Z","nivel":"info","msg":"validación del ancla","pass":true,"motivo":"1 comprobación(es) superadas"}
{"ts":"2026-09-17T05:17:37.736394109Z","nivel":"info","msg":"validación superada","intento":2,"accion_final":"command exit=0"}
```

```bash
jq 'select(.nivel=="error")' workspace/starlight.log
jq -r 'select(.msg=="tarea completada") | .tarea' workspace/starlight.log
```

`log_max_mb` y `log_backups` controlan la rotación (`starlight.log.1`, `.2`, …).

---

## Verificación

```bash
make check           # gofmt + go vet + go test  (lo mismo que corre la CI)
make e2e-agente      # extremo a extremo del agente en un contenedor i386 real
make e2e             # extremo a extremo del chat en un contenedor i386 real
```

- **138 casos de prueba**, todos verdes, sobre las tres capas y sus piezas: ancla,
  sandbox, motor LLM, bucle del agente, parser YAML, registro, plantillas y
  fuentes de tareas.
- La CI (`.github/workflows/ci.yml`) corre `gofmt`, `vet`, `test -race`, compila
  el agente para `linux/386`/`amd64`/`arm64`, **verifica que el i386 sea
  ELFCLASS32** (leyendo la cabecera ELF y también con `file`), lo ejecuta dentro
  de `i386/debian:bookworm-slim` y publica los binarios al crear un tag `v*`.

### Prueba de extremo a extremo

`make e2e-agente` compila el agente y un LLM simulado (`tools/mockllm`) para 386 y
los ejecuta **dentro del mismo contenedor de 32 bits**. El modelo simulado se
equivoca a propósito en el primer intento y corrige en el segundo, de modo que se
ejercita el ciclo real completo:

```
fase=analyze  -> fase=plan -> fase=execute (intento 1, escribe contenido inválido)
{"msg":"validación del ancla","pass":false,"motivo":"comprobaciones fallidas: principal"}
{"msg":"intento fallido","intento":1,"max_intentos":3}
fase=execute (intento 2, escribe contenido-válido)
{"msg":"validación del ancla","pass":true,"motivo":"1 comprobación(es) superadas"}
{"msg":"validación superada","intento":2,"accion_final":"command exit=0"}

✅ E2E del agente de 3 capas en i386: OK
   ✓ el informe tiene el contenido pedido: contenido-valido
   ✓ la acción final se ejecutó tras el PASS
   ✓ hubo un intento fallido antes del PASS (el reintento funcionó)
```

La prueba comprueba el resultado **en el sistema de archivos**, no lo que el
agente dice de sí mismo.

---

## Solución de problemas (i386)

| Síntoma | Causa y solución |
|---|---|
| `no parece haber cgroups v1` (aviso, no error) | El kernel o el contenedor no exponen `/sys/fs/cgroup/memory`. Se aplican igualmente los límites POSIX y el aviso queda registrado para no fingir aislamiento. |
| `chroot solicitado pero el proceso no es root` | El chroot se omite con un aviso. Ejecuta como root o usa `tipo: cgroups`. |
| `el comando no pudo ejecutarse dentro del sandbox (código 127)` | El comando no existe o no está en el `PATH` del sandbox; el error del hijo dice la ruta buscada. |
| `el proceso fue terminado por una señal` | Un límite del sandbox hizo su trabajo (CPU o memoria). Sube `cpu_segundos` / `memoria_mb`. |
| El agente falla siempre con `se agotaron los intentos` | Mira el `motivo` del ancla: está en el mensaje de error y en el registro. Suele ser el ancla mal configurada, no el modelo. |
| `anchor.tipo=none: este agente sólo declara una tarea como completada...` | Es intencionado: sin validador determinista no hay `PASS`. Configura un ancla real. |
| El binario no arranca (`not found`) | Es un ELF de 32 bits: `head -c 5 binario \| od -An -tx1` debe empezar por `7f 45 4c 46 01`. |
| `salida truncada por el límite del sandbox` | Sube `sandbox.salida_max_kb` si el comando produce más salida de la esperada. |

---

## Estructura del proyecto

```
cmd/agent/            agente de 3 capas (programa principal)
cmd/chat/             chat interactivo de terminal
internal/anchor/      Capa A: validador determinista
internal/llm/         Capa B: OpenAI / Anthropic / Gemini y parseo JSON
internal/sandbox/     Capa C: directorio efímero, setrlimit, cgroups, chroot
internal/config/      YAML (parser propio), entorno y validación
internal/execx/       ejecución de procesos (grupo de procesos, límites, salida)
internal/task/        fuentes de tareas: stdin, archivo, cola, API
internal/plantilla/   variables {{...}} de los prompts
internal/logx/        registro JSON con rotación
tools/mockapi/        API compatible con OpenAI para probar el chat
tools/mockllm/        LLM simulado para el E2E del agente
configs/              configuración de ejemplo + 3 casos de uso
scripts/              pruebas de extremo a extremo
```

Cada paquete lleva sus pruebas junto al código (`*_test.go`).

---

## Extender el agente

- **Otra fuente de tareas**: implementa la interfaz `task.Fuente` (`Siguiente`,
  `Cerrar`, `Descripcion`) y añádela a `task.Nueva`.
- **Otro proveedor de LLM**: añade su dialecto en `internal/llm` (una función
  `llamada<Proveedor>`); el resto del agente no cambia.
- **Otra acción final**: añade un caso en `(*Agente).ejecutarAccionFinal`.
- **Observar resultados**: asigna `Agente.Observador` para recibir el
  `ResultadoTarea` de cada tarea (métricas, integración, pruebas).

## Licencia

MIT — ver [LICENSE](LICENSE).
