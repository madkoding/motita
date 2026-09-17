package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	// Sin colores ANSI en las pruebas: la salida es más legible.
	colorEnabled = false
}

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("no se pudo serializar los argumentos: %v", err)
	}
	return raw
}

// --- Herramientas -----------------------------------------------------------

func TestLeerArchivo(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "nota.txt")
	contenido := "hola starlight\n"
	if err := os.WriteFile(ruta, []byte(contenido), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := herramientaLeerArchivo(args(t, leerArchivoArgs{Ruta: ruta})); got != contenido {
		t.Fatalf("contenido = %q, se esperaba %q", got, contenido)
	}

	if got := herramientaLeerArchivo(args(t, leerArchivoArgs{})); !strings.Contains(got, "falta el parámetro 'ruta'") {
		t.Fatalf("sin ruta se esperaba un error, se obtuvo %q", got)
	}

	if got := herramientaLeerArchivo(args(t, leerArchivoArgs{Ruta: ruta + ".inexistente"})); !strings.Contains(got, "Error al acceder al archivo") {
		t.Fatalf("archivo inexistente: se obtuvo %q", got)
	}

	if got := herramientaLeerArchivo(args(t, leerArchivoArgs{Ruta: t.TempDir()})); !strings.Contains(got, "es un directorio") {
		t.Fatalf("directorio: se obtuvo %q", got)
	}
}

func TestLeerArchivoRechazaGrande(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "grande.bin")
	// Un byte por encima del límite permitido.
	if err := os.WriteFile(ruta, make([]byte, maxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	got := herramientaLeerArchivo(args(t, leerArchivoArgs{Ruta: ruta}))
	if !strings.Contains(got, "supera el límite") {
		t.Fatalf("se esperaba rechazo por tamaño, se obtuvo %q", got)
	}
}

func TestEjecutarComando(t *testing.T) {
	got := herramientaEjecutarComando(args(t, ejecutarComandoArgs{Cmd: "echo starlight-ok"}))
	if strings.TrimSpace(got) != "starlight-ok" {
		t.Fatalf("salida = %q", got)
	}

	// Pipes y redirecciones deben funcionar (sh -c).
	got = herramientaEjecutarComando(args(t, ejecutarComandoArgs{Cmd: "printf 'a\\nb\\n' | wc -l"}))
	if strings.TrimSpace(got) != "2" {
		t.Fatalf("pipe no soportado, salida = %q", got)
	}

	got = herramientaEjecutarComando(args(t, ejecutarComandoArgs{Cmd: "exit 3"}))
	if !strings.Contains(got, "Error de ejecución") {
		t.Fatalf("se esperaba error de ejecución, se obtuvo %q", got)
	}

	if got := herramientaEjecutarComando(args(t, ejecutarComandoArgs{Cmd: "   "})); !strings.Contains(got, "falta el parámetro 'cmd'") {
		t.Fatalf("comando vacío: %q", got)
	}
}

func TestEjecutarComandoTimeout(t *testing.T) {
	inicio := time.Now()
	got := herramientaEjecutarComando(args(t, ejecutarComandoArgs{Cmd: "sleep 30", TimeoutSegundos: 1}))
	if !strings.Contains(got, "excedió el límite de 1 s") {
		t.Fatalf("se esperaba timeout, se obtuvo %q", got)
	}
	// El corte debe ser real: si sólo muriera `sh` y no su hijo `sleep`, Wait
	// seguiría esperando con el pipe abierto (regresión que ya ocurrió).
	if transcurrido := time.Since(inicio); transcurrido > 3*time.Second {
		t.Fatalf("el timeout no mató al grupo de procesos a tiempo: %s", transcurrido)
	}
}

// TestDecodeArgsFormatos cubre el bug real encontrado al probar contra un
// gateway que envía `arguments` como cadena JSON en vez de objeto.
func TestDecodeArgsFormatos(t *testing.T) {
	var destino leerArchivoArgs

	if err := decodeArgs(json.RawMessage(`{"ruta":"/tmp/x"}`), &destino); err != nil || destino.Ruta != "/tmp/x" {
		t.Fatalf("objeto JSON: ruta=%q err=%v", destino.Ruta, err)
	}

	destino = leerArchivoArgs{}
	if err := decodeArgs(json.RawMessage(`"{\"ruta\":\"/tmp/y\"}"`), &destino); err != nil || destino.Ruta != "/tmp/y" {
		t.Fatalf("cadena con JSON: ruta=%q err=%v", destino.Ruta, err)
	}

	destino = leerArchivoArgs{Ruta: "intacto"}
	if err := decodeArgs(json.RawMessage(``), &destino); err != nil || destino.Ruta != "intacto" {
		t.Fatalf("argumentos vacíos: ruta=%q err=%v", destino.Ruta, err)
	}
	if err := decodeArgs(json.RawMessage(`null`), &destino); err != nil || destino.Ruta != "intacto" {
		t.Fatalf("argumentos null: ruta=%q err=%v", destino.Ruta, err)
	}
}

func TestHerramientaDesconocida(t *testing.T) {
	a := nuevoAgente(config{baseURL: "http://127.0.0.1:1", model: "test", maxLoops: 1})
	got := a.ejecutarHerramienta(toolCall{Function: functionCall{Name: "borrar_todo"}})
	if !strings.Contains(got, "herramienta desconocida") {
		t.Fatalf("se esperaba error de herramienta desconocida, se obtuvo %q", got)
	}
}

// --- Cliente HTTP -----------------------------------------------------------

func TestCompletarErrores(t *testing.T) {
	casos := []struct {
		nombre    string
		respuesta string
		status    int
		contiene  string
	}{
		{"choices vacío no entra en pánico", `{"choices":[]}`, http.StatusOK, "choices vacío"},
		{"error de la API", `{"error":{"message":"clave inválida"}}`, http.StatusUnauthorized, "clave inválida"},
		{"cuerpo no JSON", `<html>bad gateway</html>`, http.StatusBadGateway, "HTTP 502"},
		{"json roto", `{"choices":`, http.StatusOK, "respuesta ilegible"},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(caso.status)
				fmt.Fprint(w, caso.respuesta)
			}))
			defer srv.Close()

			a := nuevoAgente(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
			if _, err := a.completar(""); err == nil || !strings.Contains(err.Error(), caso.contiene) {
				t.Fatalf("se esperaba error con %q, se obtuvo %v", caso.contiene, err)
			}
		})
	}
}

// --- Bucle de agente --------------------------------------------------------

// TestTurnoEjecutaHerramientaYResponde verifica el ciclo completo: el modelo
// pide una herramienta, el agente la ejecuta, envía el resultado y recibe el
// texto final.
func TestTurnoEjecutaHerramientaYResponde(t *testing.T) {
	var (
		peticiones      int
		segundaPeticion chatRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peticiones++
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("petición ilegible: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")

		if peticiones == 1 {
			// El modelo pide ejecutar un comando.
			if len(req.Tools) != 2 {
				t.Errorf("se esperaban 2 herramientas, se enviaron %d", len(req.Tools))
			}
			fmt.Fprint(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"ejecutar_comando",
				"arguments":"{\"cmd\":\"echo starlight-vivo\"}"}}]}}]}`)
			return
		}

		segundaPeticion = req
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"El comando devolvió starlight-vivo."}}]}`)
	}))
	defer srv.Close()

	a := nuevoAgente(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 5, timeout: 5 * time.Second})
	if err := a.turno("verifica que el sistema responde"); err != nil {
		t.Fatalf("turno devolvió error: %v", err)
	}

	if peticiones != 2 {
		t.Fatalf("se esperaban 2 peticiones, hubo %d", peticiones)
	}

	// El resultado de la herramienta debe viajar de vuelta al modelo.
	var resultado string
	for _, m := range segundaPeticion.Messages {
		if m.Role == "tool" {
			resultado = m.Content
			if m.ToolCallID != "call_1" {
				t.Errorf("tool_call_id = %q, se esperaba call_1", m.ToolCallID)
			}
		}
	}
	if !strings.Contains(resultado, "starlight-vivo") {
		t.Fatalf("el modelo no recibió la salida real de la herramienta: %q", resultado)
	}
}

// TestTurnoFuerzaRespuestaFinal comprueba que tras agotar las iteraciones se
// pide la respuesta con tool_choice="none".
func TestTurnoFuerzaRespuestaFinal(t *testing.T) {
	var ultimoToolChoice string
	peticiones := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peticiones++
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		ultimoToolChoice = req.ToolChoice
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",
			"tool_calls":[{"id":"c","type":"function","function":{"name":"ejecutar_comando","arguments":"{\"cmd\":\"true\"}"}}]}}]}`)
	}))
	defer srv.Close()

	a := nuevoAgente(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 2, timeout: 5 * time.Second})
	if err := a.turno("insiste"); err != nil {
		t.Fatalf("turno devolvió error: %v", err)
	}

	if peticiones != 3 { // 2 iteraciones + 1 forzada
		t.Fatalf("se esperaban 3 peticiones, hubo %d", peticiones)
	}
	if ultimoToolChoice != "none" {
		t.Fatalf("la última petición debía llevar tool_choice=none, llevó %q", ultimoToolChoice)
	}
}

func TestTurnoRevierteHistorialSiFallaLaRed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	a := nuevoAgente(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 2, timeout: 5 * time.Second})
	antes := len(a.hist)
	if err := a.turno("algo"); err == nil {
		t.Fatal("se esperaba un error de red")
	}
	if len(a.hist) != antes {
		t.Fatalf("el historial quedó inconsistente: %d mensajes (antes %d)", len(a.hist), antes)
	}
}

func TestRecortarHistorial(t *testing.T) {
	a := nuevoAgente(config{})
	a.hist = append(a.hist, message{Role: "tool", Content: "huérfano", ToolCallID: "x"})
	for i := 0; i < maxHistorial+10; i++ {
		a.hist = append(a.hist, message{Role: "user", Content: fmt.Sprintf("m%d", i)})
	}
	a.recortarHistorial()

	if len(a.hist) > maxHistorial {
		t.Fatalf("historial sin recortar: %d mensajes", len(a.hist))
	}
	if a.hist[0].Role != "system" {
		t.Fatalf("el prompt de sistema debe permanecer primero, hay %q", a.hist[0].Role)
	}
	if a.hist[1].Role == "tool" {
		t.Fatal("quedó un resultado de herramienta huérfano al inicio del historial")
	}
}

// --- Configuración ----------------------------------------------------------

func TestGetEnv(t *testing.T) {
	t.Setenv("STARLIGHT_TEST_ENV", "  valor  ")
	if got := getEnv("STARLIGHT_TEST_ENV", "defecto"); got != "valor" {
		t.Fatalf("getEnv = %q", got)
	}
	t.Setenv("STARLIGHT_TEST_ENV", "   ")
	if got := getEnv("STARLIGHT_TEST_ENV", "defecto"); got != "defecto" {
		t.Fatalf("un valor en blanco debe caer al default, se obtuvo %q", got)
	}
}

func TestDefinicionHerramientas(t *testing.T) {
	herramientas := definicionHerramientas()
	if len(herramientas) != 2 {
		t.Fatalf("se esperaban 2 herramientas, hay %d", len(herramientas))
	}
	nombres := []string{herramientas[0].Function.Name, herramientas[1].Function.Name}
	if nombres[0] != "leer_archivo" || nombres[1] != "ejecutar_comando" {
		t.Fatalf("nombres inesperados: %v", nombres)
	}
	for _, h := range herramientas {
		if h.Type != "function" || h.Function.Description == "" || h.Function.Parameters["type"] != "object" {
			t.Fatalf("definición incompleta para %s", h.Function.Name)
		}
	}
}
