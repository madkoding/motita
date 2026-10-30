# Modelo de contexto del agente: diagnóstico y propuesta

Estado: fase 1 implementada en este PR; fase 2 propuesta, pendiente de aprobación.

## Síntoma

En conversaciones largas el agente "divaga": rehace lo que ya se decidió, pregunta otra vez o
trabaja solo desde el texto del pedido ("sí, hazlo"). La sospecha era que hay muchas formas de
guardar y recuperar contexto sin unificar. La auditoría lo confirma y además encontró dos fallos
de cableado que bastan para explicar el síntoma.

## Inventario: dónde vive el contexto y quién lo lee

| # | Mecanismo | Dónde se guarda | Cómo llega al modelo | Presupuesto | Plan (chat) | Tarea: análisis | Tarea: rondas de trabajo |
|---|---|---|---|---|---|---|---|
| 1 | Memoria de la persona (`internal/memory`) | `~/.motita/memory/*.md` | brief automático + 4 herramientas | 3000 car. | sí, cada petición | sí | **no** (antes de este PR) |
| 2 | Archivo de la conversación (`internal/contextmem`) | `~/.motita/context/<sesión>/` | recall automático | 2000 tokens | sí | sí | **no** (antes) |
| 3 | Sesión de chat: resumen de compactación | memoria + `carried.json` | mensajes de la sesión | ventana del modelo | sí | no | no |
| 4 | Plan fijado (`session.Pin`) | memoria + `carried.json` | mensaje de sistema | sin límite | sí | **no** (antes) | **no** (antes) |
| 5 | Transcripción de la tarea (`DialogueTurn`) | runner / gateway | `{{history}}` | 12 turnos × 500 car. | **solo turnos de chat** (antes) | sí | **no**: `{{history}}` se sobrescribe con las rondas |
| 6 | Lecciones del proyecto (`agent/memory.go`) | `<proyecto>/.motita/memory.md` | `{{project}}` | 3000 car. | **no** | sí | sí |
| 7 | Memoria de trabajo de la tarea (`workmemory.go`) | solo durante la tarea | "WHAT YOU HAVE ALREADY READ" | por tarea | no | no | sí |
| 8 | Rondas previas + `notes` | solo durante la tarea | "PREVIOUS ROUNDS" | 10 rondas | no | no | sí |
| 9 | Biblioteca de procedimientos (skills) + reward + usage + curator + revisión en segundo plano + propuestas al cerrar | `~/.motita/skills`, por proyecto | 4 herramientas (`list/search/read/save_skill`) | bajo demanda | sí | sí | sí |
| 10 | Diario de fricción (`internal/friction`) | local | nunca | — | no | no | no |
| 11 | SOUL.md + nota del proyecto | `~/.motita/SOUL.md`, sesión | prompt de sistema | fijo | sí | sí | sí |

Restos del sistema anterior: el índice de código (`search_index`) y el manual por carpeta de
"notas" ya no existen en el código; `config.MemoryDir` solo renombra `~/.motita/notes` a
`~/.motita/memory` al arrancar. No hay contradicción activa por ellos, pero la documentación
interna (memorias del proyecto) todavía los describe.

## Herramientas que ve el modelo

- **Modo plan (chat)**: 13 herramientas: `list_directory`, `read_file`, `execute_command`,
  `search_in_files`, `list_skills`, `search_skills`, `read_skill`, `save_skill`, `search_memory`,
  `read_memory`, `write_memory`, `append_memory`, `fetch_url`.
- **Modo tarea**: 18 tipos de acción en el prompt (más los de escritorio si está activado), entre
  ellos `memory_search`, `memory_read`, `memory_write`, `memory_append` y los 4 de skills.
- La misma memoria tiene **dos nombres** según el modo (`search_memory` frente a `memory_search`);
  el propio brief tiene que decir "ábrela con read_memory / memory_read".
- Hay **tres cosas llamadas "memoria"**: la de la persona (1), las lecciones del proyecto
  (`.motita/memory.md`, 6) y la memoria de trabajo de la tarea (7). Además, "notes" es el nombre
  de un campo de la tarea.
- El prompt de tarea pesa ~24,6 KB y el de plan ~14,5 KB (alma 5,6 KB + operativo 8,9 KB): la parte
  que explica memoria y skills ocupa una porción grande y compite con el pedido.

## Fallos encontrados (con prueba reproducible)

1. **Las rondas de trabajo de una tarea no veían el contexto.** `actionPhase` reemplaza
   `{{history}}` por las rondas, y el recall, la memoria y el plan fijado solo se pasaban al
   análisis. En una tarea "sí, hazlo" las rondas trabajaban con el texto del pedido y el resumen
   del análisis. Prueba: `TestTheWorkingRoundsSeeTheWholeContext` (antes **0 de 4** datos llegaban
   a la ronda; ahora 4 de 4).
2. **El chat no veía las tareas hechas entre sus turnos.** La sesión de plan solo guardaba turnos
   de plan, y `planSpan` contaba las tareas intermedias como "visibles", así que tampoco se
   recuperaban del archivo. Una pregunta como "¿qué renombraste?" justo después de una tarea no
   tenía la respuesta en el contexto. Prueba: `TestTheChatSeesTheWorkDoneBetweenItsTurns`.
3. **El plan fijado no llegaba al modo tarea** salvo cuando el turno anterior era exactamente el
   plan y la persona decía "adelante". Con un mensaje en medio, la tarea perdía el plan.
4. **Presupuestos independientes**: memoria 3000 car., archivo 2000 tokens, lecciones 3000 car.,
   diálogo hasta 12 KB, cada uno con su propio encabezado y sin orden de prioridad.

## Fase 1 (este PR): un único brief por turno

- Nuevo paquete `internal/brief`: `Compose(budget, parts...)` junta las fuentes en orden de
  prioridad bajo **un solo presupuesto de 8000 caracteres** (~2000 tokens) y un solo encabezado
  ("WHAT THIS CONVERSATION ALREADY KNOWS"), y corta lo que no cabe.
- Modo tarea: el brief = plan fijado → turnos anteriores del archivo → memoria de la persona. Se
  calcula una vez en el análisis y viaja con él a **todas las rondas** (`{{recall}}`), junto con
  los últimos 4 turnos de la conversación (`{{dialogue}}`, 300 car. cada uno). El plan fijado se
  omite si el pedido ya lo trae.
- Modo plan: el mismo `Compose` para archivo → memoria (el plan fijado ya lo envía la sesión).
- Cada tarea terminada se añade a la sesión de chat (`shareLastTurn`), y `planSpan` cuenta los
  turnos que la sesión de verdad tiene.
- Sin migración de datos: no cambia ningún formato en disco.

## Fase 2 (propuesta): una sola fuente de verdad

Objetivo: un solo almacén, una herramienta de lectura, una de escritura y un brief por turno.

1. **Un almacén** (`internal/memory`) con namespaces tipados:
   - `persona/…` lo que hoy es la memoria de la persona (sin mover archivos: el namespace es la
     carpeta actual).
   - `proyecto/<clave>/lecciones.md` lo que hoy es `<proyecto>/.motita/memory.md`. Migración: al
     abrir el proyecto, si existe el archivo viejo se copia (no se borra) y se marca como migrado.
   - `procedimientos/…` los skills, leídos por el mismo buscador. La biblioteca, el reward y el
     curator siguen funcionando sobre esos archivos; solo cambia la puerta de entrada.
   - El archivo de la conversación sigue aparte (es por sesión y se borra con ella), pero se
     consulta por la misma herramienta con `ns:conversación`.
2. **Dos herramientas en ambos modos, con el mismo nombre**: `recall` (busca y lee; `ns:` para
   acotar) y `remember` (crea o añade; nunca reemplaza). Sustituyen a las 8 de memoria + 4 de skills
   en ambos modos (de 13 a 7 en plan; de 18 a 12 tipos en tarea). `save_skill` pasa a ser
   `remember` en `procedimientos/`.
3. **Un brief por turno** en ambos modos (fase 1 ya lo deja listo) que incluye también las
   lecciones del proyecto, hoy ausentes en el modo plan.
4. **Prompts más cortos**: las secciones de memoria y biblioteca se fusionan en una sola
   ("Lo que sabes y cómo recordarlo"), lo que libera unos 3–4 KB por prompt.

Riesgos: cambiar nombres de herramientas toca prompts, tests de contrato, la web (chips de
"memoria usada") y las skills ya guardadas que citan `memory_search`. Por eso se propone como
PR separado, con alias de los nombres viejos durante una versión.

## Cómo medir

- `go test ./internal/agent -run TestTheWorkingRoundsSeeTheWholeContext -v` imprime cuántos de
  los 4 datos de una conversación larga (plan fijado, turno reciente, turno archivado, memoria)
  llegan al prompt de las rondas de trabajo: 0/4 antes, 4/4 después.
- `go test ./internal/tui -run TestTheChatSeesTheWorkDoneBetweenItsTurns -v` comprueba que una
  pregunta en chat después de una tarea lleva la tarea en la petición.
- Estas pruebas miden lo que el modelo recibe, no su comportamiento: con un modelo real queda
  pendiente comparar, sobre la misma conversación, cuántas rondas y preguntas repetidas hace antes
  y después (la página de evaluaciones, `/v1/evals`, es el lugar natural para ese caso).
