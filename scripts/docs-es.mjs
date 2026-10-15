// Spanish text of the Documentation pages, keyed by the slug used in
// gen-docs.mjs. Each entry replaces the English title, blurb and body; `more`
// holds only the link labels, in the same order as the English page, because
// the URLs are shared and must not be maintained twice.
export const es = {
  install: {
    title: "Instalación y primer uso",
    blurb: "Un comando para instalar y una configuración corta la primera vez.",
    more: ["Guía: Install", "Referencia: Installation", "Referencia: motita doctor"],
    body: `
<p>motita es un único binario estático. No hay nada que compilar ni nada más que instalar antes.</p>
<h2>Instalar</h2>
<pre><span class="c"># Linux y macOS</span>
curl -fsSL https://motita.kodingvibes.com/install.sh | sh

<span class="c"># Windows (PowerShell)</span>
irm https://motita.kodingvibes.com/install.ps1 | iex</pre>
<p>El instalador detecta tu sistema y arquitectura, descarga el binario que corresponde y lo verifica contra el <code>SHA256SUMS</code> de la versión. Si no coincide, se detiene y tu sistema queda intacto. Se instala sin permisos de administrador y ejecuta el binario una vez para comprobar que arranca.</p>
<h2>Primer uso</h2>
<p>Escribe <code>motita</code>. Si no hay configuración, arranca solo una configuración corta de cinco pasos. No escribe nada hasta que ves el resultado:</p>
<ol>
<li><strong>Proveedor.</strong> Qué servicio de modelos responde: OpenAI, ChatGPT (Codex), GitHub Copilot, Ollama, Anthropic, tu suscripción de Claude, Gemini o Qwen.</li>
<li><strong>Conexión.</strong> Inicia sesión con tu cuenta o pega una API key (queda oculta al pegarla).</li>
<li><strong>Modelo.</strong> Elige uno de los sugeridos o escribe cualquier ID de modelo.</li>
<li><strong>Verificación.</strong> Qué decide que una tarea está realmente terminada. Por defecto se detecta desde el proyecto: tu <code>make test</code>, <code>go test</code>, <code>npm test</code>, <code>cargo test</code>. Un proyecto con una compilación poco habitual lista sus propios comandos en <code>.motita/anchor</code>.</li>
<li><strong>Revisar y guardar.</strong> Todas las respuestas en una pantalla. <code>n</code> las recorre otra vez.</li>
</ol>
<p>Puedes repetir la configuración cuando quieras con <code>/config</code> dentro de motita o con <code>motita config</code>. Ofrece conservar tu clave y sesión actuales, y guarda el archivo anterior como <code>motita.yaml.bak</code>.</p>
<h2>Cuando algo no funciona</h2>
<p><code>motita doctor</code> revisa la máquina antes de ejecutar nada, una línea por prueba: la compilación frente al sistema, el shell, el DNS, tu <code>PATH</code>, git, la configuración y el sandbox que construiría. No necesita clave ni llama a ningún modelo.</p>
<div class="note">Las máquinas antiguas y pequeñas son un objetivo, no un detalle: hay compilaciones para x86 de 32 bits, ARM, ARM64, macOS y Windows.</div>`,
  },
  terminal: {
    title: "La interfaz de terminal",
    blurb: "La interfaz a pantalla completa que obtienes al escribir motita.",
    more: [
      "Guía: la interfaz de terminal",
      "Referencia: colores y glifos",
      "Referencia: el idioma de la interfaz",
      "Revisión de diseño de la TUI",
    ],
    body: `
<p>Ejecuta <code>motita</code> sin argumentos y obtienes una interfaz de terminal completa, escrita solo con la biblioteca estándar de Go.</p>
<img src="shots/tui-welcome.webp" alt="La interfaz de terminal justo después de la configuración" width="987" height="659" loading="lazy" />
<h2>Lo que ves</h2>
<ul>
<li><strong>Quién responde.</strong> Una línea nombra el proveedor y el modelo, y cuánto piensa. Si falta la clave, lo dice con palabras y cómo agregarla.</li>
<li><strong>Qué hará Enter.</strong> El cuadro de entrada lleva el modo como título. <strong>Task</strong> hace cambios y los demuestra con tu verificación. <strong>Plan</strong> solo lee y explica. <kbd>Tab</kbd> cambia de modo.</li>
<li><strong>Las teclas que sirven ahora.</strong> El pie cambia mientras escribes un comando, mientras corre una tarea o mientras te desplazas hacia atrás.</li>
</ul>
<h2>Mantener el control</h2>
<ul>
<li><kbd>Esc</kbd> detiene la tarea en curso.</li>
<li>Un mensaje escrito mientras el agente trabaja queda en cola y se envía al terminar.</li>
<li><code>/</code> abre la lista de comandos. <code>?</code> muestra todos los comandos y teclas. Una palabra suelta siempre es un mensaje, así que escribir <code>new</code> nunca ejecuta un comando por accidente.</li>
<li>Un turno fallido muestra el error tal como ocurrió más una pista para lo primero que encuentran quienes empiezan: Ollama apagado, una clave rechazada, un modelo inexistente, un límite de uso.</li>
</ul>
<h2>Comandos que conviene conocer</h2>
<table>
<thead><tr><th>Comando</th><th>Qué hace</th></tr></thead>
<tbody>
<tr><td><code>/config</code></td><td>Repite la configuración.</td></tr>
<tr><td><code>/models</code></td><td>Lista y cambia el modelo de esta conversación.</td></tr>
<tr><td><code>/sessions</code>, <code>/attach</code>, <code>/new</code>, <code>/back</code></td><td>Ver, unirse, abrir y dejar conversaciones. Una ejecución sigue mientras miras otra.</td></tr>
<tr><td><code>/agents</code> (<kbd>Ctrl+G</kbd>)</td><td>Los agentes en segundo plano y qué hace cada uno.</td></tr>
<tr><td><code>/good</code>, <code>/bad</code></td><td>Dile a motita si el último procedimiento ayudó.</td></tr>
<tr><td><code>/git</code></td><td>Conecta GitHub, GitLab o Bitbucket.</td></tr>
<tr><td><code>/artifacts</code></td><td>Archivos que el agente guardó en esta conversación.</td></tr>
<tr><td><code>/language</code></td><td>Cambia entre inglés y español al instante.</td></tr>
<tr><td><code>/update</code></td><td>Instala la versión más nueva.</td></tr>
</tbody>
</table>
<h2>Idioma</h2>
<p>La terminal, el navegador y la configuración hablan inglés o español. Por defecto siguen el idioma de tu sistema; <code>ui.language</code> o <code>/language</code> elige uno para todas las interfaces.</p>`,
  },
  web: {
    title: "Interfaz web y gateway",
    blurb: "El mismo agente desde un navegador o un teléfono, servido por un solo puerto.",
    more: [
      "Guía: la interfaz del navegador",
      "Guía: más de una conversación",
      "Guía: protocolo WebSocket",
      "Referencia: el gateway",
    ],
    body: `
<p>Cada proceso de motita es también un <strong>gateway</strong>: escucha en <code>127.0.0.1:7477</code> detrás de un token, y la terminal que miras es solo uno de sus clientes. Una página web o un teléfono pueden unirse a la misma conversación, con las mismas skills y el mismo historial.</p>
<img src="shots/webui-phone.webp" alt="La interfaz del navegador en un teléfono" class="phone" width="600" height="1298" loading="lazy" />
<h2>La interfaz del navegador</h2>
<p>Se sirve desde el puerto del propio gateway: no hay un segundo servidor ni configuración entre orígenes. Abre <code>http://127.0.0.1:7477</code>, pega el token una vez y tienes chat, proyectos, ajustes y la biblioteca de skills. Se instala como app (PWA) y va compilada dentro del binario.</p>
<p>El token se lee de <code>~/.motita/gateway.token</code> y lo imprime <code>motita gateway start</code>.</p>
<h2>Ejecutar el gateway</h2>
<pre>motita gateway start    <span class="c"># inicia el servicio y espera a que responda</span>
motita gateway status   <span class="c"># si hay uno corriendo, dónde y qué compilación</span>
motita gateway stop
motita gateway setup    <span class="c"># dónde responde: esta máquina, la LAN, todos</span></pre>
<p>Si ya hay un gateway corriendo, <code>motita</code> se conecta a él y nunca lo apaga. Si no hay ninguno, levanta uno para sí mismo y lo detiene al salir.</p>
<h2>Las dos mitades por separado</h2>
<pre><span class="c"># en la máquina que ejecuta los comandos</span>
motita -serve -gateway 127.0.0.1:7477

<span class="c"># en cualquier otro lugar: sin sandbox, sin agente en este proceso</span>
motita -connect 127.0.0.1:7477 -tui</pre>
<p>Un cliente <code>-connect</code> es solo una interfaz, así que corre en un portátil, una tableta o un teléfono a través de un túnel. Cómo llegar desde otras máquinas de forma segura está en <a href="docs-es-network.html">Red y acceso remoto</a>.</p>
<h2>Más de una conversación</h2>
<p>Un gateway mantiene muchas sesiones a la vez. El modelo pertenece a la sesión: cambiarlo afecta solo a esa conversación y sobrevive a un reinicio. Otros programas pueden manejar las mismas sesiones por HTTP, eventos del servidor o un protocolo WebSocket; la referencia lista todos los endpoints.</p>`,
  },
  projects: {
    title: "Proyectos, sesiones y git",
    blurb: "Cada sesión trabaja en su propia rama; los pull requests se siguen por ti.",
    more: [
      "Guía: proyectos, sesiones y volver atrás",
      "Guía: hosts de git",
      "Guía: agentes en segundo plano",
      "Prueba manual del flujo de PR",
    ],
    body: `
<p>Apunta motita a un repositorio y se convierte en un <strong>proyecto</strong>. Todo lo que hace una sesión ocurre en una copia aislada, así que puedes correr varios trabajos a la vez sin que se pisen entre sí ni toquen tu copia de trabajo.</p>
<h2>Sesiones y worktrees</h2>
<ul>
<li>Cada sesión de un proyecto tiene <strong>su propio git worktree en su propia rama</strong> (<code>motita/&lt;sesión&gt;</code>). Las carpetas de dependencias como <code>node_modules</code> se enlazan en vez de reinstalarse.</li>
<li><strong>Cada turno es un punto de control.</strong> Vuelve a cualquier turno; la conversación retrocede, y los archivos también si lo pides.</li>
<li>Pídele al agente que <strong>integre</strong> su trabajo: hace commit y fusiona la rama en la del proyecto. La sesión pasa a solo lectura y <em>continuar</em> abre una nueva desde la base actualizada.</li>
<li><strong>Borrar pregunta primero.</strong> Ves exactamente qué archivos sin confirmar se perderían.</li>
<li>Se leen los archivos de instrucciones: <code>AGENTS.md</code> (o <code>CLAUDE.md</code>) desde la raíz de git hacia abajo, y tu propio <code>~/.motita/AGENTS.md</code>. Guían cómo trabajar y nunca pasan por encima de tu petición ni de tu configuración de seguridad.</li>
</ul>
<h2>Hosts de git</h2>
<p>Conecta GitHub, GitLab, Bitbucket, o un GitLab, Gitea o GitHub Enterprise propio, una sola vez, desde <strong>Ajustes &rarr; Conexiones git</strong> o con <code>/git connect</code>. GitHub inicia sesión con un clic. Los otros hosts necesitan una aplicación OAuth tuya o un token que creas a mano; un token hace todo lo que hace el inicio de sesión y se valida antes de guardarse.</p>
<p>Después, <code>git clone</code>, <code>pull</code> y <code>push</code> funcionan dentro de una sesión. Las credenciales solo van a comandos que aprobaste y se guardan en <code>~/.motita/auth/</code> con permisos solo para tu usuario, nunca en el archivo de configuración.</p>
<h2>Pull requests y CI</h2>
<ul>
<li>Los mensajes de commit y los títulos de pull request deben ser semánticos (<code>tipo(ámbito): descripción</code>); cualquier otro se rechaza antes de crearse.</li>
<li>El agente abre un pull request con <code>motita forge pr create</code> (te pregunta antes) y te da su enlace.</li>
<li>Desde ese momento el <em>gateway</em> sigue el CI, las revisiones y el estado de la fusión sin usar el modelo, así que esperar no gasta tokens. Cuando falla un job o alguien comenta, se envía al agente a arreglarlo, hasta un límite que tú fijas (5 por defecto).</li>
<li>Un proyecto puede fusionar solo (<code>auto_merge</code>) y abrir la siguiente sesión solo (<code>auto_continue</code>).</li>
</ul>
<h2>Agentes en segundo plano</h2>
<p>Un trabajo con partes independientes no tiene que hacerse pieza por pieza. El agente puede iniciar agentes en segundo plano, cada uno en su propia rama, hasta <code>agent.max_parallel</code> (3 por defecto). Cada uno tiene un rol: <code>code</code> escribe, <code>review</code> y <code>research</code> leen y no cambian nada. Ves el propósito, estado, tiempo y costo en tokens de cada uno en la terminal (<kbd>Ctrl+G</kbd>) y en el panel de agentes del navegador.</p>`,
  },
  models: {
    title: "Modelos y proveedores",
    blurb: "Trae tu propio modelo y elige uno distinto para cada tipo de trabajo.",
    more: [
      "Guía: proveedores y cómo se autentica cada uno",
      "Guía: tu suscripción de Claude",
      "Referencia: proveedores LLM desde el navegador",
      "Referencia: modelos por tipo de trabajo",
    ],
    body: `
<p>motita no trae un modelo. Habla con el que ya usas, y el bucle que lo rodea es el mismo responda quien responda.</p>
<h2>Proveedores</h2>
<table>
<thead><tr><th>Proveedor</th><th>Cómo te conectas</th></tr></thead>
<tbody>
<tr><td>OpenAI y cualquier host compatible con OpenAI</td><td>API key</td></tr>
<tr><td>ChatGPT (Codex)</td><td>Iniciar sesión con tu plan de ChatGPT</td></tr>
<tr><td>GitHub Copilot</td><td>Código de dispositivo de GitHub</td></tr>
<tr><td>Anthropic</td><td>API key</td></tr>
<tr><td>Suscripción de Claude (<code>claude-code</code>)</td><td><code>claude auth login</code> en el cliente de Anthropic</td></tr>
<tr><td>Google Gemini</td><td>API key o tu propio inicio de sesión de Google</td></tr>
<tr><td>Qwen</td><td>API key de DashScope</td></tr>
<tr><td>Ollama</td><td>Nada en tu máquina; una API key para Ollama Cloud</td></tr>
</tbody>
</table>
<p>Una API key siempre gana sobre una sesión guardada. Las claves que pegas van a un archivo aparte solo para tu usuario junto a la configuración (<code>~/.motita/motita.env</code>), así que la configuración puede versionarse y compartirse. Una clave nunca se envía de vuelta al navegador.</p>
<img src="shots/webui-ask.webp" alt="La interfaz del navegador respondiendo una pregunta" width="1600" height="1000" loading="lazy" />
<h2>Desde el navegador</h2>
<p><strong>Ajustes &rarr; Proveedores</strong> lista cada proveedor, de dónde sale su clave, un campo para clave y endpoint, y un botón que prueba la conexión listando los modelos del proveedor. Copilot, Qwen, Codex y Gemini pueden iniciar sesión con una cuenta desde la misma página.</p>
<h2>Un modelo para cada tipo de trabajo</h2>
<p>La pestaña <strong>Modelos</strong> fija el modelo por defecto y, debajo, uno para cada tipo de trabajo. Un tipo en <em>Igual que el predeterminado</em> sigue a la sesión.</p>
<table>
<thead><tr><th>Tipo</th><th>Se usa para</th></tr></thead>
<tbody>
<tr><td><code>plan</code></td><td>Turnos en modo Plan</td></tr>
<tr><td><code>code</code></td><td>Modo Task y agentes en segundo plano que escriben</td></tr>
<tr><td><code>review</code></td><td>Agentes en segundo plano con rol de revisión</td></tr>
<tr><td><code>research</code></td><td>Agentes en segundo plano con rol de investigación</td></tr>
<tr><td><code>summary</code></td><td>Títulos, resúmenes de compactación, la suposición del modo auto</td></tr>
</tbody>
</table>
<p>Un modelo potente para planificar y uno barato para resúmenes es una división común. Si el proveedor de un tipo no tiene clave, vuelve al modelo de la sesión y el registro lo dice.</p>`,
  },
  voice: {
    title: "Voz",
    blurb: "Dicta a motita y haz que lea sus respuestas en voz alta.",
    more: ["Referencia: entrada de voz", "Referencia: leer respuestas en voz alta"],
    body: `
<p>La interfaz del navegador puede escuchar y hablar. La voz está apagada hasta que la activas en <strong>Ajustes &rarr; Voz</strong>.</p>
<h2>Entrada de voz</h2>
<p>Un botón de micrófono junto a <strong>Enviar</strong> dicta en el cuadro de mensaje. El texto siempre queda en el cuadro para que lo leas y edites; nunca se envía solo. Un modo manos libres escucha una frase de activación (por defecto <em>hey motita</em>) mientras la pestaña está abierta.</p>
<p><strong>Quién escucha</strong> lo eliges tú:</p>
<ul>
<li><strong>Este navegador.</strong> Un modelo Whisper que el gateway descarga una vez y que la página ejecuta por sí misma, con WebGPU donde existe y con WebAssembly si no. Nada de lo que dices sale del dispositivo. Es la opción privada.</li>
<li><strong>Un servicio de transcripción.</strong> El gateway reenvía la grabación a un endpoint de transcripción compatible con OpenAI (OpenAI, Groq o un servidor whisper local). El audio se mantiene en memoria durante esa petición y nunca se escribe en disco.</li>
</ul>
<div class="note">Con un servicio de transcripción, el modo manos libres envía a ese endpoint todo lo que se dice mientras escucha. Por eso el manos libres está apagado por defecto.</div>
<p>El micrófono solo existe en una página servida por https o desde localhost, así que por una dirección LAN con http plano los interruptores aparecen desactivados con esa razón. Mira <a href="docs-es-network.html">Red y acceso remoto</a>.</p>
<h2>Leer respuestas en voz alta</h2>
<ul>
<li>El botón de altavoz en una respuesta, o <kbd>Alt+L</kbd> para la última, la lee o la detiene.</li>
<li><strong>Ajustes &rarr; Leer en voz alta</strong> elige cuándo se leen solas las respuestas (nunca, solo tras un turno hablado, o siempre), además de voz, velocidad y largo.</li>
<li>Las voces pueden ser las del dispositivo o voces neuronales locales que el navegador ejecuta por sí mismo.</li>
<li>El estilo <strong>Natural</strong> habla del turno como un colega: una línea corta al empezar, otra sobre lo que está haciendo y lo esencial de la respuesta. <strong>Palabra por palabra</strong> lee el texto tal cual.</li>
<li>Los bloques de código y las tablas se nombran en vez de leerse.</li>
</ul>
<p>Con ambos activos, el modo <strong>Conversación</strong> vuelve a escuchar después de cada respuesta para que puedas hablar de ida y vuelta. Unos subtítulos opcionales muestran lo que se dice sobre el cuadro de mensaje.</p>`,
  },
  skills: {
    title: "Skills: lo que aprende",
    blurb: "Un estante de procedimientos que lee, escribe y mantiene ordenado, sin reentrenar.",
    more: [
      "Guía: aprende de ti",
      "Guía: el estante se mantiene ordenado",
      "Referencia: la biblioteca de skills",
    ],
    body: `
<p>motita mantiene una <strong>biblioteca de procedimientos</strong>: documentos que describen cómo se hace un tipo de trabajo, con los pasos, los comandos que funcionan y las trampas que alguien ya pagó. El modelo busca uno cuando lo necesita y escribe uno nuevo cuando aprende algo. Son archivos simples, así que puedes leerlos, corregirlos y versionarlos.</p>
<h2>Cómo mejora</h2>
<ul>
<li>Una instalación nueva parte con procedimientos incluidos (ejecutar comandos, archivos, git, APIs HTTP, búsqueda web, toolchains, verificar un cambio, diagramas e informes).</li>
<li><code>/good</code> y <code>/bad</code> caen sobre los procedimientos que realmente se usaron, y la biblioteca se busca según lo que funcionó antes.</li>
<li>Sin ajuste fino, sin API extra, sin cuenta extra. Un registro junto a un estante.</li>
</ul>
<h2>Ver y controlar el estante</h2>
<p>El navegador tiene una ventana de <strong>Biblioteca de skills</strong>: cada documento, para qué sirve, cuántas veces se usó, el documento mismo renderizado, y un interruptor para apagar uno sin perderlo.</p>
<p>Un proyecto puede tener sus propios procedimientos en <code>&lt;proyecto&gt;/.motita/skills/</code>; se buscan primero y tapan a la biblioteca compartida.</p>
<h2>El curador</h2>
<p>Si se deja solo, un estante se pudre, así que un curador corre cada semana, solo cuando nadie ha hablado con motita en dos horas. No usa modelo para lo que importa y solo toca documentos que escribió el agente:</p>
<ul>
<li>Sin uso por 14 días: se marca <strong>obsoleto</strong>, sigue buscable.</li>
<li>Obsoleto por 30 días más: se <strong>archiva</strong>, nunca se borra, un comando lo recupera.</li>
<li>Los documentos <strong>fijados</strong> quedan fuera de todo paso automático. Nada automático borra jamás.</li>
</ul>
<pre>motita curator status
motita curator run --dry-run
motita curator pin build-firmware</pre>`,
  },
  security: {
    title: "Seguridad y sandbox",
    blurb: "Lo que nunca puede hacer, lo que pregunta y cuánto disco alcanza.",
    more: [
      "Guía: guardarraíles que no se pueden apagar",
      "Referencia: guardarraíles y sus dos capas",
      "Referencia: acceso al disco",
      "Referencia: qué corre en silencio",
    ],
    body: `
<p>Un agente que ejecuta comandos necesita límites que no dependan de que el modelo se porte bien. motita tiene dos capas, y solo una es tuya.</p>
<h2>El piso</h2>
<p>Algunos comandos se rechazan siempre: formatear discos y editores de particiones, <code>shred</code>, <code>dd</code> hacia una salida, comandos de estado de energía, y <code>rm</code>, <code>mv</code>, <code>cp</code> o <code>ln</code> dirigidos a <code>/</code>, tu home, una ruta del sistema o la carpeta que contiene tu workspace (también tras <code>sudo</code>, <code>xargs</code> o <code>find -exec</code>). <strong>Ninguna clave, variable o flag relaja el piso.</strong></p>
<h2>Permitir, preguntar, denegar</h2>
<table>
<thead><tr><th>Veredicto</th><th>Cuándo</th></tr></thead>
<tbody>
<tr><td>Permitir</td><td>El comando no cambia nada, o solo el workspace: <code>make</code>, <code>go build</code>, <code>npm test</code>, tus propios <code>./scripts/*</code>. Corre en silencio, porque una política que pregunta por eso termina apagada.</td></tr>
<tr><td>Preguntar</td><td>Llega a la red o al sistema, escribe fuera del workspace, o no se puede clasificar. Se te pregunta antes de ejecutarlo.</td></tr>
<tr><td>Denegar</td><td>Modo estricto y la línea no se puede ubicar.</td></tr>
</tbody>
</table>
<h2>El sandbox</h2>
<p>Los comandos corren confinados, sin Docker. Se limitan memoria, tiempo de CPU, procesos, archivos abiertos y tamaño de salida, y opcionalmente no hay red dentro del comando. En Linux con Landlock, un comando no puede escribir fuera de los directorios en los que trabaja ni leer las sesiones, la configuración o el almacén de credenciales de git de motita.</p>
<h2>Acceso al disco</h2>
<p><strong>Ajustes &rarr; Seguridad</strong> elige cuánto del disco alcanza el agente:</p>
<ul>
<li><strong>Solo el workspace</strong> (por defecto): se rechazan las ediciones de archivos fuera de la carpeta de la sesión, y en Linux un comando no puede leer ni listar tus archivos en otras partes (home, <code>/home</code>, <code>/mnt</code>, ...).</li>
<li><strong>Todo el disco</strong>: ambos llegan a cualquier parte que tu cuenta alcance. La página pregunta primero y explica el riesgo. Las credenciales propias de motita quedan ocultas en ambos casos.</li>
</ul>
<p>Donde nada confina un comando (Windows, macOS, Linux sin Landlock), el agente pregunta antes de ejecutar código del propio proyecto, salvo que lo desactives.</p>
<p>Cada sesión además tiene su propio HOME y no puede leer los demás proyectos y sesiones del mismo gateway.</p>`,
  },
  network: {
    title: "Red y acceso remoto",
    blurb: "Mantenlo en esta máquina, compártelo en tu LAN o llega desde un teléfono.",
    more: ["Referencia: el gateway", "Guía: la interfaz del navegador y las notificaciones"],
    body: `
<p>Por defecto el gateway responde solo en <code>127.0.0.1:7477</code>, así que solo la máquina donde corre puede alcanzarlo. Todo lo que sigue es un paso deliberado fuera de eso.</p>
<h2>Quién puede llegar</h2>
<p>El primer <code>gateway start</code> pregunta una vez, y <strong>Ajustes &rarr; Red</strong> o <code>motita gateway setup</code> lo cambia después:</p>
<table>
<thead><tr><th>Respuesta</th><th>Resultado</th></tr></thead>
<tbody>
<tr><td>Solo esta máquina (por defecto)</td><td>Loopback.</td></tr>
<tr><td>La red local</td><td>Escucha en todas las interfaces, con <code>allow: ["lan", "!any"]</code>.</td></tr>
<tr><td>Todos</td><td>Escucha en todas las interfaces sin reglas de origen.</td></tr>
</tbody>
</table>
<p><code>gateway.allow</code> es una lista ordenada de reglas (<code>lan</code>, una dirección, una red, cada una con prefijo <code>!</code> opcional). Gana la primera que coincide, y loopback siempre se permite, así que no puedes dejarte fuera. El token sigue siendo la llave real; las reglas dicen desde dónde puede venir.</p>
<div class="note">El gateway habla http plano. Fuera de loopback el token viaja sin cifrar, y el gateway te lo advierte al abrir.</div>
<h2>Llegar de forma segura</h2>
<ul>
<li><strong>Túnel SSH.</strong> <code>ssh -N -L 7477:127.0.0.1:7477 el-host</code> lo deja en <code>http://localhost:7477</code> en tu computador, sin exponer nada.</li>
<li><strong>Tailscale.</strong> Si Tailscale está corriendo, motita puede publicar el gateway con <code>tailscale serve</code>: https en el nombre de tu tailnet, accesible solo desde tus propios dispositivos. Funnel (internet pública) nunca se usa.</li>
<li><strong>Un proxy inverso con TLS</strong> como Caddy delante de un gateway en loopback, o el segundo listener https del propio gateway (<code>gateway.tls</code>).</li>
</ul>
<h2>Por qué importa https</h2>
<p>Los navegadores permiten el micrófono, las notificaciones de escritorio y el push al teléfono solo en https o localhost. Un teléfono en la LAN con http plano no obtiene ninguno, y es la razón habitual para elegir una de las opciones de arriba.</p>
<h2>Notificaciones</h2>
<p>Cuando termina el CI de un pull request, la página muestra un aviso. Las <strong>notificaciones al teléfono</strong> usan Web Push estándar desde el propio gateway, así que llegan con la página cerrada. En un iPhone o iPad la página debe agregarse antes a la pantalla de inicio (iOS 16.4 o posterior). <strong>Ajustes &rarr; Notificaciones</strong> tiene un botón de prueba de todo el camino.</p>`,
  },
  automation: {
    title: "Actualizaciones y tareas programadas",
    blurb: "Mantente en la última versión verificada y ejecuta tareas con una cadencia.",
    more: ["Referencia: tareas programadas", "Guía: versiones automáticas"],
    body: String.raw`
<h2>Actualizar</h2>
<p><code>/update</code> instala la versión más nueva. La pantalla de bienvenida y la barra lateral del navegador avisan cuando existe una. La descarga debe coincidir con el <code>SHA256SUMS</code> de la versión o no se instala nada. Un gateway iniciado con <code>-serve</code> se reinicia solo con el nuevo binario; una interfaz te dice que la reinicies.</p>
<p>El gateway informa su propia compilación en <code>motita gateway status</code>, así que tras instalar sobre un binario viejo puedes saber si el proceso que sigue corriendo es el anterior.</p>
<p>Las versiones se crean automáticamente a partir de commits semánticos: <code>feat</code> sube la versión menor, <code>fix</code> el parche y un cambio incompatible la mayor. La versión más nueva siempre aparece en la <a href="index.html">página de inicio</a>.</p>
<h2>Tareas programadas</h2>
<p>Una <strong>tarea programada</strong> es una tarea o un plan que el gateway envía por sí solo, con una cadencia, sin nadie frente al teclado. Cada una es un pequeño archivo JSON en <code>~/.motita/schedules/</code> que puedes leer y editar a mano.</p>
<ul>
<li>La cadencia es una duración (<code>30m</code>, <code>6h</code>, <code>24h</code>), no sintaxis cron. <code>every 6h</code> significa seis horas después de la última ejecución.</li>
<li>La cadencia más corta permitida es un minuto (<code>schedule.min_every</code>).</li>
<li>Una tarea puede ser un <code>plan</code> (solo lectura) o un <code>task</code>, y puede fijarse a una sesión.</li>
<li>Créalas, cámbialas, ejecútalas al instante y bórralas con <code>/v1/schedules</code>, o desde la interfaz.</li>
</ul>
<pre>curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"auditoría nocturna","task":"revisa los logs y reporta","kind":"plan","every":"24h"}' \
  http://127.0.0.1:7477/v1/schedules</pre>`,
  },
};
