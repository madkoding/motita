// Command starlight es un agente de IA de línea de comandos escrito en Go puro
// (sólo biblioteca estándar, cero dependencias externas) que habla con
// cualquier API compatible con OpenAI.
//
// Está pensado para máquinas i386 (linux/386): el binario es estático, no usa
// cgo y aplica límites explícitos de memoria (lectura de archivos, salida de
// comandos, tamaño del historial) y timeouts de red.
//
// Variables de entorno:
//
//	OPENAI_API_KEY   (obligatoria) clave de la API.
//	OPENAI_BASE_URL  (opcional) base URL; por defecto https://api.openai.com/v1
//	OPENAI_MODEL     (opcional) modelo; por defecto gpt-4o-mini
//
// Uso:
//
//	export OPENAI_API_KEY=sk-...
//	starlight                          # REPL interactivo
//	starlight -p "lista los .go del directorio actual"   # una instrucción y sale
//
// Las trazas ([Pensando...], [Ejecutando herramienta: ...]) se escriben en
// stderr; la respuesta final en stdout, para poder canalizarla.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Presentación y límites de recursos
// ---------------------------------------------------------------------------

const (
	// Colores ANSI a mano: no se permite ninguna librería externa.
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"

	// maxFileBytes limita leer_archivo a 1 MiB: en un i386 la RAM es escasa y
	// no queremos que el agente intente cargar un vídeo o un log gigante.
	maxFileBytes = 1 << 20

	// maxToolOutputBytes recorta la salida acumulada de un comando (64 KiB).
	maxToolOutputBytes = 64 << 10

	// maxResponseBytes limita cuánto leemos de la respuesta HTTP.
	maxResponseBytes = 8 << 20

	// maxHistorial mensajes conservados en memoria (el system prompt siempre
	// queda). Evita que un REPL largo crezca sin control en 32 bits.
	maxHistorial = 41

	// timeoutComandoPorDefecto evita que un comando colgado bloquee al agente.
	timeoutComandoPorDefecto = 120

	defaultMaxLoops = 5
	defaultBaseURL  = "https://api.openai.com/v1"
	defaultModel    = "gpt-4o-mini"
	defaultTimeout  = 60 * time.Second

	nombrePrograma = "starlight"
)

// version se inyecta en el build: -ldflags "-X main.version=v1.0.0".
var version = "dev"

// colorEnabled se apaga con --sin-color o con NO_COLOR=1.
var colorEnabled = true

// paint envuelve s en un color ANSI respetando la configuración global.
func paint(color, s string) string {
	if !colorEnabled || color == "" {
		return s
	}
	return color + s + colorReset
}

// traza escribe mensajes de progreso en stderr (nunca contaminan stdout).
func traza(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

const systemPrompt = `Eres Starlight, un agente de terminal que ayuda a trabajar en el sistema local.
Respondes en español, de forma directa y concisa.

Reglas:
- Usa las herramientas cuando necesites información real del sistema (contenido de
  archivos o salida de comandos). Nunca inventes el contenido de un archivo ni el
  resultado de un comando.
- Prefiere comandos de sólo lectura. Antes de ejecutar algo destructivo (borrar,
  sobrescribir, instalar, publicar), explica qué vas a hacer y espera confirmación.
- Si una herramienta devuelve un error, léelo y corrige el plan antes de reintentar.
- Cuando ya tengas la respuesta, entrégala como texto final sin volver a llamar
  herramientas.`

// ---------------------------------------------------------------------------
// Tipos de la API compatible con OpenAI
// ---------------------------------------------------------------------------

type chatRequest struct {
	Model      string    `json:"model"`
	Messages   []message `json:"messages"`
	Tools      []tool    `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
}

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type tool struct {
	Type     string      `json:"type"`
	Function functionDef `json:"function"`
}

type functionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatResponse struct {
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Configuración
// ---------------------------------------------------------------------------

type config struct {
	apiKey   string
	baseURL  string
	model    string
	maxLoops int
	timeout  time.Duration
}

func getEnv(clave, porDefecto string) string {
	if v, ok := os.LookupEnv(clave); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return porDefecto
}

// ---------------------------------------------------------------------------
// Herramientas del agente
// ---------------------------------------------------------------------------

// leerArchivoArgs son los argumentos de leer_archivo.
type leerArchivoArgs struct {
	Ruta string `json:"ruta"`
}

// ejecutarComandoArgs son los argumentos de ejecutar_comando.
type ejecutarComandoArgs struct {
	Cmd             string `json:"cmd"`
	TimeoutSegundos int    `json:"timeout_segundos,omitempty"`
}

// decodeArgs deserializa los argumentos de una herramienta.
//
// Cubre tres formatos que se ven en producción (no todos los proveedores
// "compatibles con OpenAI" respetan la especificación):
//
//	{"ruta":"x"}          -> objeto JSON (lo que manda la spec)
//	"{\"ruta\":\"x\"}"    -> cadena que contiene JSON (visto en varios gateways)
//	(vacío)               -> sin argumentos
//
// Sin esta normalización el agente fallaría con "cannot unmarshal string" aunque
// el usuario haya escrito la petición correcta.
func decodeArgs(raw json.RawMessage, dst any) error {
	recortado := bytes.TrimSpace(raw)
	if len(recortado) == 0 || string(recortado) == "null" {
		return nil
	}
	if recortado[0] == '"' {
		var texto string
		if err := json.Unmarshal(recortado, &texto); err != nil {
			return err
		}
		if strings.TrimSpace(texto) == "" {
			return nil
		}
		recortado = []byte(texto)
	}
	return json.Unmarshal(recortado, dst)
}

// herramientaLeerArchivo implementa leer_archivo (texto, máx. 1 MiB).
func herramientaLeerArchivo(raw json.RawMessage) string {
	var args leerArchivoArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "Error al parsear argumentos: " + err.Error()
	}
	if strings.TrimSpace(args.Ruta) == "" {
		return "Error: falta el parámetro 'ruta'."
	}

	fi, err := os.Stat(args.Ruta)
	if err != nil {
		return "Error al acceder al archivo: " + err.Error()
	}
	if fi.IsDir() {
		return "Error: '" + args.Ruta + "' es un directorio; lista su contenido con ejecutar_comando (por ejemplo: ls -la " + args.Ruta + ")."
	}
	if fi.Size() > maxFileBytes {
		return fmt.Sprintf("Error: el archivo pesa %d bytes y supera el límite de %d bytes (1 MiB).", fi.Size(), maxFileBytes)
	}

	f, err := os.Open(args.Ruta)
	if err != nil {
		return "Error al abrir el archivo: " + err.Error()
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
	if err != nil {
		return "Error al leer el archivo: " + err.Error()
	}
	if len(data) == 0 {
		return "(el archivo está vacío)"
	}
	return string(data)
}

// bufferLimitado acumula como máximo max bytes y marca si hubo recorte, para no
// cargar en RAM la salida completa de un comando.
type bufferLimitado struct {
	buf      bytes.Buffer
	max      int
	truncado bool
}

func (b *bufferLimitado) Write(p []byte) (int, error) {
	espacio := b.max - b.buf.Len()
	if espacio <= 0 {
		b.truncado = true
		return len(p), nil // se descarta el excedente, pero el comando no falla
	}
	if espacio < len(p) {
		b.buf.Write(p[:espacio])
		b.truncado = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// herramientaEjecutarComando implementa ejecutar_comando vía `sh -c`, para
// soportar pipes, redirecciones y comillas como lo haría una persona.
func herramientaEjecutarComando(raw json.RawMessage) string {
	var args ejecutarComandoArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "Error al parsear argumentos: " + err.Error()
	}
	comando := strings.TrimSpace(args.Cmd)
	if comando == "" {
		return "Error: falta el parámetro 'cmd'."
	}

	segundos := args.TimeoutSegundos
	if segundos <= 0 {
		segundos = timeoutComandoPorDefecto
	}

	ctx, cancelar := context.WithTimeout(context.Background(), time.Duration(segundos)*time.Second)
	defer cancelar()

	cmd := exec.CommandContext(ctx, "sh", "-c", comando)
	// Los comandos van a su propio grupo de procesos y se matan en grupo al
	// expirar el plazo: si sólo muriera `sh`, sus hijos seguirían con el pipe
	// abierto y Wait se quedaría esperando (ver proceso_unix.go).
	configurarGrupo(cmd)
	cmd.Cancel = func() error { return matarGrupo(cmd) }
	cmd.WaitDelay = 2 * time.Second

	salida := &bufferLimitado{max: maxToolOutputBytes}
	cmd.Stdout = salida
	cmd.Stderr = salida

	err := cmd.Run()
	texto := salida.buf.String()
	if salida.truncado {
		texto += fmt.Sprintf("\n[... salida truncada a %d KiB ...]", maxToolOutputBytes/1024)
	}

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Error: el comando excedió el límite de %d s y fue terminado.\nSalida parcial:\n%s", segundos, texto)
	}
	if err != nil {
		return fmt.Sprintf("Error de ejecución: %v\nSalida:\n%s", err, texto)
	}
	if strings.TrimSpace(texto) == "" {
		return "(el comando terminó sin producir salida)"
	}
	return texto
}

// definicionHerramientas describe las funciones expuestas al modelo.
func definicionHerramientas() []tool {
	return []tool{
		{
			Type: "function",
			Function: functionDef{
				Name:        "leer_archivo",
				Description: "Lee el contenido de un archivo de texto del sistema local (máximo 1 MiB).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ruta": map[string]any{
							"type":        "string",
							"description": "Ruta absoluta o relativa del archivo a leer.",
						},
					},
					"required": []string{"ruta"},
				},
			},
		},
		{
			Type: "function",
			Function: functionDef{
				Name:        "ejecutar_comando",
				Description: "Ejecuta un comando de shell (sh -c) en el sistema local y devuelve stdout y stderr combinados.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{
							"type":        "string",
							"description": "Comando de shell a ejecutar (admite pipes y redirecciones).",
						},
						"timeout_segundos": map[string]any{
							"type":        "integer",
							"description": fmt.Sprintf("Tiempo máximo de ejecución en segundos (por defecto %d).", timeoutComandoPorDefecto),
						},
					},
					"required": []string{"cmd"},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Agente
// ---------------------------------------------------------------------------

type agente struct {
	cfg    config
	client *http.Client
	hist   []message
}

func nuevoAgente(cfg config) *agente {
	a := &agente{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.timeout},
	}
	a.reiniciar()
	a.hist = append(a.hist, message{Role: "system", Content: systemPrompt})
	return a
}

// reiniciar vacía el historial dejando sólo el prompt de sistema.
func (a *agente) reiniciar() {
	a.hist = []message{{Role: "system", Content: systemPrompt}}
}

// completar envía el historial a la API y devuelve el mensaje del asistente.
// toolChoice vacío = decidir libremente; "none" = prohibir herramientas.
func (a *agente) completar(toolChoice string) (message, error) {
	peticion := chatRequest{
		Model:      a.cfg.model,
		Messages:   a.hist,
		Tools:      definicionHerramientas(),
		ToolChoice: toolChoice,
	}
	cuerpo, err := json.Marshal(peticion)
	if err != nil {
		return message{}, fmt.Errorf("no se pudo serializar la petición: %w", err)
	}

	url := strings.TrimRight(a.cfg.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(cuerpo))
	if err != nil {
		return message{}, fmt.Errorf("petición inválida: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.apiKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return message{}, fmt.Errorf("error de red: %w", err)
	}
	defer resp.Body.Close()

	datos, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return message{}, fmt.Errorf("error al leer la respuesta: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(datos, &apiErr) == nil && apiErr.Error.Message != "" {
			return message{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErr.Error.Message)
		}
		return message{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(datos)))
	}

	var salida chatResponse
	if err := json.Unmarshal(datos, &salida); err != nil {
		return message{}, fmt.Errorf("respuesta ilegible: %w", err)
	}
	if salida.Error != nil && salida.Error.Message != "" {
		return message{}, errors.New("la API devolvió un error: " + salida.Error.Message)
	}
	// Algunos proveedores devuelven 200 con choices vacío: no indexar a ciegas.
	if len(salida.Choices) == 0 {
		return message{}, errors.New("la API no devolvió ninguna opción (choices vacío)")
	}
	return salida.Choices[0].Message, nil
}

// recortarHistorial mantiene el prompt de sistema y los últimos mensajes, sin
// dejar resultados de herramienta huérfanos al principio.
func (a *agente) recortarHistorial() {
	if len(a.hist) <= maxHistorial {
		return
	}
	recorte := a.hist[len(a.hist)-(maxHistorial-1):]
	for len(recorte) > 0 && recorte[0].Role == "tool" {
		recorte = recorte[1:]
	}
	nuevo := make([]message, 0, len(recorte)+1)
	nuevo = append(nuevo, a.hist[0])
	nuevo = append(nuevo, recorte...)
	a.hist = nuevo
}

// ejecutarHerramienta ejecuta una llamada del modelo y devuelve su resultado.
func (a *agente) ejecutarHerramienta(tc toolCall) string {
	traza("  %s\n", paint(colorYellow, "[⚙️  Ejecutando herramienta: "+tc.Function.Name+"]"))
	switch tc.Function.Name {
	case "leer_archivo":
		return herramientaLeerArchivo(tc.Function.Arguments)
	case "ejecutar_comando":
		return herramientaEjecutarComando(tc.Function.Arguments)
	default:
		return "Error: herramienta desconocida '" + tc.Function.Name + "'"
	}
}

// turno procesa una instrucción del usuario: bucle de agentes con un máximo de
// iteraciones de herramientas y respuesta final forzada si se agota.
func (a *agente) turno(entrada string) error {
	a.hist = append(a.hist, message{Role: "user", Content: entrada})
	a.recortarHistorial()

	mensajesPrevios := len(a.hist)

	for i := 1; i <= a.cfg.maxLoops; i++ {
		traza("  %s\n", paint(colorCyan, "[🧠 Pensando...]"))

		msg, err := a.completar("")
		if err != nil {
			// Se revierte el turno fallido para no dejar el historial inconsistente.
			a.hist = a.hist[:mensajesPrevios-1]
			return err
		}
		a.hist = append(a.hist, msg)
		a.recortarHistorial()

		if len(msg.ToolCalls) == 0 {
			contenido := strings.TrimSpace(msg.Content)
			if contenido == "" {
				contenido = "(el modelo devolvió una respuesta vacía)"
			}
			fmt.Fprintln(os.Stdout, paint(colorBold, contenido))
			return nil
		}

		for _, tc := range msg.ToolCalls {
			resultado := a.ejecutarHerramienta(tc)
			a.hist = append(a.hist, message{
				Role:       "tool",
				Content:    resultado,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
			a.recortarHistorial()
		}
	}

	// Se agotaron las iteraciones: pedimos la respuesta final sin herramientas.
	traza("  %s\n", paint(colorYellow, fmt.Sprintf("[⏳ Límite de %d iteraciones alcanzado: forzando respuesta final]", a.cfg.maxLoops)))
	msg, err := a.completar("none")
	if err != nil {
		a.hist = a.hist[:mensajesPrevios-1]
		return err
	}
	a.hist = append(a.hist, msg)
	a.recortarHistorial()

	contenido := strings.TrimSpace(msg.Content)
	if contenido == "" {
		contenido = "(el modelo no entregó respuesta final)"
	}
	fmt.Fprintln(os.Stdout, paint(colorBold, contenido))
	return nil
}

// ---------------------------------------------------------------------------
// Interfaz de usuario
// ---------------------------------------------------------------------------

func ayuda() {
	fmt.Fprint(os.Stderr, strings.Join([]string{
		"",
		paint(colorBold, "starlight — agente de IA de terminal (Go puro, compatible con OpenAI)"),
		"",
		"Comandos del REPL:",
		"  /ayuda      esta ayuda",
		"  /limpiar    olvida la conversación",
		"  salir       termina (también Ctrl+D)",
		"",
		"Variables de entorno:",
		"  OPENAI_API_KEY   clave de la API (obligatoria)",
		"  OPENAI_BASE_URL  por defecto https://api.openai.com/v1",
		"  OPENAI_MODEL     por defecto gpt-4o-mini",
		"",
	}, "\n"))
}

func main() {
	var (
		flagPrompt   = flag.String("p", "", "ejecuta una instrucción y termina (modo no interactivo)")
		flagModelo   = flag.String("modelo", "", "modelo a usar (por defecto $OPENAI_MODEL o "+defaultModel+")")
		flagURL      = flag.String("url", "", "base URL de la API (por defecto $OPENAI_BASE_URL)")
		flagLoops    = flag.Int("max-loops", defaultMaxLoops, "máximo de iteraciones de herramientas por turno")
		flagTimeout  = flag.Duration("timeout", defaultTimeout, "timeout HTTP por petición")
		flagSinColor = flag.Bool("sin-color", false, "desactiva los colores ANSI")
		flagVersion  = flag.Bool("version", false, "muestra la versión y sale")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Uso: %s [opciones] [instrucción]\n\nOpciones:\n", nombrePrograma)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *flagVersion {
		fmt.Printf("%s %s (%s/%s)\n", nombrePrograma, version, runtime.GOOS, runtime.GOARCH)
		return
	}

	if *flagSinColor || os.Getenv("NO_COLOR") != "" {
		colorEnabled = false
	}

	cfg := config{
		apiKey:   getEnv("OPENAI_API_KEY", ""),
		baseURL:  getEnv("OPENAI_BASE_URL", defaultBaseURL),
		model:    getEnv("OPENAI_MODEL", defaultModel),
		maxLoops: *flagLoops,
		timeout:  *flagTimeout,
	}
	if *flagModelo != "" {
		cfg.model = *flagModelo
	}
	if *flagURL != "" {
		cfg.baseURL = *flagURL
	}
	if cfg.maxLoops < 1 {
		cfg.maxLoops = 1
	}

	if cfg.apiKey == "" {
		fmt.Fprintf(os.Stderr, "%s\n", paint(colorRed, "❌ Error: debes definir la variable de entorno OPENAI_API_KEY."))
		fmt.Fprintf(os.Stderr, "   Ejemplo: export OPENAI_API_KEY=\"tu_clave\"\n")
		os.Exit(1)
	}

	ag := nuevoAgente(cfg)

	// Modo no interactivo: instrucción por bandera o por argumentos sueltos.
	entrada := *flagPrompt
	if entrada == "" && flag.NArg() > 0 {
		entrada = strings.Join(flag.Args(), " ")
	}
	if entrada != "" {
		if err := ag.turno(entrada); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", paint(colorRed, "❌ "+err.Error()))
			os.Exit(1)
		}
		return
	}

	traza("%s\n", paint(colorGreen, "🤖 Starlight listo. Escribe tu instrucción ('/ayuda' para ayuda, 'salir' para terminar)."))
	traza("%s\n", paint(colorDim, fmt.Sprintf("   modelo=%s  endpoint=%s  max-loops=%d", cfg.model, cfg.baseURL, cfg.maxLoops)))

	lector := bufio.NewReader(os.Stdin)
	for {
		traza("\n%s", paint(colorBold, "> "))

		linea, err := lector.ReadString('\n')
		linea = strings.TrimSpace(linea)

		if linea == "" {
			if err != nil { // EOF (Ctrl+D) o error de lectura
				traza("\n%s\n", paint(colorDim, "👋 Agente finalizado."))
				return
			}
			continue
		}

		switch strings.ToLower(linea) {
		case "salir", "exit", "/salir", "/exit", "quit":
			traza("%s\n", paint(colorDim, "👋 Agente finalizado."))
			return
		case "/ayuda", "/help", "ayuda":
			ayuda()
			continue
		case "/limpiar", "/reset":
			ag.reiniciar()
			traza("%s\n", paint(colorDim, "🧹 Conversación olvidada."))
			continue
		}

		if err := ag.turno(linea); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", paint(colorRed, "❌ "+err.Error()))
		}

		if err != nil { // el flujo de entrada se cerró
			traza("%s\n", paint(colorDim, "👋 Agente finalizado."))
			return
		}
	}
}
