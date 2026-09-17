# Glossary: Spanish → English (starlight)

This file is the single source of truth for translating this repository to
English. Every identifier, comment, string literal, YAML key and document must
follow it exactly so all packages stay consistent.

## Rules

1. **Only translate.** Never change behaviour, logic, control flow or test
   expectations. A translation that changes what the code does is a bug.
2. Identifiers become English, in Go style: exported `PascalCase`, unexported
   `camelCase`, short and idiomatic (`ruta` → `path`, not `thePathVariable`).
3. Comments become English prose. Keep the *meaning*, including the hard-won
   operational details (measurements, footguns, "why not the obvious thing").
   A comment that explains a bug must still explain that bug in English.
4. Error messages, log messages and CLI help become English.
5. Test names become English (`TestSandboxEjecutaConLimites` →
   `TestSandboxRunsWithLimits`).
6. After translating a package: `gofmt -w .`, `go build ./...`, `go vet ./...`,
   and `go test ./<package>/...` must all pass. Fix any fallout of your own
   renames.
7. Do not add `//go:build` tags, do not reorder functions, do not "improve"
   anything. Translation only.

## Core domain vocabulary

| Spanish | English |
|---|---|
| ancla | anchor |
| capa | layer |
| sandbox | sandbox (already English) |
| tarea | task |
| subtarea | subtask |
| motor (de razonamiento) | engine |
| plantilla | template |
| validador | validator |
| reglas | rules |
| veredicto | verdict |
| hallazgo | finding |
| aviso | warning |
| motivo | reason |
| fallo / falla | failure / fail |
| fallos seguidas | consecutive failures |
| intento | attempt |
| reintento | retry |
| escalar | escalate |
| apagado (ordenado) | (graceful) shutdown |
| efímero | ephemeral |
| límites | limits |
| aislamiento | isolation |
| privilegios | privileges |
| descriptores | file descriptors |

## Identifiers

| Spanish | English |
|---|---|
| tipo | kind / Type (see below) |
| comando | command / Command |
| argumentos | args / Arguments |
| ruta | path / Path |
| cuerpo | body |
| campo | field |
| metodo | method |
| esperar_exit | expect_exit |
| esperar_salida | expect_output |
| salida | output |
| entrada | input |
| entrada_estandar | stdin |
| salida_estandar | stdout |
| nombre | name |
| duracion | duration |
| motivo | reason |
| nivel | level |
| mensaje | message |
| resultado | result |
| respuesta | response |
| peticion | request |
| opciones | options |
| limites | limits |
| usuario | user |
| procesos | processes |
| memoria | memory |
| archivos_abiertos | open_files |
| tamano | size |
| raiz | root |
| conservar | keep |
| avisos | warnings |
| problemas | problems |
| modos | modes |
| contenido | content |
| historial | history |
| faltantes | missing |
| recortar | truncate |
| truncado | truncated |
| cargar | load |
| guardar | save |
| abrir | open |
| cerrar | close |
| crear | create |
| borrar / eliminar | remove |
| ejecutar | run / execute |
| leer | read |
| escribir | write |
| buscar | find / search |
| sustituir | substitute |
| rellenar | fill |
| parsear / parseo | parse / parsing |
| descomillar | unquote |
| recortar_comentario | strip_comment |
| linea | line |
| vacias | blank |
| sangrado | indent |
| indicador | indicator |
| anidado | nested |
| secuencia | sequence |
| mapa | map |
| lista | list |
| escalar_linea | scalar |
| clave | key |
| valor | value |
| cola | queue |
| fuente | source |
| directorio | directory |
| archivo | file |
| carpeta | folder |
| tamano_max_archivo_mb | max_file_size_mb |
| memoria_mb | memory_mb |
| cpu_segundos | cpu_seconds |
| aislar_red | isolate_network |
| cgroup_raiz | cgroup_root |
| salida_max_kb | max_output_kb |
| conservar_efimero | keep_ephemeral |
| max_reintentos | max_retries |
| profundidad_subtareas | subtask_depth |
| max_tareas | max_tasks |
| apagado_timeout | shutdown_timeout |
| mensaje_commit | commit_message |
| log_nivel | log_level |
| log_consola | log_console |
| log_archivo | log_file |
| escalar (bloque) | on_failure |
| es_indicator_bloque | is_block_indicator |
| es_item | is_item |
| partir_clave | split_key |
| intentar_clave | try_key |
| parse_* helpers | parse_* (keep English prefix) |

## Types: `tipo` vs `kind`

- Struct field holding a discriminator (`anchor.tipo`): name it `Kind`.
- `Config.Type`-like method or local: prefer `kind`.
- The Go builtin `type` keyword is untouched.

## Config keys (YAML) and environment variables

All YAML keys become the English identifier of the field they feed.

| YAML key (old) | YAML key (new) |
|---|---|
| `tipo` | `kind` |
| `ruta` | `path` |
| `dir` | `dir` (keep) |
| `url` | `url` (keep) |
| `metodo` | `method` |
| `campo` | `field` |
| `intervalo` | `interval` |
| `headers` | `headers` (keep) |
| `cuerpo` | `body` |
| `comando` | `command` |
| `argumentos` | `args` |
| `timeout` | `timeout` (keep) |
| `esperar_exit` | `expect_exit` |
| `esperar_salida` | `expect_output` |
| `checks` | `checks` (keep) |
| `raiz` | `root` |
| `usuario` | `user` |
| `memoria_mb` | `memory_mb` |
| `cpu_segundos` | `cpu_seconds` |
| `procesos` | `processes` |
| `archivos_abiertos` | `open_files` |
| `tamano_max_archivo_mb` | `max_file_size_mb` |
| `aislar_red` | `isolate_network` |
| `cgroups` | `cgroups` (keep) |
| `cgroup_raiz` | `cgroup_root` |
| `conservar_efimero` | `keep_ephemeral` |
| `salida_max_kb` | `max_output_kb` |
| `proveedor` | `provider` |
| `modelo` | `model` |
| `api_key` | `api_key` (keep) |
| `base_url` | `base_url` (keep) |
| `max_tokens` | `max_tokens` (keep) |
| `temperature` | `temperature` (keep) |
| `max_intentos` | `max_attempts` |
| `backoff_inicial` | `backoff_initial` |
| `backoff_max` | `backoff_max` (keep) |
| `sistema` | `system` |
| `usuario` (prompt) | `user` |
| `max_reintentos` | `max_retries` |
| `profundidad_subtareas` | `subtask_depth` |
| `max_tareas` | `max_tasks` |
| `workspace_dir` | `workspace_dir` (keep) |
| `log_file` | `log_file` (keep) |
| `log_level` | `log_level` (keep) |
| `log_consola` | `log_console` |
| `log_max_mb` | `log_max_mb` (keep) |
| `log_backups` | `log_backups` (keep) |
| `graceful_shutdown_timeout` | `graceful_shutdown_timeout` (keep) |
| `escalar` | `on_failure` |
| `mensaje_commit` | `commit_message` |
| `nombre` (check) | `name` |

Environment variables: `STARLIGHT_<BLOCK>_<FIELD>` using the **new** English
field names, e.g. `STARLIGHT_LLM_MODEL`, `STARLIGHT_SANDBOX_MEMORY_MB`,
`STARLIGHT_ANCHOR_EXPECT_EXIT`, `STARLIGHT_AGENT_MAX_RETRIES`.

## Prompt template variables

`{{tarea}}`→`{{task}}`, `{{workspace}}` (keep), `{{origen}}`→`{{origin}}`,
`{{intento}}`→`{{attempt}}`, `{{max_intentos}}`→`{{max_attempts}}`,
`{{reglas}}`→`{{rules}}`, `{{analisis}}`→`{{analysis}}`, `{{plan}}` (keep),
`{{historial}}`→`{{history}}`, `{{modelo}}`→`{{model}}`,
`{{proveedor}}`→`{{provider}}`, `{{contexto_*}}`→`{{context_*}}`.

## Markers and reserved strings

- Sandbox re-exec marker `__sandbox_exec`: keep as is.
- JSON output keys of the anchor (`pass`, `checks`, `motivo`, `duracion_ms`):
  translate to `pass`, `checks`, `reason`, `duration_ms`.
- Log field names become English (`nivel`→`level`, `motivo`→`reason`, etc.).

## Files that are documents, not code

`README.md`, `cmd/chat/README.md`, `configs/*.yaml`, `configs/casos/*.yaml`,
`scripts/*.sh`, `Makefile` and the CI workflow comments are translated too,
including all prose and every config key per the table above.
