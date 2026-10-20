// The Spanish dictionary, keyed by the English text the interface is written in. It is loaded
// only when Spanish is chosen (see i18n.ts), so English readers never download it.
//
// Neutral Latin-American Spanish, addressing the reader as "tú". A key that is missing here is
// shown in English, never as an empty string.

import more from "./i18n.es.part2.ts";

// The dictionary is in two files so neither grows too long: this one and i18n.es.part2.ts.
const first: Record<string, string> = {
  Image: "Imagen",
  "Next image": "Imagen siguiente",
  "Previous image": "Imagen anterior",
  Fit: "Ajustar",
  "Zoom out": "Alejar",
  "Zoom in": "Acercar",
  "Image not available": "Imagen no disponible",
  "No image": "Sin imagen",
  "Click to enlarge": "Clic para ampliar",
  Screenshot: "Captura",
  After: "Después",
  Before: "Antes",
  "Before and after": "Antes y después",
  Suggestions: "Sugerencias",
  Plan: "Plan",
  "Run as task": "Ejecutar como tarea",
  needed: "necesaria",
  "Run as plan": "Ejecutar como plan",
  "Applying suggestions": "Aplicando sugerencias",
  Apply: "Aplicar",
  "Apply ({n})": "Aplicar ({n})",
  Ignore: "Ignorar",
  Ignored: "Ignoradas",
  "{done} of {total} selected": "{done} de {total} seleccionadas",
  "1 suggestion applied": "1 sugerencia aplicada",
  "{n} suggestions applied": "{n} sugerencias aplicadas",
  "Not answered before the conversation moved on; no longer offered.":
    "No se respondieron antes de seguir la conversación; ya no se ofrecen.",
  "Apply these suggestions you made, and nothing else:":
    "Aplica estas sugerencias que hiciste, y nada más:",
  "(active)": "(activo)",
  "({n} event(s) were not kept while nothing was listening)":
    "({n} evento(s) no se guardaron mientras nadie escuchaba)",
  "1 action": "1 acción",
  "1 agent": "1 agente",
  "1 subagent": "1 subagente",
  "1 background agent running": "1 agente en segundo plano trabajando",
  "1 change": "1 cambio",
  "1 command": "1 comando",
  "1 file changed": "1 archivo cambiado",
  "1 thought": "1 pensamiento",
  "1 uncommitted change in this working tree":
    "1 cambio sin commitear en este árbol de trabajo",
  "1 uncommitted change will be discarded":
    "Se descartará 1 cambio sin commitear",
  "A session's checkout could not be inspected":
    "No se pudo revisar el checkout de una sesión",
  "A turn is running: it finishes with the current model, and the new one answers from the next turn.":
    "Hay un turno en curso: termina con el modelo actual y el nuevo responde desde el próximo turno.",
  "API key": "Clave de API",
  "Always allow this here": "Permitir siempre esto aquí",
  "Files, checks and what was learned":
    "Archivos, comprobaciones y lo aprendido",
  "Kept for next time": "Guardado para la próxima vez",
  "Run it, and from now on run commands of this kind in this project without asking. Commands the policy forbids stay forbidden.":
    "Ejecutarlo y, de ahora en adelante, ejecutar los comandos de este tipo en este proyecto sin preguntar. Los que la política prohíbe siguen prohibidos.",
  "Any running tasks will be interrupted.":
    "Las tareas en curso se interrumpirán.",
  Archived: "Archivadas",
  "Model switched to {model} in this session":
    "Modelo cambiado a {model} en esta sesión",
  "Proposed by the review": "Propuestas de la revisión",
  "No session uses these until you accept them. Read each one first.":
    "Ninguna sesión las usa hasta que las aceptes. Lee cada una antes.",
  Accept: "Aceptar",
  Reject: "Rechazar",
  "could not open the proposed skill": "no se pudo abrir la skill propuesta",
  "could not accept the skill": "no se pudo aceptar la skill",
  "could not reject the skill": "no se pudo rechazar la skill",
  "Ask for something…": "Pide algo…",
  "Authentication required": "Se requiere autenticación",
  "No connection to the server": "Sin conexión con el servidor",
  "Motita cannot reach the gateway right now":
    "Motita no puede comunicarse con el gateway ahora",
  "Check that it is running and that you are online. Retrying on its own…":
    "Revisa que esté en marcha y que tengas conexión. Reintenta solo…",
  "Connection lost. Reconnecting…": "Conexión perdida. Reconectando…",
  Automatic: "Automático",
  "Back at the checkpoint — conversation and files":
    "De vuelta en el checkpoint — conversación y archivos",
  "Back at the checkpoint — conversation only":
    "De vuelta en el checkpoint — solo la conversación",
  "Back to the list": "Volver a la lista",
  Cancel: "Cancelar",
  "Checking credentials…": "Verificando credenciales…",
  "Checking latest release": "Buscando la última versión",
  "Upgrade complete. The gateway is restarting.":
    "Actualización completa. El gateway se está reiniciando.",
  "Checking what this project's sessions are holding…":
    "Revisando qué tienen las sesiones de este proyecto…",
  "Checking what this session is holding…": "Revisando qué tiene esta sesión…",
  "The conversation so far was written with {model}; {next} takes it over from the next turn and may answer differently. Open a new session to keep each model on its own.":
    "La conversación hasta ahora se escribió con {model}; {next} la retoma desde el próximo turno y puede responder distinto. Abre una sesión nueva para mantener cada modelo por separado.",
  "The turn in progress finishes with the previous model.":
    "El turno en curso termina con el modelo anterior.",
  "This conversation already has messages: the new model takes them over and may answer differently. A new session keeps each model on its own.":
    "Esta conversación ya tiene mensajes: el nuevo modelo los retoma y puede responder distinto. Una sesión nueva mantiene cada modelo por separado.",
  You: "Tú",
  Agent: "Agente",
  "The command could not be run": "No se pudo ejecutar el comando",
  "The approval could not be answered": "No se pudo responder la aprobación",
  'That computer did not keep "Always allow": it runs an older Motita. Update it there.':
    'Ese equipo no guardó "Permitir siempre": tiene una versión más antigua de Motita. Actualízalo allí.',
  "Refused, nobody was there to approve it: {what}":
    "Rechazado, no había nadie para aprobarlo: {what}",
  "Run in the shell": "Ejecutar en el shell",
  Send: "Enviar",
  "Edit project": "Editar proyecto",
  "Main branch": "Rama principal",
  "The project always goes back to this branch when it has no sessions.":
    "El proyecto siempre vuelve a esta rama cuando no tiene sesiones.",
  "It is on {branch} now.": "Ahora está en {branch}.",
  "Could not save the project": "No se pudo guardar el proyecto",
  "Project saved": "Proyecto guardado",
  "https://github.com/user/repo.git": "https://github.com/usuario/repo.git",
  Back: "Atrás",
  Name: "Nombre",
  "More options": "Más opciones",
  "Made from the name when empty. A folder under the workspace, a simple name with no paths.":
    "Se crea a partir del nombre si está vacío. Una carpeta dentro del workspace, un nombre simple sin rutas.",
  "Creating the project…": "Creando el proyecto…",
  "Creating…": "Creando…",
  "Stop the agent": "Detener al agente",
  "A message typed meanwhile goes to the run; Esc stops it":
    "Un mensaje escrito mientras tanto va a la tarea; Esc la detiene",
  "Click Continue in the session menu to keep working from the latest project branch.":
    "Haz clic en Continuar en el menú de la sesión para seguir trabajando desde la rama más reciente del proyecto.",
  "Click again: discard changes since":
    "Haz clic de nuevo: descartar los cambios desde entonces",
  Close: "Cerrar",
  "Close sidebar": "Cerrar la barra lateral",
  "Close the agents": "Cerrar los agentes",
  "Close the terminal": "Cerrar la terminal",
  "Close the thinking": "Cerrar el razonamiento",
  Complete: "Completado",
  Confirm: "Confirmar",
  Connect: "Conectar",
  "Connecting…": "Conectando…",
  Continue: "Continuar",
  "Continued in a new session": "Continuaste en una sesión nueva",
  "Could not continue this session": "No se pudo continuar esta sesión",
  "Could not isolate this thread": "No se pudo aislar este hilo",
  "Isolate in a worktree": "Aislar en un worktree",
  "Moves this thread to a worktree of its own, so it stops sharing the project's folder.":
    "Mueve este hilo a un worktree propio, para que deje de compartir la carpeta del proyecto.",
  "Could not go back to that checkpoint": "No se pudo volver a ese checkpoint",
  "Could not reach the gateway": "No se pudo contactar al gateway",
  "Could not save the language": "No se pudo guardar el idioma",
  Create: "Crear",
  "Create a new session from the project's current branch and keep working":
    "Crear una sesión nueva desde la rama actual del proyecto y seguir trabajando",
  "Current version": "Versión actual",
  Delete: "Eliminar",
  "Delete project": "Eliminar proyecto",
  "Delete session": "Eliminar sesión",
  "Delete skill": "Eliminar skill",
  "Deleting…": "Eliminando…",
  "Description (optional)": "Descripción (opcional)",
  Details: "Detalles",
  "Discard and delete": "Descartar y eliminar",
  Dismiss: "Descartar",
  Done: "Listo",
  "Download & Install": "Descargar e instalar",
  "Download complete": "Descarga completa",
  "Downloaded {p}%": "Descargado {p}%",
  "Downloading {v}": "Descargando {v}",
  Email: "Correo",
  "Enter the gateway token to continue":
    "Ingresa el token del gateway para continuar",
  "Every command in this session runs without asking. Click to be asked again.":
    "Todos los comandos de esta sesión se ejecutan sin preguntar. Haz clic para que vuelva a preguntarte.",
  "Exempt this from every automatic transition":
    "Exceptuarla de toda transición automática",
  Failed: "Falló",
  "Filter by name, title or what it is for":
    "Filtrar por nombre, título o para qué sirve",
  "Folder name": "Nombre de la carpeta",
  "go back to the conversation you left last":
    "vuelve a la última conversación que dejaste",
  "Go back and restore the changes": "Volver y restaurar los cambios",
  "Go back to this checkpoint": "Volver a este checkpoint",
  "Hide details": "Ocultar detalles",
  "How it was checked": "Cómo se verificó",
  "Installed {v}": "{v} instalada",
  Installing: "Instalando",
  Integrate: "Integrar",
  "Integrate this session's work back into the project":
    "Integrar el trabajo de esta sesión en el proyecto",
  "It is running in {session}.": "Se está ejecutando en {session}.",
  "Its contents cannot be shown. Deleting discards whatever it holds, uncommitted work included.":
    "No se puede mostrar su contenido. Eliminarlo descarta todo lo que tenga, incluido el trabajo sin commitear.",
  Language: "Idioma",
  Compact: "Compacto",
  Comfortable: "Cómodo",
  Density: "Densidad",
  Large: "Grande",
  Normal: "Normal",
  Small: "Pequeño",
  "Font size": "Tamaño de fuente",
  Settings: "Ajustes",
  Skills: "Skills",
  Tasks: "Tareas",
  General: "General",
  Appearance: "Apariencia",
  Themes: "Temas",
  Mode: "Modo",
  "Color scheme": "Esquema de color",
  "Follow the system": "Seguir al sistema",
  Light: "Claro",
  Dark: "Oscuro",
  Violet: "Violeta",
  Forest: "Bosque",
  Amber: "Ámbar",
  Rose: "Rosa",
  Graphite: "Grafito",
  Slate: "Pizarra",
  Midnight: "Medianoche",
  Dusk: "Crepúsculo",
  Coffee: "Café",
  Ember: "Brasa",
  Olive: "Oliva",
  Sand: "Arena",
  Preview: "Vista previa",
  "{name} theme, {mode}": "Tema {name}, {mode}",
  Notifications: "Notificaciones",
  "Last used {when}": "Último uso {when}",
  "Latest version": "Última versión",
  "Loading models...": "Cargando modelos...",
  "Loading session": "Cargando la sesión",
  "Loading the library…": "Cargando la biblioteca…",
  "Merged — Read Only": "Integrada — Solo lectura",
  Model: "Modelo",
  "More actions": "Más acciones",
  "My project": "Mi proyecto",
  "New project": "Proyecto nuevo",
  "New session": "Sesión nueva",
  "New session in project": "Sesión nueva en el proyecto",
  "New task": "Tarea nueva",
  "New version {version} available — click to upgrade":
    "Nueva versión {version} disponible — haz clic para actualizar",
  Next: "Siguientes pasos",
  No: "No",
  "No scheduled tasks yet. One created here fires into the conversation you are in.":
    "Todavía no hay tareas programadas. Una creada aquí se ejecuta en la conversación en la que estás.",
  "No session holds uncommitted changes, so nothing uncommitted is lost. The project's own checkout is not touched.":
    "Ninguna sesión tiene cambios sin commitear, así que no se pierde nada sin commitear. El checkout propio del proyecto no se toca.",
  "No sessions yet": "Todavía no hay sesiones",
  "No projects": "No hay proyectos",
  "No sessions": "No hay sesiones",
  "No skills yet. The agent writes one when it works something out.":
    "Todavía no hay skills. El agente escribe una cuando resuelve algo.",
  "Not completed": "No completado",
  "Nothing matches “{q}”.": "Nada coincide con “{q}”.",
  "Nothing to integrate yet": "Todavía no hay nada que integrar",
  "Nothing yet. Ask for something below.": "Nada todavía. Pide algo abajo.",
  "Waiting for a new session": "Esperando por una nueva sesión",
  "Type below to start talking to the agent, or create a session first.":
    "Escribe abajo para empezar a hablar con el agente, o crea una sesión primero.",
  "Open sidebar": "Abrir la barra lateral",
  "Open the agents": "Abrir los agentes",
  "Open the terminal history": "Abrir el historial de la terminal",
  "Open the thinking": "Abrir el razonamiento",
  "Partly done": "Hecho en parte",
  "Paste the gateway token…": "Pega el token del gateway…",
  Pause: "Pausar",
  "Paused — last {when}": "En pausa — última {when}",
  Pin: "Fijar",
  "Plan (read-only)": "Plan (solo lectura)",
  "Plan mode: ask about the project, nothing is changed":
    "Modo plan: pregunta sobre el proyecto, no se cambia nada",
  'Project "{title}" created': 'Proyecto "{title}" creado',
  "Project checkout is on branch: {branch}":
    "El checkout del proyecto está en la rama: {branch}",
  Provider: "Proveedor",
  Reasoning: "Razonamiento",
  "Reasoning level": "Nivel de razonamiento",
  Rename: "Renombrar",
  "Restarting the gateway": "Reiniciando el gateway",
  "Restarting…": "Reiniciando…",
  Restore: "Restaurar",
  Resume: "Reanudar",
  Run: "Ejecuta",
  "Run it": "Ejecutar",
  "Run now": "Ejecutar ahora",
  Save: "Guardar",
  "Saving…": "Guardando…",
  "Scheduled ({n})": "Programadas ({n})",
  "Scheduled tasks": "Tareas programadas",
  "Skill library": "Biblioteca de skills",
  "Starting upgrade…": "Iniciando la actualización…",
  Task: "Tarea",
  "Task mode: make changes and prove them with your check":
    "Modo tarea: hace cambios y los prueba con tu verificación",
  "Task started": "Tarea iniciada",
  "That task could not start": "Esa tarea no pudo iniciarse",
  "That token was rejected. Try again.":
    "Ese token fue rechazado. Inténtalo de nuevo.",
  "The agent stops seeing this in its index and its search":
    "El agente deja de verla en su índice y en sus búsquedas",
  "The agent will see this again": "El agente volverá a verla",
  "The curator will leave this alone": "El curador no la tocará",
  "The gateway is still running the version it had before. Nothing was replaced, so you can keep using it and try again.":
    "El gateway sigue con la versión que tenía antes. No se reemplazó nada, así que puedes seguir usándolo e intentarlo de nuevo.",
  "The project checkout has {what}": "El checkout del proyecto tiene {what}",
  "The session's checkout is clean, so nothing uncommitted is lost.":
    "El checkout de la sesión está limpio, así que no se pierde nada sin commitear.",
  "The task could not be created": "No se pudo crear la tarea",
  "The upgrade did not finish": "La actualización no terminó",
  "These files are not committed anywhere. Deleting the project discards them for good.":
    "Estos archivos no están commiteados en ningún lado. Eliminar el proyecto los descarta para siempre.",
  "These files are not committed anywhere. Deleting the session discards them for good.":
    "Estos archivos no están commiteados en ningún lado. Eliminar la sesión los descarta para siempre.",
  "This checkpoint did not keep the files":
    "Este checkpoint no guardó los archivos",
  "This conversation will be permanently deleted. This cannot be undone.":
    "Esta conversación se eliminará para siempre. No se puede deshacer.",
  "This needs your approval": "Esto necesita tu aprobación",
  "This project and all its sessions will be permanently deleted. This cannot be undone. Repositories it shares with other projects stay in your workspace.":
    "Este proyecto y todas sus sesiones se eliminarán para siempre. No se puede deshacer. Los repositorios que comparte con otros proyectos se quedan en tu espacio de trabajo.",
  "This session is already integrated": "Esta sesión ya está integrada",
  "This session is read-only after integration":
    "Esta sesión es de solo lectura después de integrarla",
  "This session's checkout could not be inspected":
    "No se pudo revisar el checkout de esta sesión",
  "This skill will be permanently deleted, with its usage history. This cannot be undone. A built-in procedure cannot be deleted.":
    "Esta skill se eliminará para siempre, junto con su historial de uso. No se puede deshacer. Un procedimiento incluido no se puede eliminar.",
  "This will download the new binary from GitHub, verify its checksum, replace the current executable, and":
    "Esto descargará el binario nuevo desde GitHub, verificará su checksum, reemplazará el ejecutable actual y",
  Title: "Título",
  "Token or password": "Token o contraseña",
  "Turn off": "Desactivar",
  "Turn on": "Activar",
  Unpin: "Desfijar",
  "Upgrade Motita": "Actualizar Motita",
  "Upgrade now": "Actualizar ahora",
  "Verifying checksum": "Verificando el checksum",
  "View release notes": "Ver las notas de la versión",
  "What changed": "Qué cambió",
  "What should it do?": "¿Qué debe hacer?",
  "What the files were before is kept in git as {ref}":
    "Lo que tenían los archivos antes queda guardado en git como {ref}",
  "What this project is about": "De qué trata este proyecto",
  "Worth knowing": "Conviene saber",
  "[no result]": "[sin resultado]",
  agent: "agente",
  agents: "agentes",
  "all allowed": "todo permitido",
  "all commands allowed": "todos los comandos permitidos",
  "analysing the task...": "analizando la tarea...",
  blocked: "no ejecutada",
  "approved (all commands allowed in this session): {x}":
    "aprobado (todos los comandos permitidos en esta sesión): {x}",
  "argument…": "argumento…",
  "asking you to approve: {x}": "pidiendo tu aprobación: {x}",
  "cache read {n}": "caché leída {n}",
  "cache write {n}": "caché escrita {n}",
  cancelled: "cancelado",
  change: "cambio",
  changed: "cambiado",
  changes: "cambios",
  "checks that passed": "verificaciones aprobadas",
  connecting: "conectando",
  "context used, and what was carried over":
    "contexto usado, y lo que se arrastró",
  conversation: "conversación",
  "conversation and files": "conversación y archivos",
  "conversation only": "solo la conversación",
  copied: "copiado",
  copy: "copiar",
  "could not answer the approval": "no se pudo responder la aprobación",
  "could not change the pin": "no se pudo cambiar el fijado",
  "could not change the skill": "no se pudo cambiar la skill",
  "could not create a session": "no se pudo crear una sesión",
  "could not create the project": "no se pudo crear el proyecto",
  "could not create the project: {err}": "no se pudo crear el proyecto: {err}",
  "could not delete the project": "no se pudo eliminar el proyecto",
  "could not open the artifact": "no se pudo abrir el artefacto",
  "could not delete the artifact": "no se pudo eliminar el artefacto",
  "could not pin the artifact": "no se pudo fijar el artefacto",
  "could not save the file": "no se pudo guardar el archivo",
  "could not delete the session": "no se pudo eliminar la sesión",
  "could not delete the skill": "no se pudo eliminar la skill",
  "could not load the conversation": "no se pudo cargar la conversación",
  "could not load the skill library":
    "no se pudo cargar la biblioteca de skills",
  "could not open the skill": "no se pudo abrir la skill",
  "could not rename the session": "no se pudo renombrar la sesión",
  "could not restore the skill": "no se pudo restaurar la skill",
  "could not start the upgrade": "no se pudo iniciar la actualización",
  "could not stop allowing all commands":
    "no se pudo dejar de permitir todos los comandos",
  "could not update the configuration":
    "no se pudo actualizar la configuración",
  "deciding action (round {a}/{b})...":
    "decidiendo la acción (ronda {a}/{b})...",
  "due now": "toca ahora",
  edited: "editado",
  error: "error",
  "every command and key": "todos los comandos y teclas",
  "every {d}": "cada {d}",
  fail: "fallida",
  failed: "falló",
  "failed · exit {code}": "falló · salida {code}",
  "filter the conversation": "filtra la conversación",
  "fires in": "se ejecuta en",
  high: "alto",
  "how hard the model thinks: off, low, medium, high":
    "cuánto piensa el modelo: apagado, bajo, medio, alto",
  "in a terminal to print the link, or paste the token here.":
    "en una terminal para mostrar el enlace, o pega el token aquí.",
  "in {branch}": "en {branch}",
  "in {n}": "entrada {n}",
  "install the newest motita release":
    "instala la versión más reciente de motita",
  "leave motita": "salir de motita",
  "list the conversations the gateway holds":
    "lista las conversaciones que tiene el gateway",
  "list the provider's models, or switch to one":
    "lista los modelos del proveedor, o cambia a uno",
  low: "bajo",
  main: "principal",
  medium: "medio",
  "merged {sha}": "integrada {sha}",
  missing: "falta",
  moved: "movido",
  new: "nuevo",
  "no commands yet": "todavía no hay comandos",
  "no key needed": "no requiere clave",
  "no result": "sin resultado",
  "no summary": "sin resumen",
  "no thoughts yet": "todavía no hay pensamientos",
  "not connected": "sin conexión",
  off: "apagado",
  "out {n}": "salida {n}",
  pass: "aprobada",
  passed: "completado",
  pinned: "fijada",
  "plan ready: {n} steps": "plan listo: {n} pasos",
  "planning...": "planificando...",
  preview: "vista previa",
  project: "proyecto",
  "rate the last answer as bad; the note says what to fix":
    "califica la última respuesta como mala; la nota dice qué corregir",
  "rate the last answer as good": "califica la última respuesta como buena",
  ready: "listo",
  reconnecting: "reconectando",
  removed: "eliminado",
  "restart the gateway": "reiniciará el gateway",
  "round {n} done; the model reports more to do":
    "ronda {n} terminada; el modelo indica que queda trabajo",
  "run the setup again: provider, key, model, check":
    "vuelve a ejecutar la configuración: proveedor, clave, modelo, verificación",
  running: "en curso",
  "running…": "ejecutando…",
  set: "configurada",
  "show or hide the run's agents":
    "muestra u oculta los agentes de la ejecución",
  "show or hide the run's agents: purpose, time, tokens":
    "muestra u oculta los agentes de la ejecución: propósito, tiempo, tokens",
  "skill-toggle|off": "desactivar",
  "skill-toggle|on": "activar",
  "skill|off": "desactivada",
  skipped: "omitida",
  stale: "obsoleta",
  "start a fresh conversation": "empieza una conversación nueva",
  steps: "pasos",
  "subtask {a}/{b}: {x}": "subtarea {a}/{b}: {x}",
  "switch to another conversation": "cambia a otra conversación",
  "synthesizing answer...": "sintetizando la respuesta...",
  "waiting for {n} background agent(s)...":
    "esperando a {n} agente(s) en segundo plano...",
  "task complete: {x}": "tarea completa: {x}",
  "terminal history": "historial de la terminal",
  "the diagram renderer could not be loaded":
    "no se pudo cargar el renderizador de diagramas",
  "the gateway refused the turn": "el gateway rechazó el turno",
  "the math renderer could not be loaded":
    "no se pudo cargar el renderizador de fórmulas",
  "the reply could not be used, asking again: {x}":
    "la respuesta no se pudo usar, preguntando de nuevo: {x}",
  thinking: "razonamiento",
  "this diagram could not be drawn": "no se pudo dibujar este diagrama",
  "understood: {x}": "entendido: {x}",
  unknown: "desconocida",
  "upgrade failed": "la actualización falló",
  "used {n}×": "usada {n}×",
  "validating with anchor...": "validando con el anchor...",
  "validation passed; running final action...":
    "validación aprobada; ejecutando la acción final...",
  warning: "aviso",
  "what motita has learned from your ratings":
    "lo que motita aprendió de tus calificaciones",
  working: "trabajando",
  "{n} actions": "{n} acciones",
  "{n} agents": "{n} agentes",
  "{n} subagents": "{n} subagentes",
  "{n} background agents running": "{n} agentes en segundo plano trabajando",
  "{n} changes": "{n} cambios",
  "{n} commands": "{n} comandos",
  "{n} files changed": "{n} archivos cambiados",
  "{n} sessions finished": "{n} sesiones terminaron",
  "{n} thoughts": "{n} pensamientos",
  "Show 1 earlier": "Ver 1 anterior",
  "Show {n} earlier": "Ver {n} anteriores",
  "{n} uncommitted changes in this working tree":
    "{n} cambios sin commitear en este árbol de trabajo",
  "{n} uncommitted changes will be discarded":
    "Se descartarán {n} cambios sin commitear",
  "{passed}/{total} checks passed": "{passed}/{total} verificaciones aprobadas",
  "{provider} / {model} — reasoning: {level}":
    "{provider} / {model} — razonamiento: {level}",
  "This server cannot sign in to git hosts. Ask whoever runs it to enable login.":
    "Este servidor no puede iniciar sesión en hosts de git. Pide a quien lo administra que active el inicio de sesión.",
  "Connect {host}": "Conectar {host}",
  "Connect a git host": "Conectar un host de git",
  "Loading…": "Cargando…",
  "Which git host do you want to connect?":
    "¿Qué host de git quieres conectar?",
  "Connected as {user}": "Conectado como {user}",
  "{host} could not start the login: {err}. You can connect with a token instead.":
    "{host} no pudo iniciar el acceso: {err}. Puedes conectar con un token.",
  "Contacting {host}…": "Contactando a {host}…",
  Retry: "Reintentar",
  "Use a token instead": "Usar un token en su lugar",
  "Waiting for you to approve in {host}…": "Esperando que apruebes en {host}…",
  "Sign in with {host} to let motita clone your repositories. It only asks for repository access.":
    "Inicia sesión en {host} para que motita pueda clonar tus repositorios. Solo pide acceso a repositorios.",
  "Sign in with the browser instead":
    "Iniciar sesión con el navegador en su lugar",
  "Copy code": "Copiar código",
  Copied: "Copiado",
  Copy: "Copiar",
  "Open {host}, enter this code and approve the access.":
    "Abre {host}, escribe este código y aprueba el acceso.",
  "Login code": "Código de acceso",
  "Open {host}": "Abrir {host}",
  "Authorize motita in {host}. This page notices when you are done.":
    "Autoriza a motita en {host}. Esta página detecta cuando terminas.",
  "Open {host} to authorize": "Abrir {host} para autorizar",
  "Browser on another machine? Paste the code or the URL it ended on":
    "¿Navegador en otra máquina? Pega el código o la URL en la que terminó",
  "Authorization code or URL": "Código de autorización o URL",
  "Code sent. Finishing the login…": "Código enviado. Terminando el acceso…",
  "Send code": "Enviar código",
  "Unexpected answer from the server.": "Respuesta inesperada del servidor.",
  "Bitbucket needs an app password, not your account password. Give it read access to repositories.":
    "Bitbucket necesita una contraseña de aplicación, no la contraseña de tu cuenta. Dale acceso de lectura a los repositorios.",
  'Create a personal access token with permission to read your repositories (the "repo" or "read_repository" scope), then paste it here.':
    'Crea un token de acceso personal con permiso para leer tus repositorios (el alcance "repo" o "read_repository") y pégalo aquí.',
  "Create a token": "Crear un token",
  "Account name": "Nombre de cuenta",
  "App password": "Contraseña de aplicación",
  Token: "Token",
  "The token is stored on this machine and used only to clone and list repositories.":
    "El token se guarda en esta máquina y solo se usa para clonar y listar repositorios.",
  "Checking…": "Comprobando…",
  "just now": "justo ahora",
  "{n}m ago": "hace {n} min",
  "{n}h ago": "hace {n} h",
  "{n}d ago": "hace {n} d",
  "{n}mo ago": "hace {n} meses",
  "{n}y ago": "hace {n} años",
  behind: "atrás",
  outdated: "desactualizada",
  "Update main branch": "Actualizar rama principal",
  "Update {repo}": "Actualizar {repo}",
  "Branch {branch}": "Rama {branch}",
  "could not read the repository": "no se pudo leer el repositorio",
  "the action failed": "la acción falló",
  "Pulling {repo}": "Actualizando la rama de {repo}",
  "Going back to the main branch of {repo}":
    "Volviendo a la rama principal de {repo}",
  "Creating a branch in {repo}": "Creando una rama en {repo}",
  "Putting the changes of {repo} aside":
    "Guardando aparte los cambios de {repo}",
  "Deleting the merged branches of {repo}":
    "Borrando las ramas ya integradas de {repo}",
  "Discarding the changes of {repo}": "Descartando los cambios de {repo}",
  "git fetch, then a fast-forward of the current branch; never a merge.":
    "git fetch y luego avance rápido de la rama actual; nunca un merge.",
  "git checkout of the main branch, only from a clean checkout.":
    "git checkout de la rama principal, solo desde una copia sin cambios.",
  "git switch -c: a new branch at the current work.":
    "git switch -c: una rama nueva en el trabajo actual.",
  "git stash push --include-untracked, labelled with the date.":
    "git stash push --include-untracked, con la fecha como etiqueta.",
  "git branch -d on the branches whose work is already in the main branch.":
    "git branch -d en las ramas cuyo trabajo ya está en la rama principal.",
  "Restores the tracked files and removes the new ones.":
    "Restaura los archivos seguidos y quita los nuevos.",
  "Updating {repo}": "Actualizando {repo}",
  "Updating the main branch": "Actualizando la rama principal",
  "git fetch of origin, then a fast-forward of the main branch; nothing is forced.":
    "git fetch de origin y luego avance rápido de la rama principal; no se fuerza nada.",
  "The main branch is up to date": "La rama principal está al día",
  "Working…": "Trabajando…",
  "{repo} is up to date": "{repo} está al día",
  "{repo} is back on its main branch": "{repo} volvió a su rama principal",
  "Branch created in {repo}": "Rama creada en {repo}",
  "The changes of {repo} are put aside":
    "Los cambios de {repo} quedaron guardados aparte",
  "Merged branches of {repo} deleted": "Ramas integradas de {repo} borradas",
  "The changes of {repo} are discarded": "Los cambios de {repo} se descartaron",
  "Deleted: {branches}": "Borradas: {branches}",
  "{ahead} ahead, {behind} behind {main}":
    "{ahead} adelante, {behind} atrás de {main}",
  "{n} not published": "{n} sin publicar",
  "Changes ({n})": "Cambios ({n})",
  "Nothing changed": "Sin cambios",
  "and {n} more": "y {n} más",
  History: "Historial",
  "New branch name": "Nombre de la rama nueva",
  "Create branch": "Crear rama",
  "Put changes aside": "Guardar cambios aparte",
  "Delete merged branches ({n})": "Borrar ramas integradas ({n})",
  "Discard changes…": "Descartar cambios…",
  "This deletes the uncommitted work and cannot be undone.":
    "Esto borra el trabajo sin guardar y no se puede deshacer.",
  Discard: "Descartar",
  "Reading…": "Leyendo…",
  "Changes, history and branches…": "Cambios, historial y ramas…",
  "Pull this branch": "Traer cambios de esta rama",
  "Brings in what origin has, only when nothing is uncommitted.":
    "Trae lo que tiene origen, solo si no hay nada sin guardar.",
  "Back to the main branch": "Volver a la rama principal",
  "Only when nothing is uncommitted.": "Solo si no hay nada sin guardar.",
  "Keeps them in the stash, new files included.":
    "Quedan en el stash, archivos nuevos incluidos.",
  "Delete merged branches": "Borrar ramas integradas",
  "Only the ones whose work is already in the main branch.":
    "Solo las que ya están dentro de la rama principal.",
  "Save changes (commit)…": "Guardar cambios (commit)…",
  "The agent writes the message and shows it to you first.":
    "El agente redacta el mensaje y te lo muestra antes.",
  "Push my branch…": "Subir mi rama…",
  "Open a pull request…": "Abrir un pull request…",
  "Work on it with the agent…": "Trabajar en él con el agente…",
  "Brings {repo} up to date with its origin; nothing is forced.":
    "Pone {repo} al día con su origen; no fuerza nada.",
  "could not update the main branch": "no se pudo actualizar la rama principal",
  "Main branch not updated: {reason}":
    "Rama principal sin actualizar: {reason}",
  local: "local",
  Update: "Actualizar",
  shared: "compartido",
  Artifacts: "Artefactos",
  "Nothing saved in this session yet. Ask for a report, a page or a diagram and it will show up here.":
    "Aún no hay nada guardado en esta sesión. Pide un informe, una página o un diagrama y aparecerá aquí.",
  "This session": "Esta sesión",
  "Whole project": "Todo el proyecto",
  "Upload a file": "Subir un archivo",
  "Pinned: it will not be deleted": "Fijado: no se borrará",
  "Deleted in {n} days unless pinned": "Se borrará en {n} días si no lo fijas",
  Repositories: "Repositorios",
  Private: "Privado",
  "Disconnected from {host}": "Desconectado de {host}",
  "Git connections": "Conexiones de git",
  Git: "Git",
  "Connect a git host to pick your repositories and clone private ones.":
    "Conecta un host de git para elegir tus repositorios y clonar los privados.",
  "Not connected": "No conectado",
  "Reconnect {host}": "Reconectar {host}",
  Reconnect: "Reconectar",
  "Disconnect {host}": "Desconectar {host}",
  Disconnect: "Desconectar",
  "Server type": "Tipo de servidor",
  "Server address": "Dirección del servidor",
  "Add self-hosted server": "Añadir servidor propio",
  Connected: "Conectado",
  "The login expired. Try again.": "El acceso expiró. Inténtalo de nuevo.",
  "The host did not authorize the login.": "El host no autorizó el acceso.",
  "CI passed": "CI aprobado",
  "Pull request {pr} is green: you can go and merge it.":
    "El pull request {pr} está en verde: ya puedes ir a mergearlo.",
  "This repository has no CI": "Este repositorio no tiene CI",
  "Pull request {pr} has no checks to wait for.":
    "El pull request {pr} no tiene checks que esperar.",
  "CI failed": "El CI falló",
  "Could not ask Motita to fix the CI":
    "No se pudo pedir a Motita que arregle el CI",
  "Could not create the pull request": "No se pudo crear el pull request",
  "Create PR": "Crear PR",
  "Pull request #{n}": "Pull request #{n}",
  "The CI is running": "El CI está corriendo",
  "CI running {done}/{total}": "CI corriendo {done}/{total}",
  "CI passed — ready to merge": "CI aprobado — listo para mergear",
  "No CI checks yet": "Aún no hay checks de CI",
  "Wait for the current turn to end": "Espera a que termine el turno actual",
  "Wait for the page to reload automatically":
    "Espera a que la página se recargue automáticamente",
  "The gateway is restarting with the new version.":
    "El gateway se está reiniciando con la nueva versión.",
  "The gateway did not come back in time. Reload the page to check again.":
    "El gateway no volvió a tiempo. Recarga la página para comprobarlo de nuevo.",
  "Reload page": "Recargar página",
  Projects: "Proyectos",
  Sessions: "Sesiones",
  "Commit, push and open a pull request with this session's work":
    "Hacer commit, push y abrir un pull request con el trabajo de esta sesión",
  "Motita is fixing it — attempt {n} of {max}.":
    "Motita lo está arreglando — intento {n} de {max}.",
  "Motita could not fix the CI": "Motita no pudo arreglar el CI",
  "New review comments": "Nuevos comentarios de revisión",
  "Motita is answering the comments on pull request {pr} — attempt {n} of {max}.":
    "Motita está atendiendo los comentarios del pull request {pr} — intento {n} de {max}.",
  "Merge conflict": "Conflicto de merge",
  "Pull request {pr} conflicts with its base branch: Motita is resolving it — attempt {n} of {max}.":
    "El pull request {pr} tiene conflictos con su rama base: Motita lo está resolviendo — intento {n} de {max}.",
  "It tried {n} times. Look at pull request {pr} and try again when you are ready.":
    "Lo intentó {n} veces. Revisa el pull request {pr} y vuelve a intentarlo cuando quieras.",
  "Pull request merged": "Pull request mergeado",
  "Could not merge the pull request": "No se pudo mergear el pull request",
  "CI failed — Motita gave up after {n} attempts":
    "El CI falló — Motita se rindió tras {n} intentos",
  "CI failed — Motita is fixing it ({n}/{max})":
    "El CI falló — Motita lo está arreglando ({n}/{max})",
  "Try again": "Reintentar",
  "Merge this pull request on the host": "Mergear este pull request en el host",
  "Click again to merge": "Clic de nuevo para mergear",
  Merge: "Mergear",
  Merged: "Mergeado",
  "Pull request #{n} merged": "Pull request #{n} mergeado",
  "The CI is red on the base branch too":
    "El CI también está en rojo en la rama base",
  "Pull request {pr} fails the same checks as the branch it merges into: this change did not cause it.":
    "El pull request {pr} falla los mismos checks que la rama en la que se mergea: este cambio no lo causó.",
  "Also failing on the base branch — not caused by this change":
    "También falla en la rama base — no lo causó este cambio",
  "Merge method": "Método de merge",
  "Merge commit": "Commit de merge",
  Squash: "Squash",
  Rebase: "Rebase",
  "How a pull request of this project is merged from the PR bar. Its branch is deleted afterwards.":
    "Cómo se mergea un pull request de este proyecto desde la barra de PR. Su rama se borra después.",
  "Attempts to fix a failing CI": "Intentos para arreglar un CI que falla",
  "Merge automatically when the CI passes":
    "Mergear automáticamente cuando el CI pase",
  "Only when the host accepts the merge. Off, merging is always your click.":
    "Solo cuando el host acepta el merge. Apagado, mergear siempre es tu clic.",
  "Open a new session from the updated project branch":
    "Abrir una sesión nueva desde la rama actualizada del proyecto",
  "Continue in a new session": "Continuar en una sesión nueva",
  "Waiting for approvals or required checks":
    "Esperando aprobaciones o checks requeridos",
  "Conflicts with the base branch": "Tiene conflictos con la rama base",
  "Still a draft": "Sigue en borrador",
  "Out of date with the base branch": "Desactualizado respecto a la rama base",
  "Pull request closed": "Pull request cerrado",
  "Phone notifications": "Notificaciones en el celular",
  "This browser cannot receive push notifications.":
    "Este navegador no puede recibir notificaciones push.",
  "This gateway cannot send them: it has no folder to keep them in, or it is older than this page.":
    "Este gateway no puede enviarlas: no tiene una carpeta donde guardarlas, o es más antiguo que esta página.",
  "Notifications are blocked for this site: allow them first (see the steps above).":
    "Las notificaciones están bloqueadas para este sitio: permítelas primero (mira los pasos de arriba).",
  "Even with Motita closed: the gateway sends them to this device when a CI ends.":
    "Aunque Motita esté cerrado: el gateway las envía a este dispositivo cuando termina un CI.",
  "The browser did not allow notifications.":
    "El navegador no permitió las notificaciones.",
  "Sending…": "Enviando…",
  "Test on this device": "Probar en este dispositivo",
  "The gateway sends one now, the same way a CI that ends will.":
    "El gateway envía una ahora, igual que cuando termine un CI.",
  "Pull request {pr} was closed without being merged.":
    "El pull request {pr} se cerró sin mergearse.",
  "0 uses the default (5). After this many attempts Motita stops and asks you to take a look.":
    "0 usa el valor por defecto (5). Tras esta cantidad de intentos Motita se detiene y te pide que le eches un vistazo.",
  "Notice when a CI ends": "Aviso cuando termine un CI",
  "Suggest the next request": "Sugerir el siguiente pedido",
  "After a turn, the empty field hints at what to ask next.":
    "Al terminar un turno, el campo vacío sugiere qué pedir después.",
  "Use the suggestion": "Usar la sugerencia",
  "A message inside Motita. Works on any connection.":
    "Un mensaje dentro de Motita. Funciona con cualquier conexión.",
  "Desktop notification": "Notificación de escritorio",
  Voice: "Voz",
  "Microphone button": "Botón de micrófono",
  "Dictate into the message box. You read and edit the text before sending it.":
    "Dicta en el cuadro de mensaje. Lees y editas el texto antes de enviarlo.",
  "Listen for the wake phrase": "Escuchar la frase de activación",
  "Only while this tab is open. What you say is sent to the transcription service. Say: {phrases}":
    "Solo con esta pestaña abierta. Lo que digas se envía al servicio de transcripción. Di: {phrases}",
  "Connection not secure": "Conexión no segura",
  "Why?": "¿Por qué?",
  "How to set it up": "Cómo configurarlo",
  "Browsers only allow it on a secure address: one that starts with https://, or localhost on this same computer. Open Motita through https to use it.":
    "Los navegadores solo lo permiten en una dirección segura: una que empiece con https://, o localhost en este mismo computador. Abre Motita por https para usarlo.",
  "Your browser blocks the microphone because Motita was opened without a secure connection.":
    "Tu navegador bloquea el micrófono porque Motita se abrió sin una conexión segura.",
  "Your browser blocks notifications because Motita was opened without a secure connection.":
    "Tu navegador bloquea las notificaciones porque Motita se abrió sin una conexión segura.",
};

const es: Record<string, string> = { ...first, ...more };

export default es;
