// The Spanish dictionary, keyed by the English text the interface is written in. It is loaded
// only when Spanish is chosen (see i18n.ts), so English readers never download it.
//
// Neutral Latin-American Spanish, addressing the reader as "tú". A key that is missing here is
// shown in English, never as an empty string.

const es: Record<string, string> = {
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
  "Apply this one": "Aplicar esta",
  "Apply this one and the following": "Aplicar esta y las siguientes",
  "Apply none": "No aplicar ninguna",
  "Applying suggestions": "Aplicando sugerencias",
  "Not answered before the conversation moved on; no longer offered.":
    "No se respondieron antes de seguir la conversación; ya no se ofrecen.",
  "will apply": "se aplicará",
  "not applied": "no se aplica",
  "Apply these suggestions you made, and nothing else:":
    "Aplica estas sugerencias que hiciste, y nada más:",
  "(active)": "(activo)",
  "(auto from git URL if empty)": "(se toma de la URL de git si está vacío)",
  "({n} event(s) were not kept while nothing was listening)":
    "({n} evento(s) no se guardaron mientras nadie escuchaba)",
  "1 action": "1 acción",
  "1 agent": "1 agente",
  "1 background agent running": "1 agente en segundo plano trabajando",
  "1 change": "1 cambio",
  "1 command": "1 comando",
  "1 file changed": "1 archivo cambiado",
  "1 thought": "1 pensamiento",
  "1 uncommitted change in this working tree":
    "1 cambio sin commitear en este árbol de trabajo",
  "1 uncommitted change will be discarded":
    "Se descartará 1 cambio sin commitear",
  "A folder created under the workspace. Simple name, no paths.":
    "Una carpeta creada dentro del workspace. Nombre simple, sin rutas.",
  "A session's checkout could not be inspected":
    "No se pudo revisar el checkout de una sesión",
  "A turn is running: it finishes with the current model, and the new one answers from the next turn.":
    "Hay un turno en curso: termina con el modelo actual y el nuevo responde desde el próximo turno.",
  "API key": "Clave de API",
  "Allow all this session": "Permitir todo en esta sesión",
  "Any running tasks will be interrupted.":
    "Las tareas en curso se interrumpirán.",
  Archived: "Archivadas",
  model: "modelo",
  "Model switched to {model} in this session":
    "Modelo cambiado a {model} en esta sesión",
  "Model: {provider} / {model}": "Modelo: {provider} / {model}",
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
  Automatic: "Automático",
  "Back at the checkpoint — conversation and files":
    "De vuelta en el checkpoint — conversación y archivos",
  "Back at the checkpoint — conversation only":
    "De vuelta en el checkpoint — solo la conversación",
  "Back to the list": "Volver a la lista",
  "Branch: {branch}": "Rama: {branch}",
  Cancel: "Cancelar",
  "Checking credentials…": "Verificando credenciales…",
  "Checking latest release": "Buscando la última versión",
  "Upgrade complete. The gateway is restarting.":
    "Actualización completa. El gateway se está reiniciando.",
  "Checking what this project's sessions are holding…":
    "Revisando qué tienen las sesiones de este proyecto…",
  "Checking what this session is holding…": "Revisando qué tiene esta sesión…",
  Checkpoint: "Checkpoint",
  "Checkpoint menu": "Menú del checkpoint",
  "The conversation so far was written with {model}; {next} takes it over from the next turn and may answer differently. Open a new session to keep each model on its own.":
    "La conversación hasta ahora se escribió con {model}; {next} la retoma desde el próximo turno y puede responder distinto. Abre una sesión nueva para mantener cada modelo por separado.",
  "The turn in progress finishes with the previous model.":
    "El turno en curso termina con el modelo anterior.",
  "This conversation already has messages: the new model takes them over and may answer differently. A new session keeps each model on its own.":
    "Esta conversación ya tiene mensajes: el nuevo modelo los retoma y puede responder distinto. Una sesión nueva mantiene cada modelo por separado.",
  You: "Tú",
  Agent: "Agente",
  "The command could not be run": "No se pudo ejecutar el comando",
  "Run in the shell": "Ejecutar en el shell",
  Send: "Enviar",
  "Edit project": "Editar proyecto",
  "Main branch": "Rama principal",
  "The project always goes back to this branch when it has no sessions.":
    "El proyecto siempre vuelve a esta rama cuando no tiene sesiones.",
  "The project always goes back to this branch when it has no sessions. A cloned repository uses it when it has it.":
    "El proyecto siempre vuelve a esta rama cuando no tiene sesiones. Un repositorio clonado la usa si la tiene.",
  "It is on {branch} now.": "Ahora está en {branch}.",
  "Could not save the project": "No se pudo guardar el proyecto",
  "Project saved": "Proyecto guardado",
  "How do you want to start?": "¿Cómo quieres empezar?",
  "Empty project": "Proyecto vacío",
  "A new folder to start from scratch":
    "Una carpeta nueva para empezar desde cero",
  "From a Git repository": "Desde un repositorio Git",
  "Clone a repository from GitHub, GitLab or any URL":
    "Clona un repositorio de GitHub, GitLab o de cualquier URL",
  Back: "Atrás",
  Name: "Nombre",
  "Repository URL": "URL del repositorio",
  "The repository name": "El nombre del repositorio",
  "More options": "Más opciones",
  "Made from the name when empty. A folder under the workspace, a simple name with no paths.":
    "Se crea a partir del nombre si está vacío. Una carpeta dentro del workspace, un nombre simple sin rutas.",
  "Creating the project…": "Creando el proyecto…",
  "Creating…": "Creando…",
  "Clone and create": "Clonar y crear",
  "The repository uses {branch}.": "El repositorio usa {branch}.",
  "Run menu": "Menú de la ejecución",
  "Stop the agent": "Detener al agente",
  "Enter sends a message that interrupts; Alt+Enter queues it":
    "Enter envía un mensaje que interrumpe; Alt+Enter lo deja en cola",
  "Click Continue in the session menu to keep working from the latest project branch.":
    "Haz clic en Continuar en el menú de la sesión para seguir trabajando desde la rama más reciente del proyecto.",
  "Click again: discard changes since":
    "Haz clic de nuevo: descartar los cambios desde entonces",
  "Cloning repository…": "Clonando el repositorio…",
  "Cloning…": "Clonando…",
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
  "Git URL or SSH (optional — clones instead of creating a folder)":
    "URL de git o SSH (opcional — clona en vez de crear una carpeta)",
  "Git has no user configured. It is saved as your global git identity and signs the commits of every repository.":
    "Git no tiene un usuario configurado. Se guarda como tu identidad global de git y firma los commits de todos los repositorios.",
  "Git user": "Usuario de git",
  "go back to the conversation you left last":
    "vuelve a la última conversación que dejaste",
  "Go back and restore the changes": "Volver y restaurar los cambios",
  "Go back to this checkpoint": "Volver a este checkpoint",
  "HTTPS or SSH. When set, the repo is cloned into the folder name above.":
    "HTTPS o SSH. Si lo indicas, el repositorio se clona en la carpeta de arriba.",
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
  General: "General",
  Appearance: "Apariencia",
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
  "Create a session to start talking to the agent.":
    "Crea una sesión para empezar a hablar con el agente.",
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
  'Project "{title}" created and repo cloned':
    'Proyecto "{title}" creado y repositorio clonado',
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
  "Run this and every later command in this session without asking. Commands the policy forbids stay forbidden.":
    "Ejecutar este y todos los comandos siguientes de esta sesión sin preguntar. Lo que la política prohíbe sigue prohibido.",
  "Save and create": "Guardar y crear",
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
  "This project, all its sessions and its folder will be permanently deleted. This cannot be undone.":
    "Este proyecto, todas sus sesiones y su carpeta se eliminarán para siempre. No se puede deshacer.",
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
  "Worktree: {worktree}": "Worktree: {worktree}",
  "Worth knowing": "Conviene saber",
  "[no result]": "[sin resultado]",
  agent: "agente",
  agents: "agentes",
  "all allowed": "todo permitido",
  "all commands allowed": "todos los comandos permitidos",
  "analysing the task...": "analizando la tarea...",
  "approved (all commands allowed in this session): {x}":
    "aprobado (todos los comandos permitidos en esta sesión): {x}",
  "argument…": "argumento…",
  "asking you to approve: {x}": "pidiendo tu aprobación: {x}",
  branch: "rama",
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
  "https://github.com/user/repo.git  or  git@github.com:user/repo.git":
    "https://github.com/user/repo.git  o  git@github.com:user/repo.git",
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
  worktree: "worktree",
  "{n} actions": "{n} acciones",
  "{n} agents": "{n} agentes",
  "{n} background agents running": "{n} agentes en segundo plano trabajando",
  "{n} changes": "{n} cambios",
  "{n} commands": "{n} comandos",
  "{n} files changed": "{n} archivos cambiados",
  "{n} sessions finished": "{n} sesiones terminaron",
  "{n} thoughts": "{n} pensamientos",
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
  "Choose from my repositories": "Elegir de mis repositorios",
  "Podman was found on this machine. Use it to run this project?":
    "Se encontró Podman en esta máquina. ¿Quieres usarlo para levantar este proyecto?",
  Artifacts: "Artefactos",
  "Nothing saved in this session yet. Ask for a report, a page or a diagram and it will show up here.":
    "Aún no hay nada guardado en esta sesión. Pide un informe, una página o un diagrama y aparecerá aquí.",
  "This session": "Esta sesión",
  "Whole project": "Todo el proyecto",
  "Upload a file": "Subir un archivo",
  "podman is installed but did not answer (podman info failed), so it may not work until it is set up.":
    "podman está instalado pero no respondió (falló podman info), así que puede que no funcione hasta configurarlo.",
  "Pinned: it will not be deleted": "Fijado: no se borrará",
  "Deleted in {n} days unless pinned": "Se borrará en {n} días si no lo fijas",
  "Stop using podman": "Dejar de usar podman",
  "Run with podman": "Levantar con podman",
  "This project is run with podman": "Este proyecto se levanta con podman",
  "The project will be started in containers with podman. podman compose was not found, so compose files will not work until it is installed.":
    "El proyecto se levantará en contenedores con podman. No se encontró podman compose, así que los archivos compose no funcionarán hasta instalarlo.",
  "The project will be started in containers with podman.":
    "El proyecto se levantará en contenedores con podman.",
  "Git host": "Host de git",
  "Connect another host…": "Conectar otro host…",
  "Search repositories": "Buscar repositorios",
  'No repositories match "{q}".': 'Ningún repositorio coincide con "{q}".',
  "No repositories found in {host}.":
    "No se encontraron repositorios en {host}.",
  Repositories: "Repositorios",
  Private: "Privado",
  "Loading repositories…": "Cargando repositorios…",
  "Load more": "Cargar más",
  "Disconnected from {host}": "Desconectado de {host}",
  "Git connections": "Conexiones de git",
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
  "Connect GitHub, GitLab or Bitbucket to pick one of your repositories":
    "Conecta GitHub, GitLab o Bitbucket para elegir uno de tus repositorios",
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
  "This browser cannot record audio.": "Este navegador no puede grabar audio.",
  "Off on the gateway: set voice.enabled in the configuration.":
    "Desactivado en el gateway: activa voice.enabled en la configuración.",
  "Add voice.wake_phrases to the configuration first.":
    "Primero agrega voice.wake_phrases a la configuración.",
  "Turn on speech input above first.":
    "Primero activa la entrada de voz, más arriba.",
  "Add a wake phrase above first.":
    "Primero agrega una frase de activación, más arriba.",
  "The recording is not stored, by the gateway or by this page.":
    "La grabación no se guarda, ni en el gateway ni en esta página.",
  "A key typed here is kept beside the configuration and never shown again.":
    "La clave que escribas aquí se guarda junto a la configuración y no se vuelve a mostrar.",
  "This provider has no transcription service of its own: name one.":
    "Este proveedor no tiene un servicio de transcripción propio: indica uno.",
  "What the microphone hears is sent to {endpoint} to be turned into text.":
    "Lo que escucha el micrófono se envía a {endpoint} para pasarlo a texto.",
  "Off. Turn it on to dictate and to use the wake phrase.":
    "Desactivada. Actívala para dictar y usar la frase de activación.",
  "Recordings go to {endpoint} ({model}).":
    "Las grabaciones van a {endpoint} ({model}).",
  "With the language model's own key.":
    "Con la misma clave del modelo de lenguaje.",
  "Speech input": "Entrada de voz",
  "Transcription service": "Servicio de transcripción",
  Change: "Cambiar",
  "Change the transcription service?": "¿Cambiar el servicio de transcripción?",
  "Turn on speech input?": "¿Activar la entrada de voz?",
  "Saved. Paste a new one to replace it":
    "Guardada. Pega una nueva para reemplazarla",
  "Not needed for a local server": "No hace falta para un servidor local",
  "Wake phrases": "Frases de activación",
  "Separated by commas. Listening hands-free starts a message when it hears one.":
    "Separadas por comas. La escucha manos libres empieza un mensaje cuando oye una.",
  "hey motita": "oye motita",
  "Saved. Applies when the gateway restarts: run motita gateway stop, then motita gateway start.":
    "Guardado. Se aplica al reiniciar el gateway: ejecuta motita gateway stop y luego motita gateway start.",
  "The browser was not allowed to use the microphone: allow it in this site's settings, then try again.":
    "El navegador no tiene permiso para usar el micrófono: permítelo en los ajustes de este sitio y vuelve a intentarlo.",
  "The chosen microphone is not connected: the default one is used.":
    "El micrófono elegido no está conectado: se usa el predeterminado.",
  "Allow the microphone to see the names of the ones connected.":
    "Permite el micrófono para ver los nombres de los que están conectados.",
  "Used for dictation and the wake phrase, on this device only.":
    "Se usa para dictar y para la frase de activación, solo en este dispositivo.",
  "Microphone {n}": "Micrófono {n}",
  Microphone: "Micrófono",
  "System default": "Predeterminado del sistema",
  Dictate: "Dictar",
  "Stop and transcribe": "Detener y transcribir",
  "Transcribing…": "Transcribiendo…",
  "Listening for the wake phrase": "Escuchando la frase de activación",
  "Speech input failed": "Falló la entrada de voz",
  "Heard “{phrase}”": "Escuché “{phrase}”",
  "This browser cannot show notifications.":
    "Este navegador no puede mostrar notificaciones.",
  "On iPhone and iPad they only work in the installed app: tap Share, then Add to Home Screen, and open Motita from there.":
    "En iPhone y iPad solo funcionan en la app instalada: toca Compartir, luego Agregar a inicio, y abre Motita desde ahí.",
  "Blocked in this browser. To turn them back on:":
    "Bloqueadas en este navegador. Para volver a activarlas:",
  "The browser did not ask. To allow them by hand:":
    "El navegador no mostró la pregunta. Para permitirlas a mano:",
  "Allow notifications": "Permitir notificaciones",
  "Check again": "Comprobar de nuevo",
  "Come back here: the switch shows up on its own.":
    "Vuelve aquí: el interruptor aparece solo.",
  "Open the Settings app, then Notifications.":
    "Abre la app Ajustes y entra en Notificaciones.",
  "Find Motita in the list and turn on Allow Notifications.":
    "Busca Motita en la lista y activa Permitir notificaciones.",
  "Touch and hold the Motita icon, then tap App info.":
    "Mantén pulsado el icono de Motita y toca Información de la app.",
  "Open Notifications and turn them on.":
    "Entra en Notificaciones y actívalas.",
  "Tap the icon to the left of the address, then Permissions.":
    "Toca el icono a la izquierda de la dirección y luego Permisos.",
  "Turn Notifications on.": "Activa Notificaciones.",
  "Click the icon to the left of the address.":
    "Haz clic en el icono a la izquierda de la dirección.",
  "Next to Send notifications, click × to clear the block.":
    "Junto a Enviar notificaciones, haz clic en × para quitar el bloqueo.",
  "Reload the page and press Allow here.":
    "Recarga la página y pulsa Permitir aquí.",
  "Open Safari, then Settings, then Websites, then Notifications.":
    "Abre Safari, luego Ajustes, Sitios web y Notificaciones.",
  "Set this site to Allow.": "Pon este sitio en Permitir.",
  "Open the app's menu (⋮ or ⋯ at the top), then App info.":
    "Abre el menú de la app (⋮ o ⋯ arriba) y luego Información de la app.",
  "Set Notifications to Allow (or press Reset permission).":
    "Pon Notificaciones en Permitir (o pulsa Restablecer permiso).",
  "Reload the page.": "Recarga la página.",
  "Open this site's settings in the browser (usually the icon to the left of the address).":
    "Abre los ajustes de este sitio en el navegador (suele ser el icono a la izquierda de la dirección).",
  "Allow notifications.": "Permite las notificaciones.",
  "Test notification": "Notificación de prueba",
  "Shows one now, to check that they arrive.":
    "Muestra una ahora, para comprobar que llegan.",
  "Motita notifications work": "Las notificaciones de Motita funcionan",
  "This is how a CI that ends will look.": "Así se verá cuando termine un CI.",
  "Sent. If nothing showed up, check the system's do-not-disturb mode.":
    "Enviada. Si no apareció, revisa el modo no molestar del sistema.",
  "The browser did not show it.": "El navegador no la mostró.",
  "Only while this tab is in the background. The browser has to allow it first.":
    "Solo mientras esta pestaña está en segundo plano. Primero el navegador tiene que permitirlo.",
  "Only while this tab is in the background.":
    "Solo mientras esta pestaña está en segundo plano.",
  Allow: "Permitir",
  "Pull request {pr} was merged. Use Continue in the bar to keep working from the updated branch.":
    "El pull request {pr} fue mergeado. Usa Continuar en la barra para seguir desde la rama actualizada.",
  "{n} pull requests moved": "{n} pull requests cambiaron",
  "Open a new session when a pull request is merged":
    "Abrir una sesión nueva cuando se mergee un pull request",
  "It starts from the updated branch, so the next piece of work has somewhere to begin.":
    "Parte de la rama actualizada, para que el siguiente trabajo tenga dónde empezar.",
  "A new session was opened from the updated branch":
    "Se abrió una sesión nueva desde la rama actualizada",
  "Open the new session": "Abrir la sesión nueva",
  "Pull request {pr} was merged. A new session was opened from the updated branch.":
    "El pull request {pr} fue mergeado. Se abrió una sesión nueva desde la rama actualizada.",
  "“{title}” finished": "“{title}” terminó",
  "Remove {name}": "Quitar {name}",
  "Discard the changes and remove it": "Descartar los cambios y quitarlo",
  "https://github.com/user/other-repo.git":
    "https://github.com/usuario/otro-repo.git",
  "Add a repository": "Agregar un repositorio",
  Add: "Agregar",
  "The project already has a repository with that name.":
    "El proyecto ya tiene un repositorio con ese nombre.",
  "That is not a repository URL.": "Esa no es la URL de un repositorio.",
  "A new folder": "Una carpeta nueva",
  "The first one is the main repository, the project's folder. The others are cloned inside it, and every session gets its own copy of each.":
    "El primero es el repositorio principal, la carpeta del proyecto. Los demás se clonan dentro de él, y cada sesión recibe su propia copia de cada uno.",
  "Adding or removing a repository happens right away. Open sessions get a copy of a new one; removing one deletes its folder.":
    "Agregar o quitar un repositorio ocurre al instante. Las sesiones abiertas reciben una copia del nuevo; quitar uno borra su carpeta.",
  "Could not add {name}": "No se pudo agregar {name}",
  "Could not remove {name}": "No se pudo quitar {name}",
  'Project "{title}" created, but some repositories could not be added: {names}':
    'Proyecto "{title}" creado, pero no se pudieron agregar algunos repositorios: {names}',
  Providers: "Proveedores",
  "Connect the AI providers Motita can use. The models each kind of work runs on are chosen in Models.":
    "Conecta los proveedores de IA que Motita puede usar. Los modelos de cada tipo de trabajo se eligen en Modelos.",
  "A key you type here travels to Motita unencrypted, because it was opened without a secure connection.":
    "Una clave que escribas aquí viaja a Motita sin cifrar, porque se abrió sin una conexión segura.",
  "Only an address that starts with https:// (or localhost on this same computer) encrypts what you send. Open Motita through https before pasting a key.":
    "Solo una dirección que empieza con https:// (o localhost en este mismo computador) cifra lo que envías. Abre Motita por https antes de pegar una clave.",
  "This gateway runs without a configuration file, so nothing can be saved here. Run motita config on its machine first.":
    "Este gateway funciona sin archivo de configuración, así que aquí no se puede guardar nada. Ejecuta primero motita config en su máquina.",
  "Default model": "Modelo por defecto",
  Default: "Por defecto",
  "Sessions already open keep their own model.":
    "Las sesiones ya abiertas mantienen su propio modelo.",
  "Connect a provider in Providers first.":
    "Primero conecta un proveedor en Proveedores.",
  Models: "Modelos",
  "Pick the model new sessions start with, and a model for each kind of work if you want one.":
    "Elige el modelo con el que empiezan las sesiones nuevas y, si quieres, un modelo para cada tipo de trabajo.",
  "Model per kind of work": "Modelo por tipo de trabajo",
  "Same as default": "Igual que el por defecto",
  "Model for {kind}": "Modelo para {kind}",
  "Other…": "Otro…",
  "Model name, as the provider writes it":
    "Nombre del modelo, tal como lo escribe el proveedor",
  "Models per kind of work saved": "Modelos por tipo de trabajo guardados",
  "Each session's agent and its background agents use these models, and work as a team only inside their own session.":
    "El agente de cada sesión y sus agentes en segundo plano usan estos modelos, y trabajan en equipo solo dentro de su propia sesión.",
  Planning: "Planificación",
  "Plan mode: explores and proposes, changes nothing.":
    "Modo plan: explora y propone, no cambia nada.",
  Coding: "Programación",
  "Task mode, and the background agents that write.":
    "Modo tarea, y los agentes en segundo plano que escriben.",
  Review: "Revisión",
  "Background agents that review the work and change nothing.":
    "Agentes en segundo plano que revisan el trabajo y no cambian nada.",
  Research: "Investigación",
  "Background agents that investigate and change nothing.":
    "Agentes en segundo plano que investigan y no cambian nada.",
  Summaries: "Resúmenes",
  "Titles, conversation summaries and auto mode's guess.":
    "Títulos, resúmenes de la conversación y la elección del modo auto.",
  "New sessions will start with {model}":
    "Las sesiones nuevas empezarán con {model}",
  "Connected · key saved": "Conectado · clave guardada",
  "Connected · key from {name}": "Conectado · clave desde {name}",
  "Connected · key in the configuration file":
    "Conectado · clave en el archivo de configuración",
  "Connected · signed in": "Conectado · sesión iniciada",
  "Ready · no key needed": "Listo · no necesita clave",
  Manage: "Gestionar",
  "Set up {name}": "Configurar {name}",
  "It uses the login of its own command: run {cmd} on the gateway's machine.":
    "Usa el inicio de sesión de su propio comando: ejecuta {cmd} en la máquina del gateway.",
  "Your own server needs no key.": "Tu propio servidor no necesita clave.",
  "The key comes from {name} on the gateway's machine, and it wins over one saved here.":
    "La clave viene de {name} en la máquina del gateway, y tiene prioridad sobre una guardada aquí.",
  "The key is written in the configuration file (llm.api_key). Remove it from there to manage the key here.":
    "La clave está escrita en el archivo de configuración (llm.api_key). Quítala de ahí para gestionarla desde aquí.",
  "Get a key": "Obtener una clave",
  "Paste the key": "Pega la clave",
  Endpoint: "Endpoint",
  "It works: {n} models available.": "Funciona: {n} modelos disponibles.",
  "It did not connect: {err}": "No se pudo conectar: {err}",
  "Test connection": "Probar conexión",
  "Testing…": "Probando…",
  "{name} saved": "{name} guardado",
  "The key comes from an environment variable on the gateway's machine, which wins over one saved here.":
    "La clave viene de una variable de entorno en la máquina del gateway, que tiene prioridad sobre una guardada aquí.",
  "That address is plain http outside your network: the key would travel unencrypted. Use https.":
    "Esa dirección es http sin cifrar fuera de tu red: la clave viajaría sin cifrar. Usa https.",
  "Type the key again to try it on a new address: a saved key is only sent where it was saved for.":
    "Vuelve a escribir la clave para probarla en una dirección nueva: una clave guardada solo se envía a donde se guardó.",
  "API key from platform.openai.com": "Clave de API de platform.openai.com",
  "sign in with ChatGPT, or an API key":
    "inicia sesión con ChatGPT, o una clave de API",
  "sign in with GitHub (Copilot plan)":
    "inicia sesión con GitHub (plan de Copilot)",
  "free on your own machine, or Ollama Cloud":
    "gratis en tu propia máquina, u Ollama Cloud",
  "API key from console.anthropic.com": "Clave de API de console.anthropic.com",
  "your Claude plan, via the Claude Code CLI":
    "tu plan de Claude, mediante la CLI de Claude Code",
  "sign in with Google, or an AI Studio key":
    "inicia sesión con Google, o una clave de AI Studio",
  "sign in with qwen.ai, or a DashScope key":
    "inicia sesión con qwen.ai, o una clave de DashScope",
  "Saved ({hint}). Paste a new one to replace it":
    "Guardada ({hint}). Pega una nueva para reemplazarla",
  "Signed in to {name}": "Sesión iniciada en {name}",
  "Starting…": "Iniciando…",
  "Sign in again": "Volver a iniciar sesión",
  "Sign in with your account": "Iniciar sesión con tu cuenta",
  "Open the link and type this code:": "Abre el enlace y escribe este código:",
  "Open the sign-in page": "Abrir la página de inicio de sesión",
  "If the page ends on an address that does not load, paste that address here.":
    "Si la página termina en una dirección que no carga, pega esa dirección aquí.",
  "Address the browser ended on": "Dirección en la que terminó el navegador",
  "Waiting for the provider…": "Esperando al proveedor…",
  "The sign-in did not finish: {err}": "El inicio de sesión no terminó: {err}",
  Network: "Red",
  "Who can connect": "Quién puede conectarse",
  "This machine only": "Solo este equipo",
  "Local network": "Red local",
  Everywhere: "Cualquier lugar",
  "Custom address": "Dirección personalizada",
  "Safest: only this machine can open Motita (127.0.0.1).":
    "La opción más segura: solo este equipo puede abrir Motita (127.0.0.1).",
  "Listens on every interface, but only private (local network) addresses may connect. This machine only is the safest choice.":
    "Escucha en todas las interfaces, pero solo pueden conectarse direcciones privadas (de la red local). Solo este equipo es la opción más segura.",
  "Listens on 0.0.0.0: any address that can reach this machine may connect. This machine only is the safest choice.":
    "Escucha en 0.0.0.0: puede conectarse cualquier dirección que llegue a este equipo. Solo este equipo es la opción más segura.",
  "Set by hand in the configuration: {listen}. Choosing an option replaces it.":
    "Configurada a mano en la configuración: {listen}. Elegir una opción la reemplaza.",
  "The gateway runs on its built-in defaults, without a configuration file, so this cannot be changed here.":
    "El gateway usa sus valores predeterminados, sin archivo de configuración, así que esto no se puede cambiar aquí.",
  "Switch to “{mode}”?": "¿Cambiar a “{mode}”?",
  "Other devices will lose access: only this machine will be able to open Motita.":
    "Los demás dispositivos perderán el acceso: solo este equipo podrá abrir Motita.",
  "Devices on your local network will be able to reach the gateway.":
    "Los dispositivos de tu red local podrán acceder al gateway.",
  "Anyone who can reach this machine over the network will be able to reach the gateway.":
    "Cualquiera que pueda llegar a este equipo por la red podrá acceder al gateway.",
  "The gateway speaks plain HTTP: the token travels unencrypted over the network.":
    "El gateway usa HTTP simple: el token viaja sin cifrar por la red.",
  "The token is the only thing protecting a program that runs commands on this machine.":
    "El token es lo único que protege un programa que ejecuta comandos en este equipo.",
  "The change is saved to the configuration and applies when the gateway restarts: run motita gateway stop, then motita gateway start.":
    "El cambio se guarda en la configuración y se aplica cuando el gateway se reinicia: ejecuta motita gateway stop y luego motita gateway start.",
  "Saved. Applies when the gateway restarts":
    "Guardado. Se aplica cuando el gateway se reinicie",
  "Running now: {mode} ({listen}). Restart it with motita gateway stop, then motita gateway start.":
    "Ahora en uso: {mode} ({listen}). Reinícialo con motita gateway stop y luego motita gateway start.",
  "Not installed. Tailscale gives you private https access from your own devices, without opening the gateway to the network.":
    "No está instalado. Tailscale te da acceso privado por https desde tus propios dispositivos, sin abrir el gateway a la red.",
  "Get Tailscale": "Descargar Tailscale",
  "Tailscale is installed but not connected ({state}).":
    "Tailscale está instalado pero no conectado ({state}).",
  "Tailscale is installed but not connected.":
    "Tailscale está instalado pero no conectado.",
  "Publish in my tailnet (https)": "Publicar en mi tailnet (https)",
  "Only the devices in your tailnet can open it, with https; not the internet.":
    "Solo los dispositivos de tu tailnet pueden abrirlo, con https; no internet.",
  "Published at": "Publicado en",
  "Publish in your tailnet?": "¿Publicar en tu tailnet?",
  "This runs tailscale serve: the gateway is published with https only to the devices in your tailnet, not to the internet.":
    "Esto ejecuta tailscale serve: el gateway se publica con https solo para los dispositivos de tu tailnet, no en internet.",
  "Anyone on those devices still needs the token to get in.":
    "Quien use esos dispositivos igual necesita el token para entrar.",
  Publish: "Publicar",
  "Tailscale: {error}": "Tailscale: {error}",
  "Running now: {mode} ({listen}).": "Ahora en uso: {mode} ({listen}).",
  "Restart now": "Reiniciar ahora",
  "Restart the gateway now?": "¿Reiniciar el gateway ahora?",
  Restart: "Reiniciar",
  "The gateway restarts to apply the saved setting.":
    "El gateway se reinicia para aplicar la configuración guardada.",
  "Running work is interrupted for a moment; saved sessions resume after the restart.":
    "El trabajo en curso se interrumpe un momento; las sesiones guardadas se reanudan después del reinicio.",
  "If the new setting no longer reaches this device (for example, you chose “This machine only” and opened this page from another device), this page will stop working: open it from the gateway's machine.":
    "Si la nueva configuración ya no llega a este dispositivo (por ejemplo, elegiste “Solo este equipo” y abriste esta página desde otro dispositivo), esta página dejará de funcionar: ábrela desde el equipo del gateway.",
  "Restarting the gateway…": "Reiniciando el gateway…",
  "The gateway did not come back here. If you changed who can reach it, open it from an address that the new setting allows.":
    "El gateway no volvió aquí. Si cambiaste quién puede acceder, ábrelo desde una dirección que la nueva configuración permita.",
  "Then turn the switch on again.": "Luego vuelve a activar el interruptor.",
  link: "enlace",
  "Code block.": "Bloque de código.",
  "Formula.": "Fórmula.",
  "Table.": "Tabla.",
  "The rest is on screen.": "El resto está en pantalla.",
  "Reading aloud failed": "No se pudo leer en voz alta",
  "Hi, I am Motita. I opened the PR and the CI passed.":
    "Hola, soy Motita. Abrí el PR y el CI pasó.",
  "The browser wants a tap first: press 🔊 on an answer once.":
    "El navegador pide un toque primero: presiona 🔊 en una respuesta una vez.",
  "This device has no voice for this language.":
    "Este dispositivo no tiene una voz para este idioma.",
  "Stop reading": "Dejar de leer",
  "Read aloud": "Leer en voz alta",
  "Read answers aloud": "Leer las respuestas en voz alta",
  "This browser cannot read aloud.":
    "Este navegador no puede leer en voz alta.",
  "With the voices of this device. Alt+L or 🔊 on an answer reads it or stops.":
    "Con las voces de este dispositivo. Alt+L o 🔊 en una respuesta la lee o la detiene.",
  Off: "Apagado",
  "When I spoke": "Cuando hablé",
  Always: "Siempre",
  "This device has no voice for the interface language.":
    "Este dispositivo no tiene voz para el idioma de la interfaz.",
  "Device default": "La del dispositivo",
  Speed: "Velocidad",
  Length: "Largo",
  "A longer answer is read up to its first paragraph.":
    "Una respuesta más larga se lee hasta su primer párrafo.",
  Short: "Corta",
  Medium: "Media",
  Long: "Larga",
  Whole: "Completa",
  "Try the voice": "Probar la voz",
  Try: "Probar",
  "What it says": "Qué dice",
  "Natural: the session's model says what the agent is doing while it works, and the gist of the answer at the end. Each line is a short extra call to the model.":
    "Natural: el modelo de la sesión te cuenta qué hace el agente mientras trabaja y, al final, lo esencial de la respuesta. Cada frase es una llamada corta extra al modelo.",
  "Word for word: the answer as it is written.":
    "Tal cual: la respuesta como está escrita.",
  "Word for word": "Tal cual",
  Natural: "Natural",
};

export default es;
