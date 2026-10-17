// The second half of the Spanish dictionary; i18n.es.ts has the first and merges the two.

const more: Record<string, string> = {
  "This computer": "Este equipo",
  "New folder name": "Nombre de la carpeta nueva",
  "This folder holds the system's own files. A project here puts the agent among them.":
    "Esta carpeta guarda los archivos del propio sistema. Un proyecto aquí deja al agente entre ellos.",
  "Choose a folder": "Elige una carpeta",
  "Choose a folder…": "Elegir una carpeta…",
  "Choose another folder…": "Elegir otra carpeta…",
  "Use this folder": "Usar esta carpeta",
  "No folders in here": "No hay carpetas aquí",
  Up: "Subir",
  Home: "Inicio",
  Location: "Ubicación",
  Folder: "Carpeta",
  "Use the workspace": "Usar el workspace",
  "Keep the current one": "Dejar la actual",
  "The whole disk is allowed in Settings → Security, so the project can live in any folder. The folder is used as it is and is never deleted with the project.":
    "El disco completo está permitido en Ajustes → Seguridad, así que el proyecto puede estar en cualquier carpeta. Se usa tal cual y nunca se borra junto con el proyecto.",
  "Nothing is moved: the project points at that folder. Not possible while it has sessions or other repositories.":
    "No se mueve nada: el proyecto apunta a esa carpeta. No se puede mientras tenga sesiones u otros repositorios.",
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
  "Microphone permission": "Permiso de micrófono",
  "Allow microphone": "Permitir micrófono",
  "Allow the microphone to use voice. The browser asks once, and you can take it back in this site's settings.":
    "Permite el micrófono para usar la voz. El navegador lo pide una vez y puedes quitarlo en los ajustes de este sitio.",
  "No microphone was found on this device.":
    "No se encontró ningún micrófono en este dispositivo.",
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
  "The first one is the main repository, the project's folder. The others are shared from your workspace, and every session gets its own copy of each.":
    "El primero es el repositorio principal, la carpeta del proyecto. Los demás se comparten desde tu espacio de trabajo, y cada sesión recibe su propia copia de cada uno.",
  "Adding or removing a repository happens right away. Open sessions get a copy of a new one; removing one takes it out of this project, and the other projects that use it keep it.":
    "Agregar o quitar un repositorio ocurre al instante. Las sesiones abiertas reciben una copia del nuevo; quitar uno lo saca de este proyecto, y los demás proyectos que lo usan lo conservan.",
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
  Basic: "Básico",
  Capable: "Capaz",
  "Very capable": "Muy capaz",
  "Free (local)": "Gratis (local)",
  "Try it": "Probar",
  "Asks it for a title, through the gateway, the way the agent would.":
    "Le pide un título, a través del gateway, como lo haría el agente.",
  "“{text}” in {seconds} s": "«{text}» en {seconds} s",
  "Trying it…": "Probando…",
  "Running on the graphics card: {card}":
    "Ejecutándose en la tarjeta gráfica: {card}",
  unnamed: "sin nombre",
  "This browser runs WebGPU on the processor, not on a graphics card: the model works but is very slow.":
    "Este navegador ejecuta WebGPU en el procesador, no en una tarjeta gráfica: el modelo funciona pero es muy lento.",
  "Trying it… {seconds} s": "Probando… {seconds} s",
  "Still working: a card that emulates WebGPU on the processor can take minutes.":
    "Sigue trabajando: una tarjeta que emula WebGPU con el procesador puede tardar minutos.",
  "It ran in {seconds} s but answered nothing.":
    "Corrió en {seconds} s pero no respondió nada.",
  "Model in this browser": "Modelo en este navegador",
  "Run a small model in this tab (WebGPU)":
    "Ejecutar un modelo pequeño en esta pestaña (WebGPU)",
  "Qwen2.5 0.5B, free. Downloads about {size} once. Then choose it under Summaries below: titles and summaries use it while this tab is open.":
    "Qwen2.5 0.5B, gratis. Descarga unos {size} una sola vez. Luego elígelo en Resúmenes, más abajo: los títulos y resúmenes lo usan mientras esta pestaña esté abierta.",
  "The gateway is downloading the model: {pct}%":
    "El gateway está descargando el modelo: {pct}%",
  "Loading the model into the graphics card: {pct}%":
    "Cargando el modelo en la tarjeta gráfica: {pct}%",
  "Ready: it answers while this tab stays open.":
    "Listo: responde mientras esta pestaña siga abierta.",
  "this gateway does not offer the browser model":
    "este gateway no ofrece el modelo del navegador",
  "The switch stays on: this starts the model once more.":
    "El interruptor sigue activado: esto inicia el modelo otra vez.",
  "The model could not start: {error}": "El modelo no pudo iniciar: {error}",
  "This browser or device cannot run it: it needs WebGPU, and a phone is left out.":
    "Este navegador o dispositivo no puede ejecutarlo: necesita WebGPU, y un celular queda fuera.",
  "Estimated from the model's name": "Estimado a partir del nombre del modelo",
  "Prices checked on {date}.": "Precios revisados el {date}.",
  Cheap: "Económico",
  Moderate: "Intermedio",
  Expensive: "Caro",
  "About {price} per million tokens, read / written":
    "Unos {price} por millón de tokens, leídos / escritos",
  "Each model shows how capable it is and what it costs. Use a cheap one for easy pieces and a very capable one for hard pieces. Prices are approximate.":
    "Cada modelo indica qué tan capaz es y cuánto cuesta. Usa uno económico para las piezas fáciles y uno muy capaz para las difíciles. Los precios son aproximados.",
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
  "Easy pieces (cheap)": "Piezas fáciles (barato)",
  "A small or local model for easy pieces handed to background agents. Leave empty to send everything to one model.":
    "Un modelo pequeño o local para las piezas fáciles que van a los agentes en segundo plano. Déjalo vacío para mandar todo a un solo modelo.",
  "Hard pieces (strong)": "Piezas difíciles (fuerte)",
  "The model for hard pieces, and where a cheap agent that fails tries again. Empty means the coding model.":
    "El modelo para las piezas difíciles, y donde reintenta un agente barato que falla. Vacío usa el modelo de código.",
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
  "free on your own machine or network": "gratis en tu propia máquina o red",
  "Ollama's hosted models, billed per token":
    "modelos alojados de Ollama, de pago por token",
  "API key from console.anthropic.com": "Clave de API de console.anthropic.com",
  "your Claude plan, via the Claude Code CLI":
    "tu plan de Claude, mediante la CLI de Claude Code",
  "sign in with Google, or an AI Studio key":
    "inicia sesión con Google, o una clave de AI Studio",
  "a DashScope (Alibaba Cloud Model Studio) key":
    "una clave de DashScope (Alibaba Cloud Model Studio)",
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
  "Google sign-in is not set up on this computer. Paste an AI Studio key instead.":
    "El inicio de sesión con Google no está configurado en este equipo. Pega una clave de AI Studio.",
  "The Google login on this computer cannot be used. Paste an AI Studio key instead.":
    "No se puede usar el inicio de sesión de Google de este equipo. Pega una clave de AI Studio.",
  "The Google login has no Google Cloud project. Paste an AI Studio key instead.":
    "El inicio de sesión de Google no tiene un proyecto de Google Cloud. Pega una clave de AI Studio.",
  "The Google login on this computer has expired. Paste an AI Studio key instead.":
    "El inicio de sesión de Google de este equipo caducó. Pega una clave de AI Studio.",
  "The sign-in did not finish: {err}": "El inicio de sesión no terminó: {err}",
  Network: "Red",
  Security: "Seguridad",
  "Disk access": "Acceso al disco",
  "What Motita can reach": "A qué puede acceder Motita",
  "Workspace only": "Solo su workspace",
  "Whole disk": "Disco completo",
  "Safest: Motita reads and writes only inside the folder of the session it works in. Your other files are out of its reach.":
    "Lo más seguro: Motita lee y escribe solo dentro de la carpeta de la sesión en la que trabaja. Tus demás archivos quedan fuera de su alcance.",
  "Motita can read and write any file your user account can: documents, other projects, settings.":
    "Motita puede leer y escribir cualquier archivo al que llegue tu usuario: documentos, otros proyectos, configuraciones.",
  "Motita will be able to read, change and delete any file your user account can, outside its workspace too.":
    "Motita podrá leer, cambiar y borrar cualquier archivo al que llegue tu usuario, también fuera de su workspace.",
  "A mistake, or instructions hidden in a web page or a repository it reads, could damage or leak your files.":
    "Un error, o instrucciones escondidas en una página web o en un repositorio que lea, podría dañar o filtrar tus archivos.",
  "It applies at once to every open session, from its next command.":
    "Se aplica de inmediato a todas las sesiones abiertas, desde su próximo comando.",
  "Give Motita access to the whole disk?":
    "¿Darle a Motita acceso al disco completo?",
  "Allow the whole disk": "Permitir el disco completo",
  "On this system only Motita's own file edits are limited: its commands are not, because that needs Linux with Landlock and write confinement on.":
    "En este sistema solo se limitan las ediciones de archivos que hace Motita: sus comandos no, porque eso requiere Linux con Landlock y el confinamiento de escritura activado.",
  "A command you approve one by one can write where you approved it, and sudo runs unconfined.":
    "Un comando que apruebes uno por uno puede escribir donde lo aprobaste, y sudo corre sin confinamiento.",
  "Motita's own logins and keys stay hidden from its commands either way, approved ones included.":
    "Las sesiones y claves de Motita siguen ocultas para sus comandos en ambos casos, también para los aprobados.",
  "Running the project's code": "Ejecutar el código del proyecto",
  "Ask first": "Preguntar antes",
  "Run without asking": "Ejecutar sin preguntar",
  "Where nothing confines its commands (Windows, macOS, Linux without Landlock), Motita asks before it runs the project's own code: a script, a build, a test.":
    "Donde nada confina sus comandos (Windows, macOS, Linux sin Landlock), Motita pregunta antes de ejecutar el código del propio proyecto: un script, una compilación, un test.",
  "Motita runs the project's own scripts, builds and tests without asking, even where nothing confines them. Reads outside the workspace are still asked about.":
    "Motita ejecuta los scripts, compilaciones y tests del propio proyecto sin preguntar, aunque nada los confine. Las lecturas fuera del workspace se siguen preguntando.",
  "This system confines Motita's commands, so this changes nothing here.":
    "Este sistema confina los comandos de Motita, así que aquí esto no cambia nada.",
  "Run the project's code without asking?":
    "¿Ejecutar el código del proyecto sin preguntar?",
  "On a system that does not confine it, that code can read your keys and Motita's own logins.":
    "En un sistema que no lo confina, ese código puede leer tus claves y las sesiones de Motita.",
  "A script written from instructions hidden in a web page or a repository would run before you see it.":
    "Un script escrito a partir de instrucciones escondidas en una página web o en un repositorio se ejecutaría antes de que lo veas.",
  "This session works in": "Esta sesión trabaja en",
  "Its project is": "Su proyecto es",
  "Its commands' HOME is": "El HOME de sus comandos es",
  "Kept from its commands:": "Fuera del alcance de sus comandos:",
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
  "Alt+L or 🔊 on an answer reads it or stops.":
    "Alt+L o 🔊 en una respuesta la lee o la detiene.",
  "Who speaks": "Quién habla",
  "System voices": "Voces del sistema",
  "Voice service": "Servicio de voz",
  "Downloaded voice": "Voz descargada",
  "The voices this device or browser already has. Nothing leaves the browser.":
    "Las voces que ya trae este dispositivo o navegador. Nada sale del navegador.",
  "The speech service set up on the gateway: the same voice on every device. The text of each answer read goes to that service.":
    "El servicio de voz configurado en el gateway: la misma voz en todos los dispositivos. El texto de cada respuesta leída va a ese servicio.",
  "A natural voice the gateway downloads once; it then runs inside this browser, and nothing leaves the device.":
    "Una voz natural que el gateway descarga una vez; después corre dentro de este navegador y nada sale del dispositivo.",
  "Gateway default": "La del gateway",
  "Voices to download": "Voces para descargar",
  "The first download adds {size} for the voice engine.":
    "La primera descarga suma {size} del motor de voz.",
  "Downloading… {pct}%": "Descargando… {pct} %",
  "This browser keeps the downloaded models, also when you update Motita.":
    "Este navegador conserva los modelos descargados, también cuando actualizas Motita.",
  "This browser may delete the downloaded models if the disk runs short; they download again. Installing Motita as an app helps it keep them.":
    "Este navegador puede borrar los modelos descargados si se queda sin disco; se descargan de nuevo. Instalar Motita como app ayuda a que los conserve.",
  "The download failed: {error}": "La descarga falló: {error}",
  "In use": "En uso",
  "Voice texture": "Textura de la voz",
  "How smooth the voice sounds: lower is cleaner and steadier, higher is rougher.":
    "Qué tan suave suena la voz: menos es más limpia y estable, más es más áspera.",
  "Rhythm variation": "Variación del ritmo",
  "How much the length of each sound varies: lower is more even, higher more natural.":
    "Cuánto varía la duración de cada sonido: menos es más parejo, más es más natural.",
  Pitch: "Tono",
  Reset: "Restablecer",
  "Loading into memory… {pct}%": "Cargando en memoria… {pct} %",
  "The voice could not be loaded: {error}": "No se pudo cargar la voz: {error}",
  "High quality": "Calidad alta",
  "Medium quality": "Calidad media",
  "Low quality": "Calidad baja",
  "Very low quality": "Calidad muy baja",
  "Every language": "Todos los idiomas",
  "No voice matches these filters.": "Ninguna voz coincide con estos filtros.",
  "Stop the sample": "Detener la muestra",
  "Hear {name}": "Escuchar a {name}",
  "The sample could not be played: {error}":
    "No se pudo reproducir la muestra: {error}",
  Use: "Usar",
  Download: "Descargar",
  "Motita needs your answer.": "Motita necesita tu respuesta.",
  "Conversation ended": "Conversación terminada",
  Conversation: "Conversación",
  "After reading the answer to a turn you spoke, Motita listens again and sends what you say. Say “that's all” or tap the microphone to end it.":
    "Después de leer la respuesta a algo que dijiste, Motita vuelve a escuchar y envía lo que digas. Di “basta” o toca el micrófono para terminar.",
  "Needs speech input: turn it on in Voice input.":
    "Necesita la entrada de voz: actívala en Entrada de voz.",
  "Needs answers read aloud: choose when above.":
    "Necesita leer las respuestas en voz alta: elige cuándo más arriba.",
  "Needs answers read aloud or voice notices on.":
    "Necesita leer las respuestas en voz alta o los avisos por voz.",
  "Needs subtitles on.": "Necesita los subtítulos activados.",
  "Needs a voice per language on.": "Necesita activar una voz por idioma.",
  "Voice notices": "Avisos por voz",
  "Says the CI of a pull request and the questions the agent waits on, while this tab is visible.":
    "Dice el CI de un pull request y las preguntas que espera el agente, mientras esta pestaña está visible.",
  "A voice per language": "Una voz por idioma",
  "Each sentence is read with a voice of its own language: English with an English voice.":
    "Cada oración se lee con una voz de su idioma: el inglés con una voz en inglés.",
  "Voice for English": "Voz para inglés",
  "Voice for Spanish": "Voz para español",
  "This device has no voice for that language.":
    "Este dispositivo no tiene voces en ese idioma.",
  Pronunciations: "Pronunciaciones",
  "One a line: the term, “=”, and how to say it. For example: kubectl = cube control":
    "Una por línea: el término, “=” y cómo decirlo. Por ejemplo: kubectl = cube control",
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
  Subtitles: "Subtítulos",
  "Shows on screen what Motita says aloud, sentence by sentence, like a film's subtitles.":
    "Muestra en pantalla lo que Motita dice en voz alta, frase por frase, como los subtítulos de una película.",
  "Subtitle size": "Tamaño de los subtítulos",
  "Motita needs an answer to continue":
    "Motita necesita una respuesta para seguir",
  "Motita needs {n} answers to continue":
    "Motita necesita {n} respuestas para seguir",
  "{done} of {total} answered": "{done} de {total} respondidas",
  "Answers sent": "Respuestas enviadas",
  "Could not send the answers": "No se pudieron enviar las respuestas",
  "Send answers": "Enviar respuestas",
  "A question left blank goes with what Motita suggests.":
    "Si dejas una pregunta en blanco, Motita sigue con lo que propone.",
  "Or write your own answer": "O escribe tu propia respuesta",
  "Write your answer": "Escribe tu respuesta",
  "If you leave it blank: {assumption}": "Si la dejas en blanco: {assumption}",
  "Question {n} of {total}": "Pregunta {n} de {total}",
  Answered: "Respondida",
  "Message menu": "Opciones del mensaje",
  "sends this message again": "vuelve a enviar este mensaje",
  "The mode auto mode picked": "El modo que eligió el modo automático",
  "Download a model to listen with above first.":
    "Primero descarga un modelo para escuchar, más arriba.",
  "Who listens": "Quién escucha",
  "A model downloaded once, run in this browser.":
    "Un modelo que se descarga una vez y corre en este navegador.",
  "A transcription service, through the gateway.":
    "Un servicio de transcripción, a través del gateway.",
  "This browser": "Este navegador",
  "Only while this tab is open. What you say is heard in this browser. Say: {phrases}":
    "Solo mientras esta pestaña esté abierta. Lo que dices se escucha en este navegador. Di: {phrases}",
  "The model could not be loaded: {error}":
    "No se pudo cargar el modelo: {error}",
  "Model to listen with": "Modelo para escuchar",
  "Whisper runs in this browser, on WebGPU where there is one and on WebAssembly otherwise: what you say never leaves this device.":
    "Whisper corre en este navegador, con WebGPU donde lo hay y con WebAssembly si no: lo que dices nunca sale de este dispositivo.",
  "Whisper runs in this browser, on the processor: what you say never leaves this device.":
    "Whisper corre en este navegador, con el procesador: lo que dices nunca sale de este dispositivo.",
  "Use WebGPU on this device": "Usar WebGPU en este dispositivo",
  "On a phone the models that listen and speak run on the processor, as its graphics card is too weak for them. Turn this on to try WebGPU anyway; reload the page to apply it.":
    "En el celular los modelos que escuchan y hablan corren en el procesador, porque su tarjeta gráfica es demasiado débil para ellos. Actívalo para probar WebGPU de todos modos; recarga la página para aplicarlo.",
  "The first download adds {size} for the engine.":
    "La primera descarga suma {size} para el motor.",
  "WebGPU could not start here ({reason}): the model runs on the processor.":
    "WebGPU no pudo iniciar aquí ({reason}): el modelo corre en el procesador.",
  "Language you speak": "Idioma en que hablas",
  "Detect it": "Detectarlo",
  Processor: "Procesador",
  "on the graphics card": "en la tarjeta gráfica",
  Recommended: "Recomendado",
  "Voice input": "Entrada de voz",
  "Voice output": "Salida de voz",
  "Every model ({n})": "Todos los modelos ({n})",
  "Every voice ({n})": "Todas las voces ({n})",
  "The local listener took too long and was stopped.":
    "La transcripción local tardó demasiado y se detuvo.",
  On: "Encendido",
  "Code search": "Búsqueda en el código",
  "Search the workspace": "Buscar en el espacio de trabajo",
  "Motita reads the folder once and answers with passages instead of whole files.":
    "Motita lee la carpeta una vez y responde con pasajes en vez de archivos enteros.",
  "Ranking by meaning": "Ranking por significado",
  "Learned from your own code. No network.":
    "Aprendido de tu propio código. Sin red.",
  "Find a local Ollama": "Buscar un Ollama local",
  "Looks on this machine for nomic-embed-text. The text does not leave it.":
    "Busca nomic-embed-text en este equipo. El texto no sale de él.",
  "Embeddings come from {endpoint} ({model}), on this machine or its network.":
    "Los embeddings vienen de {endpoint} ({model}), en este equipo o en tu red.",
  "Embeddings come from {endpoint} ({model}). The text of your files and every question go there.":
    "Los embeddings vienen de {endpoint} ({model}). El texto de tus archivos y cada pregunta se envían allí.",
  "Embeddings use a local Ollama with nomic-embed-text when there is one (ollama pull nomic-embed-text). The text stays on this machine.":
    "Los embeddings usan un Ollama local con nomic-embed-text si lo hay (ollama pull nomic-embed-text). El texto se queda en este equipo.",
  "No embeddings: passages are ranked by words and by meaning learned from your code.":
    "Sin embeddings: los pasajes se ordenan por palabras y por el significado aprendido de tu código.",
  "The text of the indexed files, in pieces, is sent to {endpoint} once.":
    "El texto de los archivos indexados, por trozos, se envía una vez a {endpoint}.",
  "Every question searched is sent there too.":
    "Cada pregunta que se busca también se envía allí.",
  "Use it only with a provider you would already show your code to.":
    "Úsalo solo con un proveedor al que ya le mostrarías tu código.",
  "Embeddings endpoint": "Endpoint de embeddings",
  "An OpenAI-compatible /embeddings address. Empty uses only the local Ollama, if any.":
    "Una dirección /embeddings compatible con OpenAI. Vacía, solo usa el Ollama local, si lo hay.",
  "Embeddings model": "Modelo de embeddings",
  Limits: "Límites",
  "Files larger than this many KB, or beyond this many files, are not indexed.":
    "No se indexan los archivos de más KB que esto, ni los que pasen de esta cantidad.",
  "Largest file (KB)": "Archivo más grande (KB)",
  "Most files": "Máximo de archivos",
  "Send your code to this endpoint?": "¿Enviar tu código a este endpoint?",
  "Send my code there": "Enviar mi código allí",
  "The embeddings key goes in MOTITA_CODE_INDEX_EMBEDDINGS_API_KEY. Changes apply to new sessions.":
    "La clave de embeddings va en MOTITA_CODE_INDEX_EMBEDDINGS_API_KEY. Los cambios se aplican a las sesiones nuevas.",
  "A key is set.": "Hay una clave definida.",
  "Network for commands": "Red para los comandos",
  "No network": "Sin red",
  "Allow the network": "Permitir la red",
  "Motita's commands run without a network: only this machine's loopback, so tests on 127.0.0.1 still work. A command you approve keeps the network.":
    "Los comandos de Motita se ejecutan sin red: solo el loopback de esta máquina, así que los tests sobre 127.0.0.1 siguen funcionando. Un comando que apruebas conserva la red.",
  "Motita's commands can reach the network. A command that read your files could send them anywhere.":
    "Los comandos de Motita pueden usar la red. Un comando que leyó tus archivos podría enviarlos a cualquier parte.",
  "A command that has read something, your code or a key in a file, could send it out.":
    "Un comando que leyó algo, tu código o una clave en un archivo, podría enviarlo fuera.",
  "Instructions hidden in a web page or a repository could make it do that before you see it.":
    "Instrucciones ocultas en una página web o en un repositorio podrían hacer que lo haga antes de que lo veas.",
  "This system cannot take the network away from a command (it needs Linux), so this changes nothing here.":
    "Este sistema no puede quitarle la red a un comando (necesita Linux), así que aquí no cambia nada.",
  "This system blocks it for programs without permission (Ubuntu 24.04 and later). To allow it, run once on the machine that runs Motita: sudo motita sandbox apparmor install":
    "Este sistema lo bloquea para los programas sin permiso (Ubuntu 24.04 y posteriores). Para permitirlo, ejecuta una vez en la máquina donde corre Motita: sudo motita sandbox apparmor install",
  "This system cannot give a command a network of its own ({reason}), so this changes nothing here.":
    "Este sistema no puede dar a un comando una red propia ({reason}), así que aquí esto no cambia nada.",
  "This browser has no WebGPU for the page's workers.":
    "Este navegador no ofrece WebGPU a los workers de la página.",
  "The browser gave no graphics card to use.":
    "El navegador no dio ninguna tarjeta gráfica para usar.",
  "The local listener stopped.": "El oyente local se detuvo.",
  "Give Motita's commands the network?":
    "¿Dar la red a los comandos de Motita?",
};

export default more;
