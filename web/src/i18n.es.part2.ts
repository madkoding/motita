// The second half of the Spanish dictionary; i18n.es.ts has the first and merges the two.

const more: Record<string, string> = {
  "This computer": "Este equipo",
  "Git and pull requests": "Git y pull requests",
  "A new folder in the workspace": "Una carpeta nueva en el workspace",
  "Choose or create…": "Elegir o crear…",
  "Change…": "Cambiar…",
  "Its folder is created in the workspace with this name.":
    "Su carpeta se crea en el workspace con este nombre.",
  "Start from": "Empezar desde",
  "Use a folder": "Usar una carpeta",
  "Clone a repository": "Clonar un repositorio",
  "Repository address": "Dirección del repositorio",
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
  "The folder does not exist yet: it will be created.":
    "La carpeta aún no existe: se va a crear.",
  "The folder already exists and is used as it is. Deleting the project asks whether it stays on disk.":
    "La carpeta ya existe y se usa tal cual. Al eliminar el proyecto se pregunta si se queda en el disco.",
  "This folder is among the system's own files. I know what I am doing and want a project here.":
    "Esta carpeta está entre los archivos del sistema. Sé lo que hago y quiero un proyecto aquí.",
  "What happens to the project's folder?":
    "¿Qué pasa con la carpeta del proyecto?",
  "Keep the folder and its files": "Dejar la carpeta y sus archivos",
  "Delete the folder and everything in it":
    "Eliminar la carpeta y todo su contenido",
  "This folder cannot be deleted from here:":
    "Esta carpeta no se puede eliminar desde aquí:",
  "I understand the folder and all its files will be deleted for good.":
    "Entiendo que la carpeta y todos sus archivos se eliminarán para siempre.",
  "Delete project and folder": "Eliminar proyecto y carpeta",
  "Without the whole disk in Settings → Security, the folder has to be inside the workspace.":
    "Sin el disco completo en Ajustes → Seguridad, la carpeta tiene que estar dentro del workspace.",
  "Keep the current one": "Dejar la actual",
  "The whole disk is allowed in Settings → Security, so the project can live in any folder. The folder is used as it is; deleting the project asks whether it stays.":
    "El disco completo está permitido en Ajustes → Seguridad, así que el proyecto puede estar en cualquier carpeta. Se usa tal cual; al eliminar el proyecto se pregunta si se queda.",
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
  Add: "Agregar",
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
  "Thinking for {kind}": "Razonamiento para {kind}",
  "Thinking: same as session": "Razonamiento: igual que la sesión",
  Thinking: "Razonamiento",
  Basic: "Básico",
  Capable: "Capaz",
  "Very capable": "Muy capaz",
  "Free (local)": "Gratis (local)",
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
  "Titles, conversation summaries, auto mode's guess, the final check and the answer's summary.":
    "Títulos, resúmenes de la conversación, la elección del modo auto, la revisión final y el resumen de la respuesta.",
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
  "Motita can read and write any file your user account can: documents, other projects, settings. Commands that work outside the workspace no longer ask; one that touches your keys still does.":
    "Motita puede leer y escribir cualquier archivo al que llegue tu usuario: documentos, otros proyectos, configuraciones. Los comandos que trabajan fuera del workspace ya no preguntan; uno que toca tus llaves sí.",
  "Motita will be able to read, change and delete any file your user account can, outside its workspace too.":
    "Motita podrá leer, cambiar y borrar cualquier archivo al que llegue tu usuario, también fuera de su workspace.",
  "A mistake, or instructions hidden in a web page or a repository it reads, could damage or leak your files.":
    "Un error, o instrucciones escondidas en una página web o en un repositorio que lea, podría dañar o filtrar tus archivos.",
  "It applies at once to every open session, from its next command.":
    "Se aplica de inmediato a todas las sesiones abiertas, desde su próximo comando.",
  "Give Motita access to the whole disk?":
    "¿Darle a Motita acceso al disco completo?",
  "Allow the whole disk": "Permitir el disco completo",
  "Motita's commands run inside the system's own sandbox (Landlock), not in a container.":
    "Los comandos de Motita corren dentro del sandbox del propio sistema (Landlock), no en un contenedor.",
  "On this system only Motita's own file edits are limited: its commands are not, because that needs Linux with Landlock and write confinement on.":
    "En este sistema solo se limitan las ediciones de archivos que hace Motita: sus comandos no, porque eso requiere Linux con Landlock y el confinamiento de escritura activado.",
  "A command you approve one by one can write where you approved it, and sudo runs unconfined.":
    "Un comando que apruebes uno por uno puede escribir donde lo aprobaste, y sudo corre sin confinamiento.",
  "Motita's own logins and keys stay hidden from its commands either way, approved ones included.":
    "Las sesiones y claves de Motita siguen ocultas para sus comandos en ambos casos, también para los aprobados.",
  "Running the project's code": "Ejecutar el código del proyecto",
  "Ask first": "Preguntar antes",
  "Run without asking": "Ejecutar sin preguntar",
  "Suggestions after a task": "Sugerencias tras una tarea",
  "Ask me": "Preguntarme",
  "Apply all": "Aplicar todas",
  "After a task, Motita asks which of the changes it suggests to apply: this one, this one and the following, or none.":
    "Tras una tarea, Motita pregunta cuáles de los cambios que sugiere aplicar: esta, esta y las siguientes, o ninguna.",
  "After a task, Motita applies every change it suggests without asking. It still follows the sandbox and the policy, and each applied change shows in the conversation.":
    "Tras una tarea, Motita aplica todos los cambios que sugiere sin preguntar. Sigue respetando el sandbox y la política, y cada cambio aplicado se ve en la conversación.",
  "Changes you never reviewed are written to your files right after each task.":
    "Cambios que no revisaste se escriben en tus archivos justo después de cada tarea.",
  "A suggestion shaped by instructions hidden in a web page or a repository would be applied before you see it.":
    "Una sugerencia influida por instrucciones escondidas en una página web o en un repositorio se aplicaría antes de que la veas.",
  "It applies at once to every open session, from its next report.":
    "Se aplica de inmediato a todas las sesiones abiertas, desde su próximo informe.",
  "Apply every suggestion without asking?":
    "¿Aplicar todas las sugerencias sin preguntar?",
  "Apply without asking": "Aplicar sin preguntar",
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
  Volume: "Volumen",
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
  "Send after silence": "Enviar tras el silencio",
  "After the wake phrase, what you say joins the message and it is sent once you stop talking for this long.":
    "Tras la frase de activación, lo que digas se suma al mensaje y se envía cuando dejas de hablar este tiempo.",
  "Never: I send it myself": "Nunca: lo envío yo",
  "{n} seconds": "{n} segundos",
  "Not loaded yet.": "Todavía no está cargado.",
  "Running on WebGPU.": "Corre en WebGPU.",
  "Running on the processor: WebGPU could not start.":
    "Corre en el procesador: WebGPU no pudo iniciar.",
  "Running on the processor (WebAssembly, one thread).":
    "Corre en el procesador (WebAssembly, un hilo).",
  "No recording yet: speak once to measure it.":
    "Aún no hay grabación: habla una vez para medirlo.",
  "Last: {model} wrote {seconds} s of speech in {took} s ({times}× real time).":
    "Última: {model} transcribió {seconds} s de voz en {took} s ({times}× el tiempo real).",
  "This browser has WebGPU: the “GPU” models are much faster here.":
    "Este navegador tiene WebGPU: los modelos «GPU» son mucho más rápidos aquí.",
  "Too slow on the processor: the Tiny model answers several times sooner.":
    "Va lento en el procesador: el modelo Tiny responde varias veces más rápido.",
  "Voice input": "Entrada de voz",
  "Voice output": "Salida de voz",
  "Every model ({n})": "Todos los modelos ({n})",
  "Every voice ({n})": "Todas las voces ({n})",
  "The local listener took too long and was stopped.":
    "La transcripción local tardó demasiado y se detuvo.",
  On: "Encendido",
  Indexing: "Indexado",
  "Search the workspace": "Buscar en el espacio de trabajo",
  "Motita reads the folder once and answers with passages instead of whole files.":
    "Motita lee la carpeta una vez y responde con pasajes en vez de archivos enteros.",
  "Ranking by meaning": "Ranking por significado",
  "Learned from your own code. No network.":
    "Aprendido de tu propio código. Sin red.",
  "Find a local Ollama": "Buscar un Ollama local",
  "Index cache": "Caché del índice",
  "{n} files, {size} on disk. The index is rebuilt when it is gone.":
    "{n} archivos, {size} en disco. El índice se reconstruye si falta.",
  "Code index": "Índice de código",
  "Up to date": "Al día",
  "Out of date": "Desactualizado",
  "Not indexed": "Sin indexar",
  "Indexed share of the project": "Parte del proyecto indexada",
  files: "archivos",
  passages: "pasajes",
  terms: "términos",
  "on disk": "en disco",
  "{files} files indexed; {skipped} binary or unreadable skipped.":
    "{files} archivos indexados; {skipped} binarios o ilegibles omitidos.",
  "{files} of {total} files covered.": "{files} de {total} archivos cubiertos.",
  "The limit of {max} files is reached: raise it in Settings.":
    "Se alcanzó el límite de {max} archivos: súbelo en Ajustes.",
  "Ranked also by embeddings.": "Ordena también con embeddings.",
  "Ranked also by meaning.": "Ordena también por significado.",
  "Index now": "Indexar ahora",
  "Provider usage": "Uso de proveedores",
  Week: "Semana",
  Month: "Mes",
  Session: "Sesión",
  "{n} h": "{n} h",
  "resets in {t}": "se reinicia en {t}",
  "No quota reported": "Sin dato de cuota",
  "Below the warning level": "Bajo el nivel de aviso",
  "Near the limit": "Cerca del límite",
  OK: "OK",
  "{d} d {h} h": "{d} d {h} h",
  "{d} d": "{d} d",
  "{h} h {m} min": "{h} h {m} min",
  "{m} min": "{m} min",
  "1 call": "1 llamada",
  "{n} calls": "{n} llamadas",
  "{n} tokens": "{n} tokens",
  Bars: "Barras",
  Gauge: "Indicador circular",
  "+{n} more": "+{n} más",
  "{left} of {max} tokens left in the rate window":
    "quedan {left} de {max} tokens en la ventana de límite",
  "Indexing…": "Indexando…",
  "Already up to date.": "Ya estaba al día.",
  "Reading the project's files…": "Leyendo los archivos del proyecto…",
  "Index updated: {files} files, {chunks} passages.":
    "Índice actualizado: {files} archivos, {chunks} pasajes.",
  "Clear index cache": "Borrar caché del índice",
  "{n} index files deleted ({kb} KB). The next search builds it again.":
    "{n} archivos de índice borrados ({kb} KB). La próxima búsqueda lo reconstruye.",
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
  'Create the project "{name}" from this repository and move this session into it?':
    '¿Crear el proyecto "{name}" desde este repositorio y mover esta sesión a él?',
  "Create project": "Crear proyecto",
  "It is cloned, indexed and set up with its defaults; this conversation goes on inside it.":
    "Se clona, se indexa y se configura con sus valores por defecto; esta conversación sigue dentro.",
  "Not now": "Ahora no",
  'The project "{title}" was created, but the session was not moved: {err}':
    'El proyecto "{title}" se creó, pero la sesión no se movió: {err}',
  'Project "{title}" created and this session moved into it':
    'Proyecto "{title}" creado y esta sesión movida a él',
  "Could not set up the project": "No se pudo configurar el proyecto",
  'Move this session into a new project "{title}"?':
    '¿Mover esta sesión a un proyecto nuevo "{title}"?',
  "The files it made stay in its previous folder; the conversation goes on in the new project.":
    "Los archivos que creó se quedan en su carpeta anterior; la conversación sigue en el proyecto nuevo.",
  "The project is not indexed: the code index is off":
    "El proyecto no se indexó: el índice de código está desactivado",
  "Turn it on in Settings → Indexing.": "Actívalo en Ajustes → Indexado.",
  "Test containers": "Contenedores de prueba",
  "Services Motita starts in a container only it can reach, to test a project and take screenshots. They are not where its commands run (that is the sandbox above). A run removes its own when it ends; stop here any that stay.":
    "Servicios que Motita levanta en un contenedor al que solo ella llega, para probar un proyecto y sacar capturas. No es donde corren sus comandos (eso es el sandbox de arriba). Cada ejecución quita los suyos al terminar; detén aquí los que queden.",
  Refresh: "Actualizar",
  "Stop all": "Detener todas",
  Stop: "Detener",
  "Stop {name}": "Detener {name}",
  "No boxes are running.": "No hay cajas en marcha.",
  "this computer": "este equipo",
  "Computer name": "Nombre del equipo",
  Online: "Conectado",
  Offline: "Sin conexión",
  "Disconnect {name}": "Desconectar {name}",
  "Rename {name}": "Renombrar {name}",
  "Other computers": "Otros equipos",
  "Add a computer": "Agregar un equipo",
  "Other computers cannot connect to this one: it listens on this computer only. Settings → Network opens it to the local network.":
    "Otros equipos no pueden conectarse a este: solo escucha en este equipo. Ajustes → Red lo abre a la red local.",
  "Connect {name}": "Conectar {name}",
  Address: "Dirección",
  "{count} running": "{count} en curso",
  "Waiting for the other computer to let this one in. Its screen shows the same code; on one without a screen, run `motita gateway allow {code}` there.":
    "Esperando que el otro equipo deje entrar a este. Su pantalla muestra el mismo código; si no tiene pantalla, ejecuta ahí `motita gateway allow {code}`.",
  Refused: "Rechazado",
  "{name} wants to open this computer": "{name} quiere abrir este equipo",
  "From {address}. Allow it only if that computer shows this code:":
    "Desde {address}. Permítelo solo si ese equipo muestra este código:",
  Allow: "Permitir",
  Deny: "Rechazar",
  "Revoke {name}": "Quitar el acceso a {name}",
  "Revoke access": "Quitar el acceso",
  "Can open this computer": "Pueden abrir este equipo",
  "Leave the token empty to ask that computer for access: its screen shows a code, and the person there lets this one in (on one without a screen, `motita gateway allow <code>` in its terminal). Or type its gateway token (`motita gateway start -token-only` shows it). Only computers on the local network, a VPN or the tailnet can be connected.":
    "Deja el token vacío para pedirle acceso a ese equipo: su pantalla muestra un código y quien esté ahí deja entrar a este (si no tiene pantalla, con `motita gateway allow <código>` en su terminal). O escribe el token de su gateway (`motita gateway start -token-only` lo muestra). Solo se pueden conectar equipos de la red local, una VPN o la tailnet.",
  "Token (optional)": "Token (opcional)",
  "Ask for access": "Pedir acceso",
  "Control of this computer's desktop": "Control del escritorio de este equipo",
  "Allowed, asking each step": "Permitido, preguntando cada paso",
  "Motita works with files and commands only: it never sees your screen nor uses your mouse or keyboard.":
    "Motita trabaja solo con archivos y comandos: nunca ve tu pantalla ni usa tu mouse o teclado.",
  "Motita can look at this computer's screen and use its mouse, keyboard and windows. It asks before every step, a picture of the screen included.":
    "Motita puede mirar la pantalla de este equipo y usar su mouse, teclado y ventanas. Pregunta antes de cada paso, incluida cada captura de pantalla.",
  "A picture of the screen shows the model whatever you have open: mail, chats, passwords on display.":
    "Una captura de pantalla le muestra al modelo todo lo que tengas abierto: correo, chats, contraseñas a la vista.",
  "Clicks and keys act as you, outside the sandbox, in every program of your session.":
    "Los clics y las teclas actúan como tú, fuera del sandbox, en todos los programas de tu sesión.",
  "Allowing every command for a session also allows every step on the desktop.":
    "Permitir todos los comandos en una sesión también permite todos los pasos en el escritorio.",
  "Let Motita control this computer's desktop?":
    "¿Dejar que Motita controle el escritorio de este equipo?",
  "Allow desktop control": "Permitir el control del escritorio",
  Notes: "Notas",
  "New note name": "Nombre de la nota nueva",
  "No folder": "Sin carpeta",
  "Search the notes": "Buscar en las notas",
  "No notes yet. Write the first one above.":
    "Todavía no hay notas. Escribe la primera arriba.",
  "Nothing matches that.": "Nada coincide con eso.",
  "Mentioned in ({n})": "Mencionada en ({n})",
  "No other note mentions this one yet.":
    "Ninguna otra nota menciona esta todavía.",
  "Note text": "Texto de la nota",
  Edit: "Editar",
  "Click again to delete": "Pulsa otra vez para borrar",
  "Pick a note, or write a new one.": "Elige una nota o escribe una nueva.",
  "Each project has a manual: open its folder and start with Start here. Link notes with [[double brackets]].":
    "Cada proyecto tiene un manual: abre su carpeta y empieza por Start here. Enlaza notas con [[dobles corchetes]].",
  "They are plain .md files: open this folder in Obsidian and they are all there.":
    "Son archivos .md normales: abre esta carpeta en Obsidian y estarán todas.",
  "Saved in {root}": "Guardadas en {root}",
  "Your second brain: projects, sessions and your own ideas.":
    "Tu segundo cerebro: proyectos, sesiones y tus propias ideas.",
  Map: "Mapa",
  "Close map": "Cerrar mapa",
  "Find a note on the map": "Buscar una nota en el mapa",
  "{n} found": "{n} encontradas",
  "1 found": "1 encontrada",
  "Colours of the map": "Colores del mapa",
  "Loose notes": "Notas sueltas",
  "Close the note": "Cerrar la nota",
  "Open the note": "Abrir la nota",
  "Drag to turn, scroll to zoom, click a name to look at it.":
    "Arrastra para girar, usa la rueda para acercar y haz clic en un nombre para verlo.",
  "could not load the notes": "no se pudieron cargar las notas",
  "could not open the note": "no se pudo abrir la nota",
  "could not save the note": "no se pudo guardar la nota",
  "could not create the note": "no se pudo crear la nota",
  "could not delete the note": "no se pudo borrar la nota",
  "could not rename the note": "no se pudo renombrar la nota",
  "New name": "Nombre nuevo",
  "Second brain": "Segundo cerebro",
  "Scan sessions and projects": "Escanear sesiones y proyectos",
  "Writes a note for every project and every session you have used, linked to each other, and a manual of each project with a note per folder: its files, its API, what it uses and what uses it. Run it again to refresh them; what you add around the generated block stays.":
    "Escribe una nota por cada proyecto y cada sesión que has usado, enlazadas entre sí, y un manual de cada proyecto con una nota por carpeta: sus archivos, su API, qué usa y quién la usa. Vuelve a ejecutarlo para actualizarlas; lo que agregues alrededor del bloque generado se conserva.",
  "Scan now": "Escanear ahora",
  "Scanning…": "Escaneando…",
  "Writing the notes of your sessions…":
    "Escribiendo las notas de tus sesiones…",
  "Mapping {project} ({n} of {total})": "Mapeando {project} ({n} de {total})",
  "folder {n} of {total}": "carpeta {n} de {total}",
  "Use the model to summarise sessions and explain projects":
    "Usar el modelo para resumir sesiones y explicar proyectos",
  "Adds a short summary to sessions of six turns or more (at most twenty per scan), explains how each project fits together (at most ten per scan) and documents each folder of the projects' manuals (as many per scan as you choose below). Each is only asked again when the session, project or folder changed. The text of those sessions and the structure, README and source excerpts of those projects are sent to your model provider and use tokens.":
    "Agrega un resumen corto a las sesiones de seis turnos o más (hasta veinte por escaneo), explica cómo encaja cada proyecto (hasta diez por escaneo) y documenta cada carpeta de los manuales de los proyectos (tantas por escaneo como elijas abajo). Cada uno se pide de nuevo solo cuando la sesión, el proyecto o la carpeta cambió. El texto de esas sesiones y la estructura, el README y extractos del código de esos proyectos se envían a tu proveedor de modelos y gastan tokens.",
  "{n} long sessions summarised.": "{n} sesiones largas resumidas.",
  "{mapped} projects mapped into {documented} manual notes, {explained} explained by the model.":
    "{mapped} proyectos mapeados en {documented} notas de manual, {explained} explicados por el modelo.",
  "notes created from your sessions and projects":
    "notas creadas a partir de tus sesiones y proyectos",
  "{projects} projects and {sessions} sessions scanned: {created} notes created, {updated} updated, {unchanged} unchanged, {skipped} skipped.":
    "{projects} proyectos y {sessions} sesiones escaneados: {created} notas creadas, {updated} actualizadas, {unchanged} sin cambios, {skipped} omitidas.",
  "give the note a name": "ponle un nombre a la nota",
  "a note with that name already exists": "ya existe una nota con ese nombre",
  "could not load the graph": "no se pudo cargar el grafo",
  "Review notes": "Revisar notas",
  "Folders the model explains per scan":
    "Carpetas que el modelo explica por escaneo",
  All: "Todas",
  "Each folder is one model call, so more folders take longer and use more tokens. A folder already explained is only asked again when it changed.":
    "Cada carpeta es una llamada al modelo: más carpetas tardan más y gastan más tokens. Una carpeta ya explicada solo se pide de nuevo cuando cambió.",
  "Review the notes": "Revisar las notas",
  "Opens a conversation where the agent checks the notes for contradictions, stale notes, missing sources and broken links. It changes nothing until you agree.":
    "Abre una conversación donde el agente revisa las notas en busca de contradicciones, notas desactualizadas, fuentes faltantes y enlaces rotos. No cambia nada hasta que aceptes.",
  "Ask the agent to find contradictions, stale notes and broken links":
    "Pide al agente que busque contradicciones, notas desactualizadas y enlaces rotos",
  "Nothing to draw yet.": "Todavía no hay nada que dibujar.",
  "Graph of linked notes": "Grafo de notas enlazadas",
  "{notes} notes · {links} links · {missing} to notes not written yet":
    "{notes} notas · {links} enlaces · {missing} a notas aún sin escribir",
  "Rescan the notes": "Reescanear las notas",
  "Writes the notes and the manual of this project again, in the background; the sidebar shows how far it got.":
    "Vuelve a escribir las notas y el manual de este proyecto en segundo plano; la barra lateral muestra cuánto lleva.",
  "could not rescan the notes": "no se pudieron reescanear las notas",
  "Rescanning the notes of this project in the background. The sidebar shows how far it got.":
    "Reescaneando las notas de este proyecto en segundo plano. La barra lateral muestra cuánto lleva.",
  "Scanning the notes": "Escaneando las notas",
  "New repository": "Nuevo repositorio",
  "No repositories": "Sin repositorios",
  "New session in repository": "Nueva sesión en el repositorio",
  "Delete repository": "Eliminar repositorio",
  "or a folder on this machine": "o una carpeta de este equipo",
  "Could not add the repository": "No se pudo agregar el repositorio",
  "branch {branch}": "rama {branch}",
  "The repository is cloned once. A folder is used where it is and never deleted.":
    "El repositorio se clona una sola vez. Una carpeta se usa donde está y nunca se elimina.",
  Upkeep: "Mantenimiento",
  "Weekly check": "Revisión semanal",
  "Once a week Motita looks over each project without changing anything and tells you what is new. It touches no code.":
    "Una vez a la semana Motita revisa cada proyecto sin cambiar nada y te cuenta qué hay de nuevo. No toca el código.",
  "Run the weekly check now": "Hacer la revisión semanal ahora",
  "Last check: {date}": "Última revisión: {date}",
  "Next: {date}": "Próxima: {date}",
  "Draft pull requests": "Pull requests en borrador",
  "For small, safe findings, such as texts, missing tests and code-quality warnings, Motita opens a draft pull request in the project's own repository. It never changes the main branch and never merges.":
    "Para hallazgos pequeños y seguros, como textos, pruebas que faltan y avisos de calidad del código, Motita abre un pull request en borrador en el repositorio del propio proyecto. Nunca cambia la rama principal ni fusiona nada.",
  "Look for small fixes now": "Buscar arreglos pequeños ahora",
  "Last round of fixes: {date}": "Última ronda de arreglos: {date}",
  "Most open pull requests": "Máximo de pull requests abiertos",
  "Motita keeps at most this many of its own pull requests open at once, from 1 to 10. It opens no more until you merge or close some.":
    "Motita mantiene abiertos a la vez como máximo esta cantidad de pull requests propios, de 1 a 10. No abre más hasta que fusiones o cierres alguno.",
  Journal: "Diario",
  "Loading…": "Cargando…",
  "This gateway has no journal: it is older than this page.":
    "Este gateway no tiene diario: es más antiguo que esta página.",
  "Motita writes down, on this computer only, the failures, retries and refused actions of your sessions, and the answers you mark as not good. Nothing leaves it, secrets are masked, and nothing changes by itself.":
    "Motita anota, solo en este equipo, los fallos, reintentos y acciones rechazadas de tus sesiones, y las respuestas que marcas como no buenas. Nada sale de aquí, los secretos se ocultan y nada cambia por sí solo.",
  "Clear the journal": "Borrar el diario",
  "Clear the whole journal?": "¿Borrar todo el diario?",
  "Everything written so far is deleted, not just this week.":
    "Se borra todo lo anotado hasta ahora, no solo esta semana.",
  "This was not right": "Esto no estuvo bien",
  "What went wrong? (optional)": "¿Qué salió mal? (opcional)",
  Send: "Enviar",
  "Noted, thanks": "Anotado, gracias",
  "It stays in the journal on this computer; nothing is sent anywhere.":
    "Queda en el diario de este equipo; no se envía a ningún lado.",
  "could not note it": "no se pudo anotar",
  "Guides to your projects, your conversations and your own ideas.":
    "Guías de tus proyectos, tus conversaciones y tus propias ideas.",
  "See how the notes connect to each other":
    "Mira cómo se conectan las notas entre sí",
  "Start here": "Empieza aquí",
  "Technical overview of {name}": "Resumen técnico de {name}",
  "Project page: {name}": "Página del proyecto {name}",
  Conversations: "Conversaciones",
  "Update this guide": "Actualizar esta guía",
  "Update these notes": "Actualizar estas notas",
  "Writes them again from your project, in the background":
    "Las vuelve a escribir a partir de tu proyecto, en segundo plano",
  "Delete this guide": "Eliminar esta guía",
  "Delete this folder": "Eliminar esta carpeta",
  "Options for {name}": "Opciones de {name}",
  "Guide · {pages}": "Guía · {pages}",
  "1 page": "1 página",
  "{n} pages": "{n} páginas",
  "Deletes its 1 note": "Elimina su única nota",
  "Deletes its {n} notes": "Elimina sus {n} notas",
  "What is the note about?": "¿De qué trata la nota?",
  "Updating the guides": "Actualizando las guías",
  "More options for the notes": "Más opciones para las notas",
  "Update the guides": "Actualizar las guías",
  "Writes the guide of every project again":
    "Vuelve a escribir la guía de cada proyecto",
  "Write a new note": "Escribir una nota nueva",
  "Pages that link here ({n})": "Páginas que enlazan aquí ({n})",
  "Options for this note": "Opciones de esta nota",
  "Links from other notes follow the new name":
    "Los enlaces de otras notas siguen el nuevo nombre",
  "Asks before deleting it": "Pregunta antes de eliminarla",
  "Welcome to your notes": "Bienvenido a tus notas",
  "Here Motita keeps what it learns about your projects, and you can write your own notes.":
    "Aquí Motita guarda lo que aprende de tus proyectos, y tú puedes escribir tus propias notas.",
  "Start with a guide: it explains a project in plain words, one chapter for each part.":
    "Empieza por una guía: explica un proyecto con palabras sencillas, con un capítulo para cada parte.",
  "Open the guide of {name}": "Abrir la guía de {name}",
  "There are no guides yet. Motita can write one for each of your projects.":
    "Todavía no hay guías. Motita puede escribir una para cada uno de tus proyectos.",
  "Write the guides now": "Escribir las guías ahora",
  "Where are my notes kept?": "¿Dónde se guardan mis notas?",
  "They are ordinary text files, so other programs such as Obsidian can open them too. The folder is {root}":
    "Son archivos de texto normales, así que otros programas como Obsidian también pueden abrirlas. La carpeta es {root}",
  "The note is deleted for good, with everything written in it.":
    "La nota se elimina para siempre, con todo lo que tiene escrito.",
  "Everything in it is deleted, including what you wrote in these notes. Motita can write it again from your project when you update the guides.":
    "Se elimina todo lo que contiene, incluido lo que hayas escrito en estas notas. Motita puede volver a escribirlo a partir de tu proyecto cuando actualices las guías.",
  "Everything in it is deleted, including what you wrote in these notes. This cannot be undone.":
    "Se elimina todo lo que contiene, incluido lo que hayas escrito en estas notas. No se puede deshacer.",
  "Delete this folder of notes?": "¿Eliminar esta carpeta de notas?",
  "Delete this note?": "¿Eliminar esta nota?",
  "Delete the note": "Eliminar la nota",
  "Keep it": "Conservarla",
  "1 note will be deleted.": "Se eliminará 1 nota.",
  "{n} notes will be deleted.": "Se eliminarán {n} notas.",
  "Delete 1 note": "Eliminar 1 nota",
  "Delete {n} notes": "Eliminar {n} notas",
  "could not delete the folder": "no se pudo eliminar la carpeta",
  "1 note deleted": "1 nota eliminada",
  "{n} notes deleted": "{n} notas eliminadas",
  "could not update the notes": "no se pudieron actualizar las notas",
};

export default more;
